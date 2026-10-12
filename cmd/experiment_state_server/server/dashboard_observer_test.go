// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	dashboard_apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func chartArtifactFixture() *essmsgpb.Artifact {
	return &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{Title: "Custom Title"},
			},
		},
	}
}

type recordingStorage struct {
	*storage.MockStorage
	mu            sync.Mutex
	updateUpdates []*storage.ArtifactUpdate
	// updateCtxErrs records ctx.Err() as each write was attempted, so tests can tell a write
	// that reached storage from one that was refused by an already-finished context.
	updateCtxErrs []error
}

func (s *recordingStorage) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	s.mu.Lock()
	s.updateUpdates = append(s.updateUpdates, update)
	s.updateCtxErrs = append(s.updateCtxErrs, ctx.Err())
	s.mu.Unlock()
	return s.MockStorage.UpdateArtifact(ctx, update)
}

func TestNewDashboardObserver(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	obs := NewDashboardObserver(toolClient, &storage.MockStorage{}, &wg)
	if obs == nil {
		t.Fatal("NewDashboardObserver() returned nil")
	}
}

func TestDashboardObserver_OnArtifactCreated_NonChartArtifact(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Url{
				Url: &essmsgpb.UrlArtifact{Url: "http://example.com"},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), artifact)
	wg.Wait()

	if len(mockDash.reqs) != 0 {
		t.Errorf("len(mockDash.reqs) = %d, want 0", len(mockDash.reqs))
	}
	if len(recStorage.updateUpdates) != 0 {
		t.Errorf("len(recStorage.updateUpdates) = %d, want 0", len(recStorage.updateUpdates))
	}
}

func TestDashboardObserver_OnArtifactCreated_Success(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{
		createChartResp: &dashboard_apipb.Chart{
			Title: "Custom Title",
		},
	}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title:       "Custom Title",
					MetricNames: []string{"loss", "accuracy"},
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	req := mockDash.reqs[0]
	if req.GetParent() != "experiments/100/workUnits/200" {
		t.Errorf("req.Parent = %q, want 'experiments/100/workUnits/200'", req.GetParent())
	}
	if req.GetChart().GetTitle() != "Custom Title" {
		t.Errorf("req.Chart.Title = %q, want 'Custom Title'", req.GetChart().GetTitle())
	}
	if req.GetChart().GetXid() != 100 || req.GetChart().GetWid() != 200 {
		t.Errorf("req.Chart xid/wid = (%d, %d), want (100, 200)", req.GetChart().GetXid(), req.GetChart().GetWid())
	}

	if len(recStorage.updateUpdates) != 1 {
		t.Fatalf("len(recStorage.updateUpdates) = %d, want 1", len(recStorage.updateUpdates))
	}
	up := recStorage.updateUpdates[0]
	if up.AID != 300 {
		t.Errorf("up.AID = %d, want 300", up.AID)
	}
	if up.Artifact.GetPayload().GetChart().GetStatus() != essmsgpb.ChartArtifact_SUCCESS {
		t.Errorf("Chart status = %v, want SUCCESS", up.Artifact.GetPayload().GetChart().GetStatus())
	}
	if up.UpdateMask == nil || len(up.UpdateMask.Paths) != 1 || up.UpdateMask.Paths[0] != "payload" {
		t.Errorf("up.UpdateMask = %v, want ['payload']", up.UpdateMask)
	}
}

func TestDashboardObserver_OnArtifactCreated_DerivesPlotsFromMetricNames(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{
		createChartResp: &dashboard_apipb.Chart{
			Name:  "experiments/100/workUnits/200/charts/b6f1c0a2",
			Title: "Custom Title",
		},
	}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title:       "Custom Title",
					MetricNames: []string{"chart_demo/loss", "chart_demo/accuracy"},
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	chart := mockDash.reqs[0].GetChart()

	wantPlots := []struct {
		plotID string
		typ    dashboard_apipb.PlotType
		metric string
	}{
		{"chart_demo_loss_latest", dashboard_apipb.PlotType_SUMMARY_BOX, "chart_demo/loss"},
		{"chart_demo_loss_curve", dashboard_apipb.PlotType_SCATTER, "chart_demo/loss"},
		{"chart_demo_accuracy_latest", dashboard_apipb.PlotType_SUMMARY_BOX, "chart_demo/accuracy"},
		{"chart_demo_accuracy_curve", dashboard_apipb.PlotType_SCATTER, "chart_demo/accuracy"},
	}
	if len(chart.GetPlots()) != len(wantPlots) {
		t.Fatalf("len(chart.Plots) = %d, want %d", len(chart.GetPlots()), len(wantPlots))
	}
	for i, want := range wantPlots {
		got := chart.GetPlots()[i]
		if got.GetPlotId() != want.plotID {
			t.Errorf("chart.Plots[%d].PlotId = %q, want %q", i, got.GetPlotId(), want.plotID)
		}
		if got.GetType() != want.typ {
			t.Errorf("chart.Plots[%d].Type = %v, want %v", i, got.GetType(), want.typ)
		}
		if metrics := got.GetData().GetMetrics(); len(metrics) != 1 || metrics[0] != want.metric {
			t.Errorf("chart.Plots[%d].Data.Metrics = %v, want [%q]", i, metrics, want.metric)
		}
	}
	if chart.GetPlotLayout().GetType() != dashboard_apipb.LayoutSpec_GRID {
		t.Errorf("chart.PlotLayout.Type = %v, want GRID", chart.GetPlotLayout().GetType())
	}

	// The artifact must carry the server-assigned chart, otherwise a reader has the metric
	// names but no way to find the chart that plots them.
	if len(recStorage.updateUpdates) != 1 {
		t.Fatalf("len(recStorage.updateUpdates) = %d, want 1", len(recStorage.updateUpdates))
	}
	gotChart := recStorage.updateUpdates[0].Artifact.GetPayload().GetChart().GetChart()
	if gotChart.GetName() != "experiments/100/workUnits/200/charts/b6f1c0a2" {
		t.Errorf("artifact chart name = %q, want the name CreateChart returned", gotChart.GetName())
	}
}

func TestDashboardObserver_OnArtifactCreated_NoPlotsWithoutMetricNames(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{Title: "No Metrics"},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	if plots := mockDash.reqs[0].GetChart().GetPlots(); len(plots) != 0 {
		t.Errorf("len(chart.Plots) = %d, want 0", len(plots))
	}
}

func TestPlotSpecsForMetrics(t *testing.T) {
	t.Parallel()

	t.Run("skips blank metric names", func(t *testing.T) {
		t.Parallel()
		plots := plotSpecsForMetrics(t.Context(), []string{"  ", "", "loss"})
		if len(plots) != 2 {
			t.Fatalf("len(plots) = %d, want 2", len(plots))
		}
		if got := plots[0].GetPlotId(); got != "loss_latest" {
			t.Errorf("plots[0].PlotId = %q, want 'loss_latest'", got)
		}
	})

	t.Run("disambiguates metric names that slugify alike", func(t *testing.T) {
		t.Parallel()
		plots := plotSpecsForMetrics(t.Context(), []string{"train/loss", "train_loss", "TRAIN.LOSS"})
		want := []string{
			"train_loss_latest", "train_loss_curve",
			"train_loss_2_latest", "train_loss_2_curve",
			"train_loss_3_latest", "train_loss_3_curve",
		}
		if len(plots) != len(want) {
			t.Fatalf("len(plots) = %d, want %d", len(plots), len(want))
		}
		for i, w := range want {
			if got := plots[i].GetPlotId(); got != w {
				t.Errorf("plots[%d].PlotId = %q, want %q", i, got, w)
			}
		}
	})

	t.Run("does not reuse an ID a metric name already claimed", func(t *testing.T) {
		t.Parallel()
		// "foo_2" is both a metric in its own right and the suffix the third metric would be
		// given by a naive occurrence counter, which would hand out foo_2 twice. The UI keys
		// plot data by ID, so the duplicate would draw one metric's series under both plots.
		plots := plotSpecsForMetrics(t.Context(), []string{"foo", "foo_2", "foo"})
		want := []string{
			"foo_latest", "foo_curve",
			"foo_2_latest", "foo_2_curve",
			"foo_3_latest", "foo_3_curve",
		}
		if len(plots) != len(want) {
			t.Fatalf("len(plots) = %d, want %d", len(plots), len(want))
		}
		seen := make(map[string]bool, len(plots))
		for i, w := range want {
			got := plots[i].GetPlotId()
			if got != w {
				t.Errorf("plots[%d].PlotId = %q, want %q", i, got, w)
			}
			if seen[got] {
				t.Errorf("plots[%d].PlotId = %q is a duplicate", i, got)
			}
			seen[got] = true
		}
	})

	t.Run("falls back to a placeholder slug when a name has no usable characters", func(t *testing.T) {
		t.Parallel()
		plots := plotSpecsForMetrics(t.Context(), []string{"///"})
		if len(plots) != 2 {
			t.Fatalf("len(plots) = %d, want 2", len(plots))
		}
		if got := plots[0].GetPlotId(); got != "metric_latest" {
			t.Errorf("plots[0].PlotId = %q, want 'metric_latest'", got)
		}
	})

	t.Run("caps the number of metrics it expands", func(t *testing.T) {
		t.Parallel()
		metrics := make([]string, maxProvisionedMetrics+5)
		for i := range metrics {
			metrics[i] = fmt.Sprintf("metric/%d", i)
		}
		var logs bytes.Buffer
		ctx := logging.WithLogger(t.Context(), slog.New(slog.NewJSONHandler(&logs, nil)))
		plots := plotSpecsForMetrics(ctx, metrics)
		if want := 2 * maxProvisionedMetrics; len(plots) != want {
			t.Errorf("len(plots) = %d, want %d", len(plots), want)
		}
		// The dropped metrics are invisible in the rendered chart, so the log line is the only
		// answer to why a metric the caller named has no plot.
		if got := logs.String(); !strings.Contains(got, `"dropped":5`) {
			t.Errorf("plotSpecsForMetrics() logged %q, want a warning reporting 5 dropped metrics", got)
		}
	})

	t.Run("maps scatter plots against the step axis", func(t *testing.T) {
		t.Parallel()
		plots := plotSpecsForMetrics(t.Context(), []string{"loss"})
		if got := plots[1].GetMapping().GetX(); got != "step" {
			t.Errorf("plots[1].Mapping.X = %q, want 'step'", got)
		}
	})
}

func TestDashboardObserver_OnArtifactCreated_DefaultTitle(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title: "",
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	wantTitle := "Chart for XID 100 WID 200"
	if mockDash.reqs[0].GetChart().GetTitle() != wantTitle {
		t.Errorf("req.Chart.Title = %q, want %q", mockDash.reqs[0].GetChart().GetTitle(), wantTitle)
	}
}

func TestDashboardObserver_OnArtifactCreated_ClientError(t *testing.T) {
	t.Parallel()
	clientErr := errors.New("dashboard service down")
	mockDash := &mockDashboardClient{
		createChartErr: clientErr,
	}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title: "Failing Chart",
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(recStorage.updateUpdates) != 1 {
		t.Fatalf("len(recStorage.updateUpdates) = %d, want 1", len(recStorage.updateUpdates))
	}
	up := recStorage.updateUpdates[0]
	chart := up.Artifact.GetPayload().GetChart()
	if chart.GetStatus() != essmsgpb.ChartArtifact_FAILED {
		t.Errorf("Chart status = %v, want FAILED", chart.GetStatus())
	}
	if chart.GetErrorMessage() != clientErr.Error() {
		t.Errorf("Chart error message = %q, want %q", chart.GetErrorMessage(), clientErr.Error())
	}
}

func TestWithForwardedAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		incoming metadata.MD
		// outgoing is metadata already on the context before the call, standing in for an
		// interceptor that attached its own headers upstream.
		outgoing metadata.MD
		// noIncoming builds the context without any incoming metadata at all, which is not the
		// same as incoming metadata that happens to be empty.
		noIncoming bool
		wantOK     bool
		wantOut    metadata.MD
	}{
		{
			name:     "forwards a bearer token",
			incoming: metadata.Pairs("authorization", "Bearer token-abc"),
			wantOK:   true,
			wantOut:  metadata.Pairs("authorization", "Bearer token-abc"),
		},
		{
			name:     "forwards an IAP assertion",
			incoming: metadata.Pairs("x-goog-iap-jwt-assertion", "assertion-abc"),
			wantOK:   true,
			wantOut:  metadata.Pairs("x-goog-iap-jwt-assertion", "assertion-abc"),
		},
		{
			name: "forwards both credentials and nothing else",
			incoming: metadata.Pairs(
				"authorization", "Bearer token-abc",
				"x-goog-iap-jwt-assertion", "assertion-abc",
				"x-request-id", "req-1",
			),
			wantOK: true,
			wantOut: metadata.Pairs(
				"authorization", "Bearer token-abc",
				"x-goog-iap-jwt-assertion", "assertion-abc",
			),
		},
		{
			name: "forwards the caller identity alongside the credential",
			incoming: metadata.Pairs(
				"authorization", "Bearer token-abc",
				"x-goog-user-email", "user@example.com",
			),
			wantOK: true,
			wantOut: metadata.Pairs(
				"authorization", "Bearer token-abc",
				"x-goog-user-email", "user@example.com",
			),
		},
		{
			// The email is an identity hint the Dashboard Service only honors next to a valid
			// token, so on its own it is not something to report as forwarded credentials.
			name:     "does not count the caller identity as a credential",
			incoming: metadata.Pairs("x-goog-user-email", "user@example.com"),
			wantOK:   false,
		},
		{
			// internal/auth ignores a header whose first value is empty, so forwarding one and
			// calling it a credential would suppress the warning for a call that has none.
			name:     "treats an empty credential as absent",
			incoming: metadata.Pairs("authorization", ""),
			wantOK:   false,
		},
		{
			name:     "reports no credentials when the call carried none",
			incoming: metadata.Pairs("x-request-id", "req-1"),
			wantOK:   false,
		},
		{
			name:       "reports no credentials when the call carried no metadata",
			noIncoming: true,
			wantOK:     false,
		},
		{
			name:     "adds the credentials to outgoing metadata already on the context",
			incoming: metadata.Pairs("authorization", "Bearer token-abc"),
			outgoing: metadata.Pairs("x-trace-id", "trace-1"),
			wantOK:   true,
			wantOut: metadata.Pairs(
				"authorization", "Bearer token-abc",
				"x-trace-id", "trace-1",
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if !tc.noIncoming {
				ctx = metadata.NewIncomingContext(ctx, tc.incoming)
			}
			if len(tc.outgoing) > 0 {
				ctx = metadata.NewOutgoingContext(ctx, tc.outgoing)
			}

			got, ok := withForwardedAuth(ctx)
			if ok != tc.wantOK {
				t.Errorf("withForwardedAuth() ok = %v, want %v", ok, tc.wantOK)
			}
			outgoing, hasOutgoing := metadata.FromOutgoingContext(got)
			if !tc.wantOK {
				if hasOutgoing {
					t.Errorf("withForwardedAuth() attached outgoing metadata %v, want none", outgoing)
				}
				return
			}
			if !hasOutgoing {
				t.Fatal("withForwardedAuth() attached no outgoing metadata, want the credentials")
			}
			if diff := len(outgoing) - len(tc.wantOut); diff != 0 {
				t.Errorf("len(outgoing) = %d, want %d (got %v)", len(outgoing), len(tc.wantOut), outgoing)
			}
			for key, want := range tc.wantOut {
				if got := outgoing.Get(key); len(got) != 1 || got[0] != want[0] {
					t.Errorf("outgoing[%q] = %v, want %v", key, got, want)
				}
			}
		})
	}
}

func TestDashboardObserver_OnArtifactCreated_ForwardsCallerCredentials(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	obs := NewDashboardObserver(toolClient, &storage.MockStorage{}, &wg)

	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", "Bearer token-abc"))
	obs.OnArtifactCreated(ctx, chartArtifactFixture())
	wg.Wait()

	if len(mockDash.outgoingMD) != 1 {
		t.Fatalf("len(mockDash.outgoingMD) = %d, want 1", len(mockDash.outgoingMD))
	}
	// Without this the Dashboard Service sees an anonymous call and rejects CreateChart with
	// Unauthenticated, which the UI then shows in place of the chart.
	if got := mockDash.outgoingMD[0].Get("authorization"); len(got) != 1 || got[0] != "Bearer token-abc" {
		t.Errorf("CreateChart outgoing authorization = %v, want ['Bearer token-abc']", got)
	}
}

func TestDashboardObserver_OnArtifactCreated_UncredentialedCallerStillCalls(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	obs := NewDashboardObserver(toolClient, &storage.MockStorage{}, &wg)

	// Whether an uncredentialed call is acceptable is the Dashboard Service's decision, not the
	// observer's, so it logs and calls anyway rather than failing the artifact itself.
	obs.OnArtifactCreated(t.Context(), chartArtifactFixture())
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
}

func TestDashboardObserver_OnArtifactCreated_PersistsOutcomeAfterDeadline(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{createChartErr: errors.New("deadline exceeded")}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	// A context that is already finished stands in for the real case: CreateChart spending the
	// whole two-minute budget. The status write has to survive it, or the FAILED status never
	// lands and the artifact stays PROVISIONING with no goroutine left to move it.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	artifact := chartArtifactFixture()
	obs.OnArtifactCreated(ctx, artifact)
	wg.Wait()

	if len(recStorage.updateCtxErrs) != 1 {
		t.Fatalf("len(recStorage.updateCtxErrs) = %d, want 1", len(recStorage.updateCtxErrs))
	}
	if err := recStorage.updateCtxErrs[0]; err != nil {
		t.Errorf("UpdateArtifact context was already done (%v); the FAILED status would not persist", err)
	}
	if got := artifact.GetPayload().GetChart().GetStatus(); got != essmsgpb.ChartArtifact_FAILED {
		t.Errorf("status = %v, want FAILED", got)
	}
}

func TestDashboardObserver_OnArtifactCreated_StorageUpdateError(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	mockStorage := &storage.MockStorage{
		UpdateArtifactErr: errors.New("spanner transaction aborted"),
	}
	obs := NewDashboardObserver(toolClient, mockStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{},
			},
		},
	}

	// Should not panic or block
	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()
}
