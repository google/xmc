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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	dashboard_apipb "xmanager_cloud/api/protos/dashboard"
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// sequencedXidClient returns a sequence of CreateXidEntry responses/errors and records the context
// and outgoing metadata of each call.
type sequencedXidClient struct {
	xidmsgpb.XidServiceClient
	mu         sync.Mutex
	responses  []*xidmsgpb.XidEntry
	errs       []error
	reqs       []*xidmsgpb.CreateXidEntryRequest
	outgoingMD []metadata.MD
	ctxErrs    []error
	deadlines  []time.Time
}

func (c *sequencedXidClient) CreateXidEntry(ctx context.Context, in *xidmsgpb.CreateXidEntryRequest, _ ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := len(c.reqs)
	c.reqs = append(c.reqs, in)
	md, _ := metadata.FromOutgoingContext(ctx)
	c.outgoingMD = append(c.outgoingMD, md)
	c.ctxErrs = append(c.ctxErrs, ctx.Err())
	dl, _ := ctx.Deadline()
	c.deadlines = append(c.deadlines, dl)
	if idx < len(c.errs) && c.errs[idx] != nil {
		return nil, c.errs[idx]
	}
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	return &xidmsgpb.XidEntry{Name: proto.String(fmt.Sprintf("xids/%d", 100+idx))}, nil
}

func seedSpannerHPOCatalog(ctx context.Context, t *testing.T, st storage.Storage) {
	t.Helper()
	catalog := experimenttest.HPOCatalog(nil)
	depIDs := map[string]int64{"vizier": catalog["vizier"].ComponentID, "clickhouse": catalog["clickhouse"].ComponentID}
	if _, err := st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		for _, name := range []string{"clickhouse", "vizier", "hpo"} {
			if err := tx.Components.InsertComponent(ctx, catalog[name], depIDs); err != nil {
				return err
			}
			if err := tx.Components.UpdateComponentObservation(ctx, catalog[name], false); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seedSpannerHPOCatalog() failed: %v", err)
	}
}

func studyCreateExperimentReq() *msgpb.CreateExperimentRequest {
	return &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{
		Title: "hpo study", ExperimentType: msgpb.Experiment_STUDY,
		TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()},
	}}
}

func TestCreateExperiment_ProvisionsStudyChartEndToEnd(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer user-token-123", "x-goog-iap-jwt-assertion", "iap-jwt-123", "x-goog-user-email", "test@example.com",
	))
	mockDash := &mockDashboardClient{}
	var bgTaskWG sync.WaitGroup
	srv, _ := createExperimentStateServer(ctx, t, func(p *Params) {
		seedSpannerHPOCatalog(ctx, t, p.Storage)
		p.ArtifactObservers = []ArtifactObserver{NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), p.Storage, &bgTaskWG)}
	})

	t.Run("standard_and_unspecified_do_not_provision", func(t *testing.T) {
		for _, expType := range []msgpb.Experiment_ExperimentType{msgpb.Experiment_STANDARD, msgpb.Experiment_EXPERIMENT_TYPE_UNSPECIFIED} {
			exp, err := srv.CreateExperiment(ctx, &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{Title: "standard", ExperimentType: expType}})
			if err != nil {
				t.Fatalf("CreateExperiment(%v) failed: %v", expType, err)
			}
			bgTaskWG.Wait()
			if len(mockDash.reqs) != 0 {
				t.Errorf("CreateExperiment(%v) sent %d CreateChart calls, want 0", expType, len(mockDash.reqs))
			}
			listed, err := srv.ListArtifacts(ctx, &msgpb.ListArtifactsRequest{Parent: exp.GetName()})
			if err != nil || len(listed.GetArtifacts()) != 0 {
				t.Errorf("ListArtifacts(%s) = (%v, %v), want empty", exp.GetName(), listed.GetArtifacts(), err)
			}
		}
	})

	t.Run("study_provisions_chart_and_writes_back_success", func(t *testing.T) {
		beforeArt := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))
		beforeChart := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))
		var createdDashboardChart *dashboard_apipb.Chart
		mockDash.createChartFn = func(_ context.Context, in *dashboard_apipb.CreateChartRequest) (*dashboard_apipb.Chart, error) {
			ch := proto.Clone(in.GetChart()).(*dashboard_apipb.Chart)
			ch.Name = validator.ConstructExperimentChartName(in.GetChart().GetXid(), in.GetChartId())
			createdDashboardChart = proto.Clone(ch).(*dashboard_apipb.Chart)
			return ch, nil
		}

		studyExp, err := srv.CreateExperiment(ctx, studyCreateExperimentReq())
		if err != nil {
			t.Fatalf("CreateExperiment(STUDY) failed: %v", err)
		}
		bgTaskWG.Wait()

		studyXID, err := validator.ValidateExperimentName(studyExp.GetName())
		if err != nil || len(mockDash.reqs) != 1 {
			t.Fatalf("ValidateExperimentName(%q) err = %v, len(mockDash.reqs) = %d; want nil, 1", studyExp.GetName(), err, len(mockDash.reqs))
		}
		if diff := cmp.Diff(studyChartRequest(studyXID, studyChartTitle), mockDash.reqs[0], protocmp.Transform()); diff != "" {
			t.Errorf("Dashboard CreateChart request mismatch (-want +got):\n%s", diff)
		}
		if err := validator.ValidateCreateChartRequest(mockDash.reqs[0]); err != nil {
			t.Errorf("ValidateCreateChartRequest() rejected study chart request: %v", err)
		}
		if gotAuth := mockDash.outgoingMD[0].Get("authorization"); len(gotAuth) != 1 || gotAuth[0] != "Bearer user-token-123" {
			t.Errorf("Dashboard CreateChart outgoing authorization = %v, want ['Bearer user-token-123']", gotAuth)
		}

		listed, err := srv.ListArtifacts(ctx, &msgpb.ListArtifactsRequest{Parent: studyExp.GetName()})
		if err != nil || len(listed.GetArtifacts()) != 1 {
			t.Fatalf("ListArtifacts(%s) = (%v, %v), want 1 artifact", studyExp.GetName(), listed.GetArtifacts(), err)
		}
		gotArt := listed.GetArtifacts()[0]
		if gotArt.GetName() == "" {
			t.Error("persisted study ChartArtifact has empty Name")
		}
		wantArt := &msgpb.Artifact{
			Parent: studyExp.GetName(), Url: &msgpb.Url{DisplayName: studyChartTitle},
			Tags: []string{"study", "parallel-coordinates"}, LifecyclePhase: msgpb.Artifact_OUTPUT,
			Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_Chart{Chart: &msgpb.ChartArtifact{
				Title: studyChartTitle, MetricNames: []string{"accuracy"}, Status: msgpb.ChartArtifact_SUCCESS, Chart: createdDashboardChart,
			}}},
		}
		if diff := cmp.Diff(wantArt, gotArt, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.Artifact{}, "name", "acls", "create_time", "update_time")); diff != "" {
			t.Errorf("persisted study ChartArtifact mismatch (-want +got):\n%s", diff)
		}
		if testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))-beforeArt != 0 ||
			testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))-beforeChart != 0 {
			t.Error("expected study chart failure counters to remain unchanged on successful provisioning")
		}
	})
}

func TestCreateExperiment_SkipsStudyChartWhenNoObserverConfigured(t *testing.T) {
	xidClient := &sequencedXidClient{responses: []*xidmsgpb.XidEntry{{Name: proto.String("xids/123")}}}
	st := &recordingStorage{MockStorage: &storage.MockStorage{ComponentRepository: &storage.MockComponentRepository{Records: readyHPOCatalog()}}}
	srv := newTestServer(t, withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}), withXidClient(xidClient), withStorage(st))
	beforeArt := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))
	beforeChart := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))

	got, err := srv.CreateExperiment(context.Background(), studyCreateExperimentReq())
	if err != nil || got.GetName() != "experiments/123" {
		t.Fatalf("CreateExperiment(STUDY) = (%v, %v), want experiments/123", got, err)
	}
	if len(xidClient.reqs) != 1 || len(st.createdArtifacts) != 0 {
		t.Errorf("xid calls = %d, created artifacts = %d; want (1, 0)", len(xidClient.reqs), len(st.createdArtifacts))
	}
	if testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))-beforeArt != 0 ||
		testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))-beforeChart != 0 {
		t.Error("expected study chart failure counters to remain unchanged when no observer is configured")
	}
}

func TestCreateExperiment_StudyChartProvisioningFailuresStillSucceed(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name                 string
		artifactXIDErr       error
		createArtifactErr    error
		createChartErr       error
		wantCreatedArtifacts int
		wantDashCalls        int
		wantFailedWriteBack  bool
		wantArtifactDelta    float64
		wantChartDelta       float64
	}{
		{name: "artifact_xid_failure", artifactXIDErr: boom, wantArtifactDelta: 1},
		{name: "artifact_storage_failure", createArtifactErr: boom, wantArtifactDelta: 1},
		{name: "dashboard_create_chart_failure", createChartErr: boom, wantCreatedArtifacts: 1, wantDashCalls: 1, wantFailedWriteBack: true, wantChartDelta: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xidClient := &sequencedXidClient{
				responses: []*xidmsgpb.XidEntry{{Name: proto.String("xids/123")}, {Name: proto.String("xids/456")}},
				errs:      []error{nil, tc.artifactXIDErr},
			}
			st := &recordingStorage{MockStorage: &storage.MockStorage{
				ComponentRepository: &storage.MockComponentRepository{Records: readyHPOCatalog()},
				CreateArtifactErr:   tc.createArtifactErr,
			}}
			mockDash := &mockDashboardClient{createChartErr: tc.createChartErr}
			var bgTaskWG sync.WaitGroup
			obs := NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), st, &bgTaskWG)
			srv := newTestServer(t, withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}), withXidClient(xidClient), withStorage(st), withArtifactObservers(obs))
			beforeArt := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))
			beforeChart := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))

			got, err := srv.CreateExperiment(context.Background(), studyCreateExperimentReq())
			bgTaskWG.Wait()

			if err != nil || got.GetName() != "experiments/123" {
				t.Fatalf("CreateExperiment(STUDY) = (%v, %v), want experiments/123", got, err)
			}
			if len(st.createdArtifacts) != tc.wantCreatedArtifacts || len(mockDash.reqs) != tc.wantDashCalls {
				t.Errorf("createdArtifacts = %d, dashCalls = %d; want (%d, %d)", len(st.createdArtifacts), len(mockDash.reqs), tc.wantCreatedArtifacts, tc.wantDashCalls)
			}
			if tc.wantFailedWriteBack {
				if len(st.updateUpdates) != 1 {
					t.Fatalf("len(st.updateUpdates) = %d, want 1", len(st.updateUpdates))
				}
				gotChart := st.updateUpdates[0].Artifact.GetPayload().GetChart()
				if gotChart.GetStatus() != msgpb.ChartArtifact_FAILED || !strings.Contains(gotChart.GetErrorMessage(), "boom") {
					t.Errorf("persisted ChartArtifact = (%v, %q), want FAILED containing 'boom'", gotChart.GetStatus(), gotChart.GetErrorMessage())
				}
			} else if len(st.updateUpdates) != 0 {
				t.Errorf("len(st.updateUpdates) = %d, want 0", len(st.updateUpdates))
			}
			if delta := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact)) - beforeArt; delta != tc.wantArtifactDelta {
				t.Errorf("failure counter {stage=artifact} delta = %v, want %v", delta, tc.wantArtifactDelta)
			}
			if delta := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart)) - beforeChart; delta != tc.wantChartDelta {
				t.Errorf("failure counter {stage=chart} delta = %v, want %v", delta, tc.wantChartDelta)
			}
		})
	}
}

func TestStudyChartProvisioner_SurvivesRequestCancellationAndForwardsAuth(t *testing.T) {
	xidClient := &sequencedXidClient{responses: []*xidmsgpb.XidEntry{{Name: proto.String("xids/456")}}}
	st := &recordingStorage{MockStorage: &storage.MockStorage{}}
	const wantChartName = "experiments/123/charts/study-123-parallel-coordinates"
	unblockCreateChart := make(chan struct{})
	mockDash := &mockDashboardClient{
		createChartFn: func(_ context.Context, _ *dashboard_apipb.CreateChartRequest) (*dashboard_apipb.Chart, error) {
			<-unblockCreateChart
			return &dashboard_apipb.Chart{Name: wantChartName}, nil
		},
	}
	var bgTaskWG sync.WaitGroup
	obs := NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), st, &bgTaskWG)
	srv := newTestServer(t, withConfig(&config.Config{EndpointURL: "http://ess.local"}), withXidClient(xidClient), withStorage(st), withArtifactObservers(obs))

	incoming := metadata.Pairs("authorization", "Bearer token-xyz", "x-goog-iap-jwt-assertion", "iap-xyz", "x-goog-user-email", "creator@example.com")
	ctx, cancel := context.WithCancel(metadata.NewIncomingContext(context.Background(), incoming))
	cancel()

	start := time.Now()
	srv.studyCharts.provision(ctx, "creator@example.com", &msgpb.Experiment{
		Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY,
		TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()},
	})
	// Release CreateChart only after provision() returns so its deferred cancel() has already run.
	close(unblockCreateChart)
	bgTaskWG.Wait()

	for _, c := range []struct {
		name string
		errs []error
	}{{"CreateXidEntry", xidClient.ctxErrs}, {"CreateArtifact", st.createCtxErrs}, {"CreateChart", mockDash.ctxErrs}} {
		if len(c.errs) != 1 || c.errs[0] != nil {
			t.Fatalf("%s ctxErrs = %v, want [nil] on a live detached context", c.name, c.errs)
		}
	}
	if len(st.updateUpdates) != 1 {
		t.Fatalf("UpdateArtifact calls = %d, want 1", len(st.updateUpdates))
	}
	if gotPayload := st.updateUpdates[0].Artifact.GetPayload().GetChart(); gotPayload.GetStatus() != msgpb.ChartArtifact_SUCCESS || gotPayload.GetChart().GetName() != wantChartName {
		t.Errorf("persisted ChartArtifact = (%v, %q), want (SUCCESS, %q)", gotPayload.GetStatus(), gotPayload.GetChart().GetName(), wantChartName)
	}
	if xidClient.reqs[0].GetXidEntry().GetParentXid() != "xids/123" || xidClient.reqs[0].GetXidEntry().GetCreator() != "creator@example.com" {
		t.Errorf("CreateXidEntry req = %v, want parent_xid='xids/123' creator='creator@example.com'", xidClient.reqs[0])
	}
	if dl := xidClient.deadlines[0]; dl.IsZero() || dl.After(start.Add(studyChartProvisionTimeout+time.Second)) {
		t.Errorf("CreateXidEntry deadline = %v, want bounded within %v", dl, studyChartProvisionTimeout)
	}
	for target, md := range map[string]metadata.MD{"XidService.CreateXidEntry": xidClient.outgoingMD[0], "DashboardService.CreateChart": mockDash.outgoingMD[0]} {
		for key, want := range map[string]string{"authorization": "Bearer token-xyz", "x-goog-iap-jwt-assertion": "iap-xyz", "x-goog-user-email": "creator@example.com"} {
			if got := md.Get(key); len(got) != 1 || got[0] != want {
				t.Errorf("%s outgoing[%q] = %v, want [%q]", target, key, got, want)
			}
		}
	}
}

func TestStudyChartProvisioner_EdgeCases(t *testing.T) {
	var nilProvisioner *studyChartProvisioner
	nilProvisioner.provision(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY})

	var bgTaskWG sync.WaitGroup
	obs := NewDashboardObserver(toollibs.NewDashboardToolClient(&mockDashboardClient{}), &storage.MockStorage{}, &bgTaskWG)
	srv := newTestServer(t, withArtifactObservers(obs))
	before := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))

	srv.studyCharts.provision(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "invalid-name", ExperimentType: msgpb.Experiment_STUDY})

	if delta := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact)) - before; delta != 1 {
		t.Errorf("failure counter {stage=artifact} delta = %v, want 1", delta)
	}
}
