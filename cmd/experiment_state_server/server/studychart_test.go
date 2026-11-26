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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	storagetest "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/internal/validator"
)

func studyCreateExperimentReq() *msgpb.CreateExperimentRequest {
	return &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{Title: "hpo study", ExperimentType: msgpb.Experiment_STUDY, TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}}}
}

func studyChartFailureCount(stage string) float64 {
	return testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(stage))
}

func assertStudyChartFailureDelta(t *testing.T, stage string, before, wantDelta float64) {
	t.Helper()
	if delta := studyChartFailureCount(stage) - before; delta != wantDelta {
		t.Errorf("failure counter {stage=%s} delta = %v, want %v", stage, delta, wantDelta)
	}
}

func newStudyChartTestServer(t *testing.T, st *recordingStorage, xidClient *mockXidClient, mockDash *mockDashboardClient, withObserver bool, extraOpts ...testServerOpt) (*Server, *sync.WaitGroup) {
	t.Helper()
	var bgTaskWG sync.WaitGroup
	opts := append([]testServerOpt{withXidClient(xidClient), withStorage(st)}, extraOpts...)
	if withObserver {
		opts = append(opts, func(p *Params) {
			p.ArtifactObservers = []ArtifactObserver{NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), st, &bgTaskWG)}
		})
	}
	return newTestServer(t, opts...), &bgTaskWG
}

func TestCreateExperiment_ProvisionsStudyChartEndToEnd(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer user-token-123", "x-goog-iap-jwt-assertion", "iap-jwt-123", "x-goog-user-email", "test@example.com"))
	mockDash := &mockDashboardClient{}
	var bgTaskWG sync.WaitGroup
	srv, _ := createExperimentStateServer(ctx, t, func(p *Params) {
		if err := storagetest.SeedComponentCatalog(ctx, p.Storage, experimenttest.OrderedHPOCatalog(nil)...); err != nil {
			t.Fatalf("SeedComponentCatalog() failed: %v", err)
		}
		p.ArtifactObservers, p.BackgroundTasks = []ArtifactObserver{NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), p.Storage, &bgTaskWG)}, &bgTaskWG
	})
	t.Run("standard_and_unspecified_do_not_provision", func(t *testing.T) {
		for _, expType := range []msgpb.Experiment_ExperimentType{msgpb.Experiment_STANDARD, msgpb.Experiment_EXPERIMENT_TYPE_UNSPECIFIED} {
			exp, err := srv.CreateExperiment(ctx, &msgpb.CreateExperimentRequest{Experiment: &msgpb.Experiment{Title: "standard", ExperimentType: expType}})
			bgTaskWG.Wait()
			if listed, listErr := srv.ListArtifacts(ctx, &msgpb.ListArtifactsRequest{Parent: exp.GetName()}); err != nil || len(mockDash.reqs) != 0 || listErr != nil || len(listed.GetArtifacts()) != 0 {
				t.Errorf("CreateExperiment(%v) err=%v dashReqs=%d listed=(%v,%v)", expType, err, len(mockDash.reqs), listed.GetArtifacts(), listErr)
			}
		}
	})
	t.Run("study_provisions_chart_and_writes_back_success", func(t *testing.T) {
		beforeArt, beforeChart := studyChartFailureCount(studyChartStageArtifact), studyChartFailureCount(studyChartStageChart)
		var createdDashboardChart *dashboard_apipb.Chart
		mockDash.createChartFn = func(_ context.Context, in *dashboard_apipb.CreateChartRequest) (*dashboard_apipb.Chart, error) {
			ch := proto.Clone(in.GetChart()).(*dashboard_apipb.Chart)
			ch.Name = validator.ConstructExperimentChartName(in.GetChart().GetXid(), in.GetChartId())
			createdDashboardChart = proto.Clone(ch).(*dashboard_apipb.Chart)
			return ch, nil
		}
		studyExp, createErr := srv.CreateExperiment(ctx, studyCreateExperimentReq())
		bgTaskWG.Wait()
		studyXID, xidErr := validator.ValidateExperimentName(studyExp.GetName())
		if createErr != nil || xidErr != nil || len(mockDash.reqs) != 1 {
			t.Fatalf("CreateExperiment=%v ValidateExperimentName(%q)=%v dashReqs=%d", createErr, studyExp.GetName(), xidErr, len(mockDash.reqs))
		}
		if err := validator.ValidateCreateChartRequest(mockDash.reqs[0]); err != nil {
			t.Errorf("ValidateCreateChartRequest() rejected study chart request: %v", err)
		}
		if diff := cmp.Diff(studyChartRequest(studyXID, studyChartTitle), mockDash.reqs[0], protocmp.Transform()); diff != "" {
			t.Errorf("Dashboard CreateChart request mismatch (-want +got):\n%s", diff)
		}
		if gotAuth := mockDash.outgoingMD[0].Get("authorization"); len(gotAuth) != 1 || gotAuth[0] != "Bearer user-token-123" {
			t.Errorf("Dashboard CreateChart outgoing authorization = %v, want ['Bearer user-token-123']", gotAuth)
		}
		listed, err := srv.ListArtifacts(ctx, &msgpb.ListArtifactsRequest{Parent: studyExp.GetName()})
		if err != nil || len(listed.GetArtifacts()) != 1 {
			t.Fatalf("ListArtifacts(%s) = (%v, %v), want 1 artifact", studyExp.GetName(), listed.GetArtifacts(), err)
		}
		gotArt := listed.GetArtifacts()[0]
		if _, err := validator.ValidateArtifactName(gotArt.GetName()); err != nil {
			t.Errorf("persisted study ChartArtifact name %q is invalid: %v", gotArt.GetName(), err)
		}
		wantArt := &msgpb.Artifact{Parent: studyExp.GetName(), Url: &msgpb.Url{Url: "/v1/" + createdDashboardChart.GetName(), DisplayName: studyChartTitle}, Tags: []string{"study", "parallel-coordinates"}, LifecyclePhase: msgpb.Artifact_OUTPUT, Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_Chart{Chart: &msgpb.ChartArtifact{Title: studyChartTitle, MetricNames: []string{"accuracy"}, Status: msgpb.ChartArtifact_SUCCESS, Chart: createdDashboardChart}}}}
		if diff := cmp.Diff(wantArt, gotArt, protocmp.Transform(), protocmp.IgnoreFields(&msgpb.Artifact{}, "name", "acls", "create_time", "update_time")); diff != "" {
			t.Errorf("persisted study ChartArtifact mismatch (-want +got):\n%s", diff)
		}
		assertStudyChartFailureDelta(t, studyChartStageArtifact, beforeArt, 0)
		assertStudyChartFailureDelta(t, studyChartStageChart, beforeChart, 0)
	})
}

func TestCreateExperiment_StudyChartProvisioningFailuresAndSkip(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name                                              string
		noObserver, wantFailedWriteBack                   bool
		artifactXIDErr, createArtifactErr, createChartErr error
		wantXIDCalls, wantCreatedArtifacts, wantDashCalls int
		wantArtifactDelta, wantChartDelta                 float64
	}{
		{name: "no_observer_skips_without_counting", noObserver: true, wantXIDCalls: 1},
		{name: "artifact_xid_failure", artifactXIDErr: boom, wantXIDCalls: 2, wantArtifactDelta: 1},
		{name: "artifact_storage_failure", createArtifactErr: boom, wantXIDCalls: 2, wantArtifactDelta: 1},
		{name: "dashboard_create_chart_failure", createChartErr: boom, wantXIDCalls: 2, wantCreatedArtifacts: 1, wantDashCalls: 1, wantFailedWriteBack: true, wantChartDelta: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xidClient := &mockXidClient{createXidEntryResps: []*xidmsgpb.XidEntry{{Name: proto.String("xids/123")}, {Name: proto.String("xids/456")}}, createXidEntryErrs: []error{nil, tc.artifactXIDErr}}
			st := &recordingStorage{MockStorage: &storage.MockStorage{ComponentRepository: &storage.MockComponentRepository{Records: experimenttest.HPOCatalog(nil)}, CreateArtifactErr: tc.createArtifactErr}}
			mockDash := &mockDashboardClient{createChartErr: tc.createChartErr}
			srv, bgTaskWG := newStudyChartTestServer(t, st, xidClient, mockDash, !tc.noObserver, withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}))
			beforeArt, beforeChart := studyChartFailureCount(studyChartStageArtifact), studyChartFailureCount(studyChartStageChart)
			got, err := srv.CreateExperiment(context.Background(), studyCreateExperimentReq())
			bgTaskWG.Wait()
			if err != nil || got.GetName() != "experiments/123" || xidClient.createXidEntryCalls != tc.wantXIDCalls || len(st.createdArtifacts) != tc.wantCreatedArtifacts || len(mockDash.reqs) != tc.wantDashCalls {
				t.Fatalf("got=(%v,%v) xid=%d arts=%d dash=%d, want experiments/123 xid=%d arts=%d dash=%d", got, err, xidClient.createXidEntryCalls, len(st.createdArtifacts), len(mockDash.reqs), tc.wantXIDCalls, tc.wantCreatedArtifacts, tc.wantDashCalls)
			}
			if tc.wantFailedWriteBack && (len(st.updateUpdates) != 1 || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetStatus() != msgpb.ChartArtifact_FAILED || !strings.Contains(st.updateUpdates[0].Artifact.GetPayload().GetChart().GetErrorMessage(), "boom")) {
				t.Errorf("updateUpdates = %v, want 1 FAILED update containing 'boom'", st.updateUpdates)
			} else if !tc.wantFailedWriteBack && len(st.updateUpdates) != 0 {
				t.Errorf("len(st.updateUpdates) = %d, want 0", len(st.updateUpdates))
			}
			assertStudyChartFailureDelta(t, studyChartStageArtifact, beforeArt, tc.wantArtifactDelta)
			assertStudyChartFailureDelta(t, studyChartStageChart, beforeChart, tc.wantChartDelta)
		})
	}
}

func TestStudyChartProvisioner_SurvivesRequestCancellationAndForwardsAuth(t *testing.T) {
	xidClient, st := &mockXidClient{createXidEntryResps: []*xidmsgpb.XidEntry{{Name: proto.String("xids/456")}}}, &recordingStorage{MockStorage: &storage.MockStorage{}}
	const wantChartName = "experiments/123/charts/study-123-parallel-coordinates"
	unblockCreateChart := make(chan struct{})
	mockDash := &mockDashboardClient{createChartFn: func(_ context.Context, _ *dashboard_apipb.CreateChartRequest) (*dashboard_apipb.Chart, error) {
		<-unblockCreateChart
		return &dashboard_apipb.Chart{Name: wantChartName}, nil
	}}
	srv, bgTaskWG := newStudyChartTestServer(t, st, xidClient, mockDash, true, withConfig(&config.Config{EndpointURL: "http://ess.local"}))
	ctx, cancel := context.WithCancel(metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token-xyz", "x-goog-iap-jwt-assertion", "iap-xyz", "x-goog-user-email", "creator@example.com")))
	cancel()
	start := time.Now()
	srv.studyCharts.provision(ctx, "creator@example.com", &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY, TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}})
	close(unblockCreateChart) // Release CreateChart after provision() returns and its deferred cancel() runs.
	bgTaskWG.Wait()
	for name, errs := range map[string][]error{"CreateXidEntry": xidClient.createXidEntryCtxErrs, "CreateArtifact": st.createCtxErrs, "CreateChart": mockDash.ctxErrs} {
		if len(errs) != 1 || errs[0] != nil {
			t.Fatalf("%s ctxErrs = %v, want [nil] on a live detached context", name, errs)
		}
	}
	if len(st.updateUpdates) != 1 || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetStatus() != msgpb.ChartArtifact_SUCCESS || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetChart().GetName() != wantChartName {
		t.Fatalf("UpdateArtifact updates = %v, want 1 SUCCESS update for %q", st.updateUpdates, wantChartName)
	}
	if xidReq, dl := xidClient.gotCreateXidEntryReq.GetXidEntry(), xidClient.createXidEntryDeadlines[0]; xidReq.GetParentXid() != "xids/123" || xidReq.GetCreator() != "creator@example.com" || dl.IsZero() || dl.After(start.Add(studyChartProvisionTimeout+time.Second)) {
		t.Errorf("CreateXidEntry req=%v deadline=%v, want parent='xids/123' creator='creator@example.com' within %v", xidReq, dl, studyChartProvisionTimeout)
	}
	for _, want := range []struct {
		target string
		md     metadata.MD
	}{{"XidService.CreateXidEntry", xidClient.createXidEntryMD[0]}, {"DashboardService.CreateChart", mockDash.outgoingMD[0]}} {
		for _, kv := range [][2]string{{"authorization", "Bearer token-xyz"}, {"x-goog-iap-jwt-assertion", "iap-xyz"}, {"x-goog-user-email", "creator@example.com"}} {
			if got := want.md.Get(kv[0]); len(got) != 1 || got[0] != kv[1] {
				t.Errorf("%s outgoing[%q] = %v, want [%q]", want.target, kv[0], got, kv[1])
			}
		}
	}
}

func TestStudyChartProvisioner_EdgeCasesAndBackgroundStart(t *testing.T) {
	var nilProvisioner *studyChartProvisioner
	nilProvisioner.start(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY})
	nilProvisioner.provision(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY})
	xidClient := &mockXidClient{createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/999")}}
	st := &recordingStorage{MockStorage: &storage.MockStorage{}, blockCreate: make(chan struct{})}
	srv, bgTaskWG := newStudyChartTestServer(t, st, xidClient, &mockDashboardClient{}, true)
	before := studyChartFailureCount(studyChartStageArtifact)
	srv.studyCharts.provision(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "invalid-name", ExperimentType: msgpb.Experiment_STUDY})
	assertStudyChartFailureDelta(t, studyChartStageArtifact, before, 1)
	if xidClient.createXidEntryCalls != 0 || len(st.createdArtifacts) != 0 {
		t.Errorf("(xidCalls, createdArtifacts) = (%d, %d), want (0, 0)", xidClient.createXidEntryCalls, len(st.createdArtifacts))
	}
	p := newStudyChartProvisioner(srv.ArtifactHandler, bgTaskWG)
	study := &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY, TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}}
	p.start(context.Background(), "creator@example.com", study) // Returns while CreateArtifact blocks.
	close(st.blockCreate)
	bgTaskWG.Wait()
	if len(st.createdArtifacts) != 1 || st.createdArtifacts[0].GetParent() != "experiments/123" {
		t.Errorf("createdArtifacts = %v, want 1 under experiments/123", st.createdArtifacts)
	}
	p.start(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "experiments/124", ExperimentType: msgpb.Experiment_STANDARD})
	bgTaskWG.Wait()
	if len(st.createdArtifacts) != 1 {
		t.Errorf("STANDARD start created %d extra artifacts, want 0", len(st.createdArtifacts)-1)
	}
	st.blockCreate, st.panicCreateArtifact = nil, true
	beforePanic := studyChartFailureCount(studyChartStageArtifact)
	p.start(context.Background(), "creator@example.com", study)
	bgTaskWG.Wait()
	assertStudyChartFailureDelta(t, studyChartStageArtifact, beforePanic, 1)
}
