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

package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/task_queue_redis/redisclient"

	msgpb "xmanager_cloud/api/protos/experiment"
	taskpb "xmanager_cloud/api/protos/task_queue"
	xidmsgpb "xmanager_cloud/api/protos/xid"
)

// createExperimentWithPublisher creates an experiment of expType with xid 123 on a test server
// whose study reconcile publisher is publisher.
func createExperimentWithPublisher(t *testing.T, expType msgpb.Experiment_ExperimentType, publisher StudyReconcilePublisher) (*msgpb.Experiment, error) {
	t.Helper()
	server := newTestServer(t,
		withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}),
		withXidClient(&mockXidClient{createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")}}),
		withStorage(&storage.MockStorage{ComponentRepository: &storage.MockComponentRepository{Records: readyHPOCatalog()}}),
		withStudyReconcilePublisher(publisher),
	)
	req := &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{Title: "exp", ExperimentType: expType}}
	if expType == msgpb.Experiment_STUDY {
		req.Experiment.TypeConfig = &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}
	}
	return server.CreateExperiment(context.Background(), req)
}

// decodeStudyReconcilePayloads decodes every message published to the fake publisher.
func decodeStudyReconcilePayloads(t *testing.T, publisher *redisclient.FakeClient) []*taskpb.StudyReconcilePayload {
	t.Helper()
	var got []*taskpb.StudyReconcilePayload
	for _, msg := range publisher.Messages {
		payload := &taskpb.StudyReconcilePayload{}
		if err := protojson.Unmarshal([]byte(msg), payload); err != nil {
			t.Fatalf("published message %q is not a protojson StudyReconcilePayload: %v", msg, err)
		}
		got = append(got, payload)
	}
	return got
}

func TestCreateExperiment_EnqueuesStudyReconcileTick(t *testing.T) {
	tests := []struct {
		name    string
		expType msgpb.Experiment_ExperimentType
		want    []*taskpb.StudyReconcilePayload
	}{
		{name: "study enqueues one tick", expType: msgpb.Experiment_STUDY,
			want: []*taskpb.StudyReconcilePayload{{Experiment: proto.String("experiments/123")}}},
		{name: "standard enqueues nothing", expType: msgpb.Experiment_STANDARD},
		{name: "unspecified type is standard and enqueues nothing", expType: msgpb.Experiment_EXPERIMENT_TYPE_UNSPECIFIED},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			publisher := redisclient.NewFakeClient()

			if _, err := createExperimentWithPublisher(t, tc.expType, publisher); err != nil {
				t.Fatalf("CreateExperiment(%v) error = %v, want nil", tc.expType, err)
			}

			if diff := cmp.Diff(tc.want, decodeStudyReconcilePayloads(t, publisher), protocmp.Transform()); diff != "" {
				t.Errorf("CreateExperiment(%v) published payloads mismatch (-want +got):\n%s", tc.expType, diff)
			}
		})
	}
}

func TestCreateExperiment_StudyReconcileEnqueueFailureStillSucceeds(t *testing.T) {
	publisher := redisclient.NewFakeClient()
	publisher.AddMessageError = errors.New("redis unavailable")
	before := testutil.ToFloat64(studyReconcileEnqueueFailuresTotal)

	got, err := createExperimentWithPublisher(t, msgpb.Experiment_STUDY, publisher)

	if err != nil {
		t.Fatalf("CreateExperiment(STUDY) with a failing publisher error = %v, want nil", err)
	}
	if got.GetName() != "experiments/123" || got.GetExperimentType() != msgpb.Experiment_STUDY {
		t.Errorf("CreateExperiment(STUDY) = %v, want the created study experiments/123", got)
	}
	if delta := testutil.ToFloat64(studyReconcileEnqueueFailuresTotal) - before; delta != 1 {
		t.Errorf("%s increased by %v, want 1", "xmc_ess_study_reconcile_enqueue_failures_total", delta)
	}
}

func TestPublishStudyReconcile_InvalidNameIsNotPublished(t *testing.T) {
	publisher := redisclient.NewFakeClient()
	server := newTestServer(t, withStudyReconcilePublisher(publisher))

	// protojson rejects strings that are not valid UTF-8.
	err := server.publishStudyReconcile(context.Background(), "experiments/\xff")

	if err == nil {
		t.Error("publishStudyReconcile() with an invalid UTF-8 name error = nil, want an encoding error")
	}
	if len(publisher.Messages) != 0 {
		t.Errorf("publishStudyReconcile() published %q, want nothing", publisher.Messages)
	}
}

// recordingPublisher records the context of each AddMessage call.
type recordingPublisher struct {
	ctxErr      error
	deadline    time.Time
	hasDeadline bool
	calls       int
}

func (p *recordingPublisher) AddMessage(ctx context.Context, _, _ string) error {
	p.calls++
	p.ctxErr = ctx.Err()
	p.deadline, p.hasDeadline = ctx.Deadline()
	return nil
}

func TestEnqueueStudyReconcile_SurvivesRequestCancellation(t *testing.T) {
	publisher := &recordingPublisher{}
	server := newTestServer(t, withStudyReconcilePublisher(publisher))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()

	server.enqueueStudyReconcile(ctx, &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY})

	if publisher.calls != 1 || publisher.ctxErr != nil {
		t.Fatalf("publish after the request was cancelled: %d calls, ctx error %v; want 1 call on a live context", publisher.calls, publisher.ctxErr)
	}
	if !publisher.hasDeadline || publisher.deadline.After(start.Add(studyReconcilePublishTimeout+time.Second)) {
		t.Errorf("publish deadline = %v (set %v), want within %v", publisher.deadline, publisher.hasDeadline, studyReconcilePublishTimeout)
	}
}

// startSilentRedis returns the address of a TCP server that accepts connections and never
// replies, like a hung Redis server.
func startSilentRedis(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		lis.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	return lis.Addr().String()
}

// TestCreateExperiment_HungRedisBoundedByPublishTimeout uses the production publisher settings
// against a Redis server that never replies.
func TestCreateExperiment_HungRedisBoundedByPublishTimeout(t *testing.T) {
	publisher, cleanup := initStudyReconcilePublisher(&config.Config{RedisAddr: startSilentRedis(t)})
	defer cleanup()
	before := testutil.ToFloat64(studyReconcileEnqueueFailuresTotal)

	start := time.Now()
	got, err := createExperimentWithPublisher(t, msgpb.Experiment_STUDY, publisher)
	elapsed := time.Since(start)

	if err != nil || got.GetName() != "experiments/123" {
		t.Fatalf("CreateExperiment(STUDY) with a hung Redis = (%v, %v), want the created study", got, err)
	}
	if limit := studyReconcilePublishTimeout + time.Second; elapsed > limit {
		t.Errorf("CreateExperiment(STUDY) with a hung Redis took %v, want at most %v", elapsed, limit)
	}
	if delta := testutil.ToFloat64(studyReconcileEnqueueFailuresTotal) - before; delta != 1 {
		t.Errorf("xmc_ess_study_reconcile_enqueue_failures_total increased by %v, want 1", delta)
	}
}
