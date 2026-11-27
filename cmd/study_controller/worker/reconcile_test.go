// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package worker

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/pkg/study"
)

type fakeExperimentReader struct {
	exp     *spb.Experiment
	err     error
	gotName string
}

func (f *fakeExperimentReader) GetExperiment(_ context.Context, name string) (*spb.Experiment, error) {
	f.gotName = name
	if f.err != nil {
		return nil, f.err
	}
	return f.exp, nil
}

type fakeStudyEngine struct {
	calls int
	xid   int64
	cfg   *spb.StudyConfig
	err   error
}

func (f *fakeStudyEngine) CreateStudy(_ context.Context, xid int64, cfg *spb.StudyConfig) (string, error) {
	f.calls++
	f.xid = xid
	f.cfg = cfg
	if f.err != nil {
		return "", f.err
	}
	return fmt.Sprintf("owners/xmc/studies/study-%d", xid), nil
}

func TestNewESSReconciler_RejectsNilDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewESSReconciler(nil, &fakeStudyEngine{}); err == nil {
		t.Fatal("NewESSReconciler(nil, engine) error = nil, want error")
	}
	if _, err := NewESSReconciler(&fakeExperimentReader{}, nil); err == nil {
		t.Fatal("NewESSReconciler(reader, nil) error = nil, want error")
	}
}

func TestESSReconciler_GateMatrix(t *testing.T) {
	activeStudy := &spb.Experiment{
		Name:           "experiments/42",
		ExperimentType: spb.Experiment_STUDY,
		Status: &spb.ExperimentStatus{
			ActiveState: spb.ExperimentStatus_ACTIVE,
			LaunchState: spb.ExperimentStatus_LAUNCHING,
		},
		TypeConfig: &spb.Experiment_StudyConfig{
			StudyConfig: experimenttest.StudyConfig(),
		},
	}

	tests := []struct {
		name            string
		exp             *spb.Experiment
		err             error
		engineErr       error
		wantSuccess     bool
		wantRetry       bool
		wantErrCode     codes.Code
		wantEngineCalls int
		wantLogs        []string
	}{
		{
			name:            "active STUDY registers in Vizier and ACKs",
			exp:             activeStudy,
			wantSuccess:     true,
			wantEngineCalls: 1,
			wantLogs:        []string{"Registered active study in Vizier", "vizier_study=owners/xmc/studies/study-42", "launch_state=LAUNCHING", "max_trial_budget=10"},
		},
		{
			name:            "active STUDY transient engine error retries",
			exp:             activeStudy,
			engineErr:       status.Error(codes.Unavailable, "vizier down"),
			wantRetry:       true,
			wantErrCode:     codes.Unavailable,
			wantEngineCalls: 1,
		},
		{
			name:            "active STUDY invalid config dead-letters without retry",
			exp:             activeStudy,
			engineErr:       fmt.Errorf("%w: bad param", study.ErrInvalidStudyConfig),
			wantEngineCalls: 1,
			wantLogs:        []string{"Dropping invalid study config tick"},
		},
		{
			name:        "STANDARD active experiment is skipped and ACKed",
			exp:         &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STANDARD, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE}},
			wantSuccess: true,
			wantLogs:    []string{"Skipping study reconcile tick: experiment is not a STUDY", "experiment_type=STANDARD"},
		},
		{
			name:        "UNSPECIFIED experiment type is skipped and ACKed",
			exp:         &spb.Experiment{Name: "experiments/42", Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE}},
			wantSuccess: true,
			wantLogs:    []string{"Skipping study reconcile tick: experiment is not a STUDY", "experiment_type=EXPERIMENT_TYPE_UNSPECIFIED"},
		},
		{
			name:        "inactive STUDY is skipped and ACKed",
			exp:         &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_INACTIVE, LaunchState: spb.ExperimentStatus_COMPLETED}},
			wantSuccess: true,
			wantLogs:    []string{"Skipping study reconcile tick: study is not active", "active_state=INACTIVE", "launch_state=COMPLETED"},
		},
		{
			name:        "STUDY with unspecified active state is skipped and ACKed",
			exp:         &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE_STATE_UNSPECIFIED}},
			wantSuccess: true,
			wantLogs:    []string{"Skipping study reconcile tick: study is not active", "active_state=ACTIVE_STATE_UNSPECIFIED"},
		},
		{
			name:        "STUDY with nil status is skipped and ACKed",
			exp:         &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY},
			wantSuccess: true,
			wantLogs:    []string{"Skipping study reconcile tick: study is not active", "active_state=ACTIVE_STATE_UNSPECIFIED"},
		},
		{name: "NotFound is skipped and ACKed", err: status.Error(codes.NotFound, "not found"), wantSuccess: true, wantLogs: []string{"Skipping study reconcile tick: experiment not found"}},
		{name: "wrapped NotFound is skipped and ACKed", err: fmt.Errorf("wrap: %w", status.Error(codes.NotFound, "not found")), wantSuccess: true, wantLogs: []string{"Skipping study reconcile tick: experiment not found"}},
		{name: "nil experiment without error is retried", exp: nil, wantRetry: true},
		{name: "Unavailable is retried", err: status.Error(codes.Unavailable, "ess restarting"), wantRetry: true, wantErrCode: codes.Unavailable},
		{name: "Internal is retried", err: status.Error(codes.Internal, "db error"), wantRetry: true, wantErrCode: codes.Internal},
		{name: "PermissionDenied is retried", err: status.Error(codes.PermissionDenied, "not authorized"), wantRetry: true, wantErrCode: codes.PermissionDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeExperimentReader{exp: tc.exp, err: tc.err}
			engine := &fakeStudyEngine{err: tc.engineErr}
			reconcile, err := NewESSReconciler(reader, engine)
			if err != nil {
				t.Fatalf("NewESSReconciler() error = %v", err)
			}
			h, err := NewHandler(HandlerParams{
				Locker:    &fakeLocker{lease: &fakeLease{}},
				Reconcile: reconcile,
			})
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			var logs bytes.Buffer
			res := h.HandleTask(newLogContext(&logs), "worker-1", `{"experiment":"experiments/42"}`)

			if reader.gotName != "experiments/42" {
				t.Errorf("GetExperiment name = %q, want %q", reader.gotName, "experiments/42")
			}
			if engine.calls != tc.wantEngineCalls {
				t.Errorf("CreateStudy calls = %d, want %d", engine.calls, tc.wantEngineCalls)
			}
			if tc.wantEngineCalls > 0 {
				if engine.xid != 42 {
					t.Errorf("CreateStudy xid = %d, want 42", engine.xid)
				}
				if diff := cmp.Diff(tc.exp.GetStudyConfig(), engine.cfg, protocmp.Transform()); diff != "" {
					t.Errorf("CreateStudy cfg mismatch (-want +got):\n%s", diff)
				}
			}
			if res.ShouldBeRetried != tc.wantRetry || res.WasRunSuccessful != tc.wantSuccess {
				t.Errorf("HandleTask() = %+v, want retry=%v success=%v", res, tc.wantRetry, tc.wantSuccess)
			}
			if tc.wantErrCode != codes.OK && status.Code(res.Error) != tc.wantErrCode {
				t.Errorf("HandleTask() error code = %v, want %v (err=%v)", status.Code(res.Error), tc.wantErrCode, res.Error)
			}
			for _, wantLog := range tc.wantLogs {
				if !strings.Contains(logs.String(), wantLog) {
					t.Errorf("logs = %q, want %q", logs.String(), wantLog)
				}
			}
		})
	}

	t.Run("repeated ticks for active STUDY succeed idempotently", func(t *testing.T) {
		reader := &fakeExperimentReader{exp: activeStudy}
		engine := &fakeStudyEngine{}
		reconcile, err := NewESSReconciler(reader, engine)
		if err != nil {
			t.Fatalf("NewESSReconciler() error = %v", err)
		}
		h, _ := NewHandler(HandlerParams{Locker: &fakeLocker{lease: &fakeLease{}}, Reconcile: reconcile})
		for i := range 2 {
			res := h.HandleTask(context.Background(), "worker-1", `{"experiment":"experiments/42"}`)
			if !res.WasRunSuccessful || res.ShouldBeRetried {
				t.Fatalf("tick %d HandleTask() = %+v, want success", i+1, res)
			}
		}
		if engine.calls != 2 {
			t.Errorf("CreateStudy calls = %d, want 2", engine.calls)
		}
	})
}
