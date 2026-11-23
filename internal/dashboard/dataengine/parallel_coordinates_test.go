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

package dataengine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

type ctxMarkerKey struct{}

// fakeStudyReader serves one experiment and pages through its work units like the experiment store:
// page tokens are the offset of the next page.
type fakeStudyReader struct {
	experiment   *essmsgpb.Experiment
	fetchErr     error
	workUnits    []*essmsgpb.WorkUnit
	listErr      error
	fetchCtx     context.Context
	listCtx      context.Context
	listedParent string
	listedOrder  string
	listCalls    int
}

func (f *fakeStudyReader) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	f.fetchCtx = ctx
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if f.experiment == nil || f.experiment.GetName() != fmt.Sprintf("experiments/%d", xid) {
		return nil, fmt.Errorf("experiment %d: %w", xid, grpcerr.ErrNotFound)
	}
	return f.experiment, nil
}

func (f *fakeStudyReader) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, _, orderBy string, _ time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	f.listCtx = ctx
	f.listCalls++
	f.listedParent = parent
	f.listedOrder = orderBy
	if f.listErr != nil {
		return nil, "", f.listErr
	}
	start := 0
	if pageToken != "" {
		start, _ = strconv.Atoi(pageToken)
	}
	end := min(start+int(pageSize), len(f.workUnits))
	next := ""
	if end < len(f.workUnits) {
		next = strconv.Itoa(end)
	}
	return f.workUnits[start:end], next, nil
}

var (
	testCreateTime   = timestamppb.New(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	testCompleteTime = timestamppb.New(time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC))
)

// repeatingTokenReader is a faulty store that always hands back the same page token.
type repeatingTokenReader struct {
	fakeStudyReader
}

func (r *repeatingTokenReader) ListWorkUnits(context.Context, string, int32, string, string, string, time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	r.listCalls++
	return nil, "stuck", nil
}

func baseStudyExperiment() *essmsgpb.Experiment {
	return &essmsgpb.Experiment{
		Name:           "experiments/1001",
		ExperimentType: essmsgpb.Experiment_STUDY,
		TypeConfig: &essmsgpb.Experiment_StudyConfig{StudyConfig: &essmsgpb.StudyConfig{
			Parameters: []*essmsgpb.ParameterSpec{{
				ParameterId:        "learning_rate",
				ParameterValueSpec: &essmsgpb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &essmsgpb.DoubleValueSpec{MinValue: 1e-5, MaxValue: 1e-1}},
				ScaleType:          essmsgpb.ParameterSpec_LOG,
			}},
			Metrics: []*essmsgpb.MetricSpec{{MetricId: "val/loss", Goal: essmsgpb.MetricSpec_MINIMIZE}},
		}},
	}
}

func testWorkUnit(wid int64, state essmsgpb.WorkUnitStatus_WorkUnitState, complete *timestamppb.Timestamp) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Name:       fmt.Sprintf("experiments/1001/workUnits/%d", wid),
		CreateTime: testCreateTime,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Status:       &essmsgpb.WorkUnitStatus{State: state},
			CompleteTime: complete,
		},
	}
}

func testTrajectory(wid int64, state dpb.ParallelCoordinatesData_RunTrajectory_WorkUnitState, complete *timestamppb.Timestamp) *dpb.ParallelCoordinatesData_RunTrajectory {
	return &dpb.ParallelCoordinatesData_RunTrajectory{Wid: wid, State: state, CreateTime: testCreateTime, CompleteTime: complete}
}

var parallelCoordinatesPlot = &dpb.PlotSpec{PlotId: "study", Type: dpb.PlotType_PARALLEL_COORDINATES}

func TestFetchParallelCoordinatesData(t *testing.T) {
	reader := &fakeStudyReader{
		experiment: baseStudyExperiment(),
		workUnits: []*essmsgpb.WorkUnit{
			testWorkUnit(1, essmsgpb.WorkUnitStatus_COMPLETED, testCompleteTime),
			testWorkUnit(2, essmsgpb.WorkUnitStatus_RUNNING, nil),
		},
	}
	engine := &DataEngine{studies: reader}
	ctx := context.WithValue(context.Background(), ctxMarkerKey{}, "req-1")

	got, err := engine.fetchParallelCoordinatesData(ctx, 1001, parallelCoordinatesPlot)
	if err != nil {
		t.Fatalf("fetchParallelCoordinatesData() error = %v", err)
	}

	want := []*dpb.ChartData_PlotData{{
		PlotId: "study",
		Type:   dpb.PlotType_PARALLEL_COORDINATES,
		Content: &dpb.ChartData_PlotData_ParallelCoordinatesData{ParallelCoordinatesData: &dpb.ParallelCoordinatesData{
			Axes: []*dpb.ParallelCoordinatesData_Axis{{
				ParameterId: "learning_rate",
				Values:      &dpb.ParallelCoordinatesData_Axis_DoubleRange_{DoubleRange: &dpb.ParallelCoordinatesData_Axis_DoubleRange{MinValue: 1e-5, MaxValue: 1e-1}},
				ScaleType:   dpb.ParallelCoordinatesData_Axis_LOG,
			}},
			Objective: &dpb.ParallelCoordinatesData_Objective{MetricId: "val/loss", Goal: dpb.ParallelCoordinatesData_Objective_MINIMIZE},
			Trajectories: []*dpb.ParallelCoordinatesData_RunTrajectory{
				testTrajectory(1, dpb.ParallelCoordinatesData_RunTrajectory_COMPLETED, testCompleteTime),
				testTrajectory(2, dpb.ParallelCoordinatesData_RunTrajectory_RUNNING, nil),
			},
		}},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("fetchParallelCoordinatesData() mismatch (-want +got):\n%s", diff)
	}
	if reader.listedParent != "experiments/1001" {
		t.Errorf("ListWorkUnits parent = %q, want %q", reader.listedParent, "experiments/1001")
	}
	if reader.listedOrder != "work_unit_id" {
		t.Errorf("ListWorkUnits orderBy = %q, want %q", reader.listedOrder, "work_unit_id")
	}
	if reader.fetchCtx.Value(ctxMarkerKey{}) != "req-1" || reader.listCtx.Value(ctxMarkerKey{}) != "req-1" {
		t.Errorf("ctx propagation = (%v, %v), want req-1 on both FetchExperiment and ListWorkUnits", reader.fetchCtx.Value(ctxMarkerKey{}), reader.listCtx.Value(ctxMarkerKey{}))
	}
}

func TestFetchParallelCoordinatesDataNoWorkUnits(t *testing.T) {
	engine := &DataEngine{studies: &fakeStudyReader{experiment: baseStudyExperiment()}}

	got, err := engine.fetchParallelCoordinatesData(context.Background(), 1001, parallelCoordinatesPlot)
	if err != nil {
		t.Fatalf("fetchParallelCoordinatesData() error = %v", err)
	}
	trajectories := got[0].GetParallelCoordinatesData().GetTrajectories()
	if trajectories == nil || len(trajectories) != 0 {
		t.Errorf("trajectories = %v, want an empty non-nil slice", trajectories)
	}
	if len(got[0].GetParallelCoordinatesData().GetAxes()) != 1 {
		t.Errorf("axes = %v, want the study's single axis", got[0].GetParallelCoordinatesData().GetAxes())
	}
}

func TestListRunTrajectoriesFollowsEveryPage(t *testing.T) {
	for _, count := range []int{workUnitPageSize - 1, workUnitPageSize, workUnitPageSize + 1, 2*workUnitPageSize + 3} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			reader := &fakeStudyReader{}
			for wid := int64(1); wid <= int64(count); wid++ {
				reader.workUnits = append(reader.workUnits, testWorkUnit(wid, essmsgpb.WorkUnitStatus_SCHEDULING, nil))
			}

			got, err := listRunTrajectories(context.Background(), reader, 1001)
			if err != nil {
				t.Fatalf("listRunTrajectories() error = %v", err)
			}
			if len(got) != count {
				t.Fatalf("listRunTrajectories() returned %d trajectories, want %d", len(got), count)
			}
			for i, trajectory := range got {
				if trajectory.GetWid() != int64(i+1) {
					t.Fatalf("trajectory %d has wid %d, want %d (ascending, no gaps or repeats)", i, trajectory.GetWid(), i+1)
				}
			}
			if wantCalls := (count + workUnitPageSize - 1) / workUnitPageSize; reader.listCalls != wantCalls {
				t.Errorf("ListWorkUnits called %d times, want %d", reader.listCalls, wantCalls)
			}
		})
	}
}

func TestFetchParallelCoordinatesDataErrors(t *testing.T) {
	standard := prototest.Modify(baseStudyExperiment(), func(e *essmsgpb.Experiment) {
		e.ExperimentType = essmsgpb.Experiment_STANDARD
		e.TypeConfig = nil
	})
	studyWithoutConfig := prototest.Modify(baseStudyExperiment(), func(e *essmsgpb.Experiment) {
		e.TypeConfig = nil
	})
	tests := []struct {
		name     string
		engine   *DataEngine
		wantCode codes.Code
	}{
		{name: "no study reader", engine: &DataEngine{}, wantCode: codes.FailedPrecondition},
		{name: "standard experiment", engine: &DataEngine{studies: &fakeStudyReader{experiment: standard}}, wantCode: codes.FailedPrecondition},
		{name: "study without study config", engine: &DataEngine{studies: &fakeStudyReader{experiment: studyWithoutConfig}}, wantCode: codes.FailedPrecondition},
		{name: "experiment not found", engine: &DataEngine{studies: &fakeStudyReader{}}, wantCode: codes.NotFound},
		{name: "experiment read fails", engine: &DataEngine{studies: &fakeStudyReader{fetchErr: errors.New("spanner unavailable")}}, wantCode: codes.Unknown},
		{name: "work unit list fails", engine: &DataEngine{studies: &fakeStudyReader{experiment: baseStudyExperiment(), listErr: grpcerr.ErrUnavailable}}, wantCode: codes.Unavailable},
		{name: "invalid work unit name", engine: &DataEngine{studies: &fakeStudyReader{
			experiment: baseStudyExperiment(),
			workUnits:  []*essmsgpb.WorkUnit{{Name: "experiments/1001/workUnits/0"}},
		}}, wantCode: codes.Internal},
		{name: "repeated page token", engine: &DataEngine{studies: &repeatingTokenReader{
			fakeStudyReader: fakeStudyReader{experiment: baseStudyExperiment()},
		}}, wantCode: codes.Internal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.engine.fetchParallelCoordinatesData(context.Background(), 1001, parallelCoordinatesPlot)
			if err == nil {
				t.Fatal("fetchParallelCoordinatesData() error = nil, want an error")
			}
			if got := status.Code(err); got != tc.wantCode {
				t.Errorf("fetchParallelCoordinatesData() code = %v, want %v (err: %v)", got, tc.wantCode, err)
			}
		})
	}
}

func TestRunTrajectoryWithoutExecution(t *testing.T) {
	got, err := runTrajectory(&essmsgpb.WorkUnit{Name: "experiments/1001/workUnits/3", CreateTime: testCreateTime})
	if err != nil {
		t.Fatalf("runTrajectory() error = %v", err)
	}
	want := testTrajectory(3, dpb.ParallelCoordinatesData_RunTrajectory_WORK_UNIT_STATE_UNSPECIFIED, nil)
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("runTrajectory() mismatch (-want +got):\n%s", diff)
	}
}

func TestProviderFetchChartDataServesParallelCoordinates(t *testing.T) {
	reader := &fakeStudyReader{
		experiment: baseStudyExperiment(),
		workUnits:  []*essmsgpb.WorkUnit{testWorkUnit(1, essmsgpb.WorkUnitStatus_RUNNING, nil)},
	}
	fp := "fp-v1"
	connector := &countingConnector{results: []func() (ClickHouseConn, error){succeeds}}
	provider := NewDynamicProvider(func(context.Context) (string, Connector, error) {
		return fp, connector.connect, nil
	}, WithStudyReader(reader), WithRetryInterval(0), WithCloseGracePeriod(0))
	t.Cleanup(func() { _ = provider.Close() })
	chart := &dpb.Chart{Name: "experiments/1001/charts/study", Xid: 1001, Plots: []*dpb.PlotSpec{parallelCoordinatesPlot}}
	want := &dpb.ChartData{
		Name: "experiments/1001/charts/study", Xid: 1001, Wid: 0,
		PlotsData: []*dpb.ChartData_PlotData{{
			PlotId: "study", Type: dpb.PlotType_PARALLEL_COORDINATES,
			Content: &dpb.ChartData_PlotData_ParallelCoordinatesData{ParallelCoordinatesData: &dpb.ParallelCoordinatesData{
				Axes: []*dpb.ParallelCoordinatesData_Axis{{
					ParameterId: "learning_rate",
					Values:      &dpb.ParallelCoordinatesData_Axis_DoubleRange_{DoubleRange: &dpb.ParallelCoordinatesData_Axis_DoubleRange{MinValue: 1e-5, MaxValue: 1e-1}},
					ScaleType:   dpb.ParallelCoordinatesData_Axis_LOG,
				}},
				Objective:    &dpb.ParallelCoordinatesData_Objective{MetricId: "val/loss", Goal: dpb.ParallelCoordinatesData_Objective_MINIMIZE},
				Trajectories: []*dpb.ParallelCoordinatesData_RunTrajectory{testTrajectory(1, dpb.ParallelCoordinatesData_RunTrajectory_RUNNING, nil)},
			}},
		}},
	}

	firstEngine, err := provider.Engine(context.Background())
	if err != nil {
		t.Fatalf("Engine() initial error = %v", err)
	}
	data, err := provider.FetchChartData(context.Background(), chart, nil)
	if err != nil {
		t.Fatalf("FetchChartData() error = %v", err)
	}
	if diff := cmp.Diff(want, data, protocmp.Transform()); diff != "" {
		t.Errorf("FetchChartData() mismatch (-want +got):\n%s", diff)
	}

	// Trigger a dynamic fingerprint rebuild and verify both Engine() and FetchChartData() keep StudyReader.
	fp = "fp-v2"
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() rebuild error = %v", err)
	}
	rebuiltEngine, err := provider.Engine(context.Background())
	if err != nil {
		t.Fatalf("Engine() rebuilt error = %v", err)
	}
	if rebuiltEngine == firstEngine {
		t.Fatal("Engine() after fingerprint change returned initial pointer, want rebuilt engine")
	}
	rebuiltData, err := rebuiltEngine.FetchChartData(context.Background(), chart, nil)
	if err != nil {
		t.Fatalf("rebuilt Engine().FetchChartData() error = %v", err)
	}
	if diff := cmp.Diff(want, rebuiltData, protocmp.Transform()); diff != "" {
		t.Errorf("rebuilt Engine().FetchChartData() mismatch (-want +got):\n%s", diff)
	}
}

func TestProviderFetchChartDataWrapsParallelCoordinatesErrors(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){succeeds}}).connect, WithStudyReader(&fakeStudyReader{}))
	chart := &dpb.Chart{Name: "experiments/1001/charts/study", Xid: 1001, Plots: []*dpb.PlotSpec{parallelCoordinatesPlot}}

	_, err := provider.FetchChartData(context.Background(), chart, nil)
	if status.Code(err) != codes.NotFound {
		t.Errorf("FetchChartData() code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
	}
	if !strings.Contains(err.Error(), `failed to fetch parallel coordinates for plot "study"`) {
		t.Errorf("FetchChartData() err = %q, want outer plot wrapper context", err)
	}
}

func TestWithStudyReaderOnNilEngine(t *testing.T) {
	var engine *DataEngine
	if got := engine.withStudyReader(&fakeStudyReader{}); got != nil {
		t.Errorf("withStudyReader() on a nil engine = %v, want nil", got)
	}
	nonNil := &DataEngine{}
	if got := nonNil.withStudyReader(nil); got != nonNil {
		t.Errorf("withStudyReader(nil) = %p, want original pointer %p", got, nonNil)
	}
}
