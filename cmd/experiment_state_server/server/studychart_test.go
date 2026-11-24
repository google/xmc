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

func TestCreateExperiment_ProvisionsStudyChartEndToEnd(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer user-token-123", "x-goog-iap-jwt-assertion", "iap-jwt-123", "x-goog-user-email", "test@example.com"))
	mockDash := &mockDashboardClient{}
	var bgTaskWG sync.WaitGroup
	srv, _ := createExperimentStateServer(ctx, t, func(p *Params) {
		if err := storagetest.SeedComponentCatalog(ctx, p.Storage, experimenttest.OrderedHPOCatalog(nil)...); err != nil {
			t.Fatalf("SeedComponentCatalog() failed: %v", err)
		}
		p.ArtifactObservers = []ArtifactObserver{NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), p.Storage, &bgTaskWG)}
		p.BackgroundTasks = &bgTaskWG
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
			if listed, err := srv.ListArtifacts(ctx, &msgpb.ListArtifactsRequest{Parent: exp.GetName()}); err != nil || len(listed.GetArtifacts()) != 0 {
				t.Errorf("ListArtifacts(%s) = (%v, %v), want empty", exp.GetName(), listed.GetArtifacts(), err)
			}
		}
	})
	t.Run("study_provisions_chart_and_writes_back_success", func(t *testing.T) {
		beforeArt, beforeChart := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact)), testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))
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
		if _, err := validator.ValidateArtifactName(gotArt.GetName()); err != nil {
			t.Errorf("persisted study ChartArtifact name %q is invalid: %v", gotArt.GetName(), err)
		}
		wantArt := &msgpb.Artifact{
			Parent: studyExp.GetName(), Url: &msgpb.Url{Url: "/v1/" + createdDashboardChart.GetName(), DisplayName: studyChartTitle}, Tags: []string{"study", "parallel-coordinates"}, LifecyclePhase: msgpb.Artifact_OUTPUT,
			Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_Chart{Chart: &msgpb.ChartArtifact{Title: studyChartTitle, MetricNames: []string{"accuracy"}, Status: msgpb.ChartArtifact_SUCCESS, Chart: createdDashboardChart}}},
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
			st := &recordingStorage{MockStorage: &storage.MockStorage{ComponentRepository: &storage.MockComponentRepository{Records: readyHPOCatalog()}, CreateArtifactErr: tc.createArtifactErr}}
			mockDash := &mockDashboardClient{createChartErr: tc.createChartErr}
			var bgTaskWG sync.WaitGroup
			opts := []testServerOpt{withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}), withXidClient(xidClient), withStorage(st)}
			if !tc.noObserver {
				opts = append(opts, withArtifactObservers(NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), st, &bgTaskWG)))
			}
			srv := newTestServer(t, opts...)
			beforeArt, beforeChart := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact)), testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageChart))
			got, err := srv.CreateExperiment(context.Background(), studyCreateExperimentReq())
			bgTaskWG.Wait()
			if err != nil || got.GetName() != "experiments/123" {
				t.Fatalf("CreateExperiment(STUDY) = (%v, %v), want experiments/123", got, err)
			}
			if xidClient.createXidEntryCalls != tc.wantXIDCalls || len(st.createdArtifacts) != tc.wantCreatedArtifacts || len(mockDash.reqs) != tc.wantDashCalls {
				t.Errorf("(xidCalls, createdArtifacts, dashCalls) = (%d, %d, %d), want (%d, %d, %d)", xidClient.createXidEntryCalls, len(st.createdArtifacts), len(mockDash.reqs), tc.wantXIDCalls, tc.wantCreatedArtifacts, tc.wantDashCalls)
			}
			if tc.wantFailedWriteBack {
				if len(st.updateUpdates) != 1 || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetStatus() != msgpb.ChartArtifact_FAILED || !strings.Contains(st.updateUpdates[0].Artifact.GetPayload().GetChart().GetErrorMessage(), "boom") {
					t.Errorf("updateUpdates = %v, want 1 FAILED update containing 'boom'", st.updateUpdates)
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
	xidClient, st := &mockXidClient{createXidEntryResps: []*xidmsgpb.XidEntry{{Name: proto.String("xids/456")}}}, &recordingStorage{MockStorage: &storage.MockStorage{}}
	const wantChartName = "experiments/123/charts/study-123-parallel-coordinates"
	unblockCreateChart := make(chan struct{})
	mockDash := &mockDashboardClient{createChartFn: func(_ context.Context, _ *dashboard_apipb.CreateChartRequest) (*dashboard_apipb.Chart, error) {
		<-unblockCreateChart
		return &dashboard_apipb.Chart{Name: wantChartName}, nil
	}}
	var bgTaskWG sync.WaitGroup
	srv := newTestServer(t, withConfig(&config.Config{EndpointURL: "http://ess.local"}), withXidClient(xidClient), withStorage(st), withArtifactObservers(NewDashboardObserver(toollibs.NewDashboardToolClient(mockDash), st, &bgTaskWG)))
	ctx, cancel := context.WithCancel(metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token-xyz", "x-goog-iap-jwt-assertion", "iap-xyz", "x-goog-user-email", "creator@example.com")))
	cancel()
	start := time.Now()
	srv.studyCharts.provision(ctx, "creator@example.com", &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY, TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}})
	close(unblockCreateChart) // Release CreateChart after provision() returns and its deferred cancel() runs.
	bgTaskWG.Wait()
	for _, c := range []struct {
		name string
		errs []error
	}{{"CreateXidEntry", xidClient.createXidEntryCtxErrs}, {"CreateArtifact", st.createCtxErrs}, {"CreateChart", mockDash.ctxErrs}} {
		if len(c.errs) != 1 || c.errs[0] != nil {
			t.Fatalf("%s ctxErrs = %v, want [nil] on a live detached context", c.name, c.errs)
		}
	}
	if len(st.updateUpdates) != 1 || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetStatus() != msgpb.ChartArtifact_SUCCESS || st.updateUpdates[0].Artifact.GetPayload().GetChart().GetChart().GetName() != wantChartName {
		t.Fatalf("UpdateArtifact updates = %v, want 1 SUCCESS update for %q", st.updateUpdates, wantChartName)
	}
	if xidClient.gotCreateXidEntryReq.GetXidEntry().GetParentXid() != "xids/123" || xidClient.gotCreateXidEntryReq.GetXidEntry().GetCreator() != "creator@example.com" {
		t.Errorf("CreateXidEntry req = %v, want parent_xid='xids/123' creator='creator@example.com'", xidClient.gotCreateXidEntryReq)
	}
	if dl := xidClient.createXidEntryDeadlines[0]; dl.IsZero() || dl.After(start.Add(studyChartProvisionTimeout+time.Second)) {
		t.Errorf("CreateXidEntry deadline = %v, want bounded within %v", dl, studyChartProvisionTimeout)
	}
	for target, md := range map[string]metadata.MD{"XidService.CreateXidEntry": xidClient.createXidEntryMD[0], "DashboardService.CreateChart": mockDash.outgoingMD[0]} {
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
	xidClient, st := &mockXidClient{createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/999")}}, &recordingStorage{MockStorage: &storage.MockStorage{}}
	srv := newTestServer(t, withXidClient(xidClient), withStorage(st), withArtifactObservers(NewDashboardObserver(toollibs.NewDashboardToolClient(&mockDashboardClient{}), st, &bgTaskWG)))
	before := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact))
	srv.studyCharts.provision(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "invalid-name", ExperimentType: msgpb.Experiment_STUDY})
	if delta := testutil.ToFloat64(studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact)) - before; delta != 1 || xidClient.createXidEntryCalls != 0 || len(st.createdArtifacts) != 0 {
		t.Errorf("(delta, xidCalls, createdArtifacts) = (%v, %d, %d), want (1, 0, 0)", delta, xidClient.createXidEntryCalls, len(st.createdArtifacts))
	}
}

func TestStudyChartProvisioner_StartRunsInBackgroundWhenTracked(t *testing.T) {
	var bgTaskWG sync.WaitGroup
	xidClient := &mockXidClient{createXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/456")}}
	st := &recordingStorage{MockStorage: &storage.MockStorage{}, blockCreate: make(chan struct{})}
	obs := NewDashboardObserver(toollibs.NewDashboardToolClient(&mockDashboardClient{}), st, &bgTaskWG)
	srv := newTestServer(t, withXidClient(xidClient), withStorage(st), withArtifactObservers(obs))
	p := newStudyChartProvisioner(srv.ArtifactHandler, &bgTaskWG)
	study := &msgpb.Experiment{Name: "experiments/123", ExperimentType: msgpb.Experiment_STUDY, TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}}
	p.start(context.Background(), "creator@example.com", study) // Returns while CreateArtifact is still blocked.
	close(st.blockCreate)
	bgTaskWG.Wait()
	if len(st.createdArtifacts) != 1 || st.createdArtifacts[0].GetParent() != "experiments/123" {
		t.Errorf("createdArtifacts = %v, want one artifact under experiments/123", st.createdArtifacts)
	}
	p.start(context.Background(), "creator@example.com", &msgpb.Experiment{Name: "experiments/124", ExperimentType: msgpb.Experiment_STANDARD})
	bgTaskWG.Wait()
	if len(st.createdArtifacts) != 1 {
		t.Errorf("STANDARD start created %d artifacts, want 0 more", len(st.createdArtifacts)-1)
	}
}
