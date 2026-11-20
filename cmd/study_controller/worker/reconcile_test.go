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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/experiment"
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

func TestNewESSReconciler_RejectsNilReader(t *testing.T) {
	t.Parallel()
	if _, err := NewESSReconciler(nil); err == nil {
		t.Fatal("NewESSReconciler(nil) error = nil, want error")
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
			StudyConfig: &spb.StudyConfig{
				Algorithm:      "RANDOM_SEARCH",
				MaxTrialBudget: 10,
			},
		},
	}

	tests := []struct {
		name        string
		exp         *spb.Experiment
		err         error
		wantRetry   bool
		wantErrCode codes.Code
		wantLogs    []string
	}{
		{
			name: "active STUDY proceeds and ACKs", exp: activeStudy,
			wantLogs: []string{"Verified active study on ESS", "experiment_type=STUDY", "active_state=ACTIVE", "launch_state=LAUNCHING", "algorithm=RANDOM_SEARCH", "max_trial_budget=10"},
		},
		{
			name:     "STANDARD active experiment is skipped and ACKed",
			exp:      &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STANDARD, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE}},
			wantLogs: []string{"Skipping study reconcile tick: experiment is not a STUDY", "experiment_type=STANDARD"},
		},
		{
			name:     "UNSPECIFIED experiment type is skipped and ACKed",
			exp:      &spb.Experiment{Name: "experiments/42", Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE}},
			wantLogs: []string{"Skipping study reconcile tick: experiment is not a STUDY", "experiment_type=EXPERIMENT_TYPE_UNSPECIFIED"},
		},
		{
			name:     "inactive STUDY is skipped and ACKed",
			exp:      &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_INACTIVE, LaunchState: spb.ExperimentStatus_COMPLETED}},
			wantLogs: []string{"Skipping study reconcile tick: study is not active", "active_state=INACTIVE", "launch_state=COMPLETED"},
		},
		{
			name:     "STUDY with unspecified active state is skipped and ACKed",
			exp:      &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY, Status: &spb.ExperimentStatus{ActiveState: spb.ExperimentStatus_ACTIVE_STATE_UNSPECIFIED}},
			wantLogs: []string{"Skipping study reconcile tick: study is not active", "active_state=ACTIVE_STATE_UNSPECIFIED"},
		},
		{
			name:     "STUDY with nil status is skipped and ACKed",
			exp:      &spb.Experiment{Name: "experiments/42", ExperimentType: spb.Experiment_STUDY},
			wantLogs: []string{"Skipping study reconcile tick: study is not active", "active_state=ACTIVE_STATE_UNSPECIFIED"},
		},
		{name: "NotFound is skipped and ACKed", err: status.Error(codes.NotFound, "not found"), wantLogs: []string{"Skipping study reconcile tick: experiment not found"}},
		{name: "wrapped NotFound is skipped and ACKed", err: fmt.Errorf("wrap: %w", status.Error(codes.NotFound, "not found")), wantLogs: []string{"Skipping study reconcile tick: experiment not found"}},
		{name: "nil experiment without error is retried", exp: nil, wantRetry: true},
		{name: "Unavailable is retried", err: status.Error(codes.Unavailable, "ess restarting"), wantRetry: true, wantErrCode: codes.Unavailable},
		{name: "Internal is retried", err: status.Error(codes.Internal, "db error"), wantRetry: true, wantErrCode: codes.Internal},
		{name: "PermissionDenied is retried", err: status.Error(codes.PermissionDenied, "not authorized"), wantRetry: true, wantErrCode: codes.PermissionDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeExperimentReader{exp: tc.exp, err: tc.err}
			reconcile, err := NewESSReconciler(reader)
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
			if res.ShouldBeRetried != tc.wantRetry || res.WasRunSuccessful == tc.wantRetry {
				t.Errorf("HandleTask() = %+v, want retry=%v success=%v", res, tc.wantRetry, !tc.wantRetry)
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
}
