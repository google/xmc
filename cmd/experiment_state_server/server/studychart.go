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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	dashboard_apipb "xmanager_cloud/api/protos/dashboard"
	msgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

const (
	// studyChartProvisionTimeout bounds the creation of a study's chart artifact, so that a hung
	// XID service or database delays CreateExperiment by at most this long.
	studyChartProvisionTimeout = 10 * time.Second

	// studyChartTitle is the title of a study's chart artifact and of its chart.
	studyChartTitle = "Parallel Coordinates"

	// studyChartPlotID is the ID of the single plot of a study's chart.
	studyChartPlotID = "parallel_coordinates"

	// Stages of study chart provisioning, as studyChartProvisioningFailuresTotal labels.
	studyChartStageArtifact = "artifact"
	studyChartStageChart    = "chart"
)

var studyChartProvisioningFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "xmc_ess_study_chart_provisioning_failures_total",
	Help: "Number of created STUDY experiments whose parallel coordinates chart could not be provisioned, by stage: artifact (chart artifact creation) or chart (Dashboard CreateChart or status write-back).",
}, []string{"stage"})

func init() {
	for _, stage := range []string{studyChartStageArtifact, studyChartStageChart} {
		studyChartProvisioningFailuresTotal.WithLabelValues(stage)
	}
}

// studyChartProvisioner gives every created STUDY a chart artifact. The artifact observers then
// create the study's parallel coordinates chart on the Dashboard Service and record the outcome on
// the artifact.
type studyChartProvisioner struct {
	artifacts *ArtifactHandler
}

func newStudyChartProvisioner(artifacts *ArtifactHandler) *studyChartProvisioner {
	return &studyChartProvisioner{artifacts: artifacts}
}

// provision creates the chart artifact of exp, on behalf of creator, when exp is a STUDY. It runs
// after exp is committed, so a failure is logged and counted but not returned: failing the RPC
// would make the client retry and create a duplicate study. The creation is not cancelled with the
// request, since the study exists either way, and is bounded by studyChartProvisionTimeout.
func (p *studyChartProvisioner) provision(ctx context.Context, creator string, exp *msgpb.Experiment) {
	if exp.GetExperimentType() != msgpb.Experiment_STUDY {
		return
	}
	if p == nil || p.artifacts == nil || len(p.artifacts.artifactObservers) == 0 {
		// Without an observer nothing creates the chart, so the artifact would stay PROVISIONING.
		logging.Info(ctx, "No Dashboard Service configured; skipping study chart provisioning", "experiment_name", exp.GetName())
		return
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact).Inc()
		logging.Error(ctx, "Failed to parse the name of a created study", "experiment_name", exp.GetName(), "error", err)
		return
	}
	provisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), studyChartProvisionTimeout)
	defer cancel()
	idCtx := createArtifactContext{creator: creator, parentXID: xid}
	if _, err := p.artifacts.persistArtifact(provisionCtx, idCtx, exp.GetName(), studyChartArtifact(exp)); err != nil {
		studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact).Inc()
		logging.Error(ctx, "Failed to create the study chart artifact; the study was created without a chart",
			"experiment_name", exp.GetName(), "error", err)
	}
}

// studyChartArtifact builds the chart artifact of the STUDY exp. It names the study's objective
// metrics so that the artifact describes its chart before the chart exists. Being built by the
// server, it skips the user-request validation that keeps ChartArtifacts on work units.
func studyChartArtifact(exp *msgpb.Experiment) *msgpb.Artifact {
	var metricNames []string
	for _, metric := range exp.GetStudyConfig().GetMetrics() {
		metricNames = append(metricNames, metric.GetMetricId())
	}
	return validator.FillArtifactNullValues(&msgpb.Artifact{
		Url:            &msgpb.Url{DisplayName: studyChartTitle},
		Tags:           []string{"study", "parallel-coordinates"},
		LifecyclePhase: msgpb.Artifact_OUTPUT,
		Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_Chart{Chart: &msgpb.ChartArtifact{
			Title:       studyChartTitle,
			MetricNames: metricNames,
			Status:      msgpb.ChartArtifact_PROVISIONING,
		}}},
	})
}

// studyChartRequest builds the CreateChart request of the parallel coordinates chart of the STUDY
// xid: an experiment-level chart with the deterministic study chart ID and a single plot spanning
// every trial of the study.
func studyChartRequest(xid int64, title string) *dashboard_apipb.CreateChartRequest {
	if title == "" {
		title = studyChartTitle
	}
	return &dashboard_apipb.CreateChartRequest{
		Parent:  validator.ConstructExperimentName(xid),
		ChartId: validator.StudyChartID(xid),
		Chart: &dashboard_apipb.Chart{
			Title: title,
			Xid:   xid,
			Plots: []*dashboard_apipb.PlotSpec{{
				PlotId: studyChartPlotID,
				Title:  title,
				Type:   dashboard_apipb.PlotType_PARALLEL_COORDINATES,
			}},
			PlotLayout: &dashboard_apipb.LayoutSpec{Type: dashboard_apipb.LayoutSpec_GRID, Columns: 1},
		},
	}
}
