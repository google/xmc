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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	msgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

const (
	// studyChartProvisionTimeout bounds study chart artifact creation so a hung XID/DB call caps delay.
	studyChartProvisionTimeout = 10 * time.Second
	studyChartTitle            = "Parallel Coordinates"
	studyChartStageArtifact    = "artifact"
	studyChartStageChart       = "chart"
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

// studyChartProvisioner creates a ChartArtifact for every committed STUDY so artifact observers
// provision its parallel coordinates chart on Dashboard Service and persist the outcome. When wg is
// set, provisioning runs on a goroutine tracked by wg so CreateExperiment returns right after commit.
type studyChartProvisioner struct {
	artifacts *ArtifactHandler
	wg        *sync.WaitGroup
}

// newStudyChartProvisioner constructs a studyChartProvisioner backed by artifacts and tracked by wg.
func newStudyChartProvisioner(artifacts *ArtifactHandler, wg *sync.WaitGroup) *studyChartProvisioner {
	return &studyChartProvisioner{artifacts: artifacts, wg: wg}
}

// start provisions exp's chart artifact in the background when wg is set, and inline otherwise.
// A slow XID or DB call then cannot delay the CreateExperiment response past commit, which would
// make clients with short deadlines retry and create duplicate studies.
func (p *studyChartProvisioner) start(ctx context.Context, creator string, exp *msgpb.Experiment) {
	if p == nil || exp.GetExperimentType() != msgpb.Experiment_STUDY {
		return
	}
	if p.wg == nil {
		p.provision(ctx, creator, exp)
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact).Inc()
				logging.Error(ctx, "Panic during study chart provisioning", "experiment_name", exp.GetName(), "panic", r)
			}
		}()
		p.provision(ctx, creator, exp)
	}()
}

// provision creates the chart artifact of exp on behalf of creator.
func (p *studyChartProvisioner) provision(ctx context.Context, creator string, exp *msgpb.Experiment) {
	if p == nil || p.artifacts == nil || !p.artifacts.hasDashboardObserver() {
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
	if _, err := p.artifacts.persistArtifact(provisionCtx, createArtifactContext{creator: creator, parentXID: xid}, exp.GetName(), studyChartArtifact(exp, xid)); err != nil {
		studyChartProvisioningFailuresTotal.WithLabelValues(studyChartStageArtifact).Inc()
		logging.Error(ctx, "Failed to create the study chart artifact; the study was created without a chart", "experiment_name", exp.GetName(), "error", err)
	}
}

// studyChartArtifact builds study xid's ChartArtifact with the chart's REST path as Url.Url.
func studyChartArtifact(exp *msgpb.Experiment, xid int64) *msgpb.Artifact {
	var metricNames []string
	for _, metric := range exp.GetStudyConfig().GetMetrics() {
		metricNames = append(metricNames, metric.GetMetricId())
	}
	return validator.FillArtifactNullValues(&msgpb.Artifact{
		Url: &msgpb.Url{Url: "/v1/" + validator.ConstructExperimentChartName(xid, validator.StudyChartID(xid)), DisplayName: studyChartTitle}, Tags: []string{"study", "parallel-coordinates"}, LifecyclePhase: msgpb.Artifact_OUTPUT,
		Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_Chart{Chart: &msgpb.ChartArtifact{Title: studyChartTitle, MetricNames: metricNames, Status: msgpb.ChartArtifact_PROVISIONING}}},
	})
}
