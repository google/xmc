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
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	dashboard_apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	dashboard_pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	// maxProvisionedMetrics bounds how many of a ChartArtifact's metric_names become plots.
	// The list is client-supplied and the data engine runs one query per plot, so an
	// unbounded list turns a single GetChartData into a proportionally expensive fan-out.
	maxProvisionedMetrics = 20

	// plotLayoutColumns lays the derived plots out two to a row, which keeps the summary
	// boxes side by side rather than stacked down a single column.
	plotLayoutColumns = 2
)

// nonPlotIDChars matches every run of characters that cannot appear in a plot ID. Metric
// names are path-like ("chart_demo/loss"), so they need slugifying before use as an ID.
var nonPlotIDChars = regexp.MustCompile(`[^a-z0-9]+`)

// authForwardKeys are the credential headers the Dashboard Service reads off a call, in the
// order it looks for them. See jwtFromMetadata in internal/auth.
var authForwardKeys = []string{"x-goog-iap-jwt-assertion", "authorization"}

// identityForwardKeys travel with the credentials but are not credentials. resolveUserEmail in
// internal/auth prefers x-goog-user-email over the token's own email claim, so dropping it here
// would authorize the chart against a different principal than the rest of the caller's
// requests. Carrying one of these and no token is still an uncredentialed call.
var identityForwardKeys = []string{"x-goog-user-email"}

// withForwardedAuth copies the caller's credentials from ctx's incoming metadata onto its
// outgoing metadata, so the CreateChart RPC below travels as the user who created the
// artifact.
//
// It has to be done by hand twice over. grpc-go does not propagate incoming metadata to
// outgoing calls, and the Dashboard Service connection is dialed with insecure credentials
// and no per-RPC credentials, so without this the call arrives with no token at all and
// CreateChart rejects it with Unauthenticated. Nor would the Experiment State Server's own
// identity help if it had one: CreateChart authorizes against the *user's* WRITER role on the
// experiment, so the user's token is the one that has to make the trip.
func withForwardedAuth(ctx context.Context) (context.Context, bool) {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, false
	}
	// Build on whatever outgoing metadata ctx already carries rather than replacing it.
	// Nothing sets any today -- this is the only NewOutgoingContext in the tree, and the
	// Dashboard connection is dialed without interceptors -- but a tracing or request-ID
	// interceptor added later would otherwise have its headers silently dropped here.
	outgoing, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		outgoing = outgoing.Copy()
	} else {
		outgoing = metadata.MD{}
	}
	forwarded := false
	for _, key := range authForwardKeys {
		if copyMetadataValue(incoming, outgoing, key) {
			forwarded = true
		}
	}
	for _, key := range identityForwardKeys {
		copyMetadataValue(incoming, outgoing, key)
	}
	if !forwarded {
		return ctx, false
	}
	return metadata.NewOutgoingContext(ctx, outgoing), true
}

// copyMetadataValue copies key from incoming to outgoing, reporting whether it carried anything.
// A present but empty value counts as absent, which is how internal/auth reads these headers.
func copyMetadataValue(incoming, outgoing metadata.MD, key string) bool {
	values := incoming.Get(key)
	if len(values) == 0 || values[0] == "" {
		return false
	}
	outgoing.Set(key, values...)
	return true
}

type dashboardObserver struct {
	client  *toollibs.DashboardToolClient
	storage storage.Storage
	wg      *sync.WaitGroup
}

// uniquePlotID slugifies a metric name into a plot ID, disambiguating names that slugify to
// the same string ("train/loss" and "train_loss") with a counter suffix. used records every ID
// handed out so far and is mutated.
//
// The counter has to skip IDs already taken rather than simply count occurrences of a slug: a
// metric whose own name ends in a number can otherwise collide with a generated suffix, so
// ["foo", "foo_2", "foo"] would hand out foo_2 twice. Duplicate plot IDs are not cosmetic --
// the UI keys plot data by ID, so both plots would draw the same series.
func uniquePlotID(metric string, used map[string]bool) string {
	slug := strings.Trim(nonPlotIDChars.ReplaceAllString(strings.ToLower(metric), "_"), "_")
	if slug == "" {
		slug = "metric"
	}
	id := slug
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s_%d", slug, n)
	}
	used[id] = true
	return id
}

// plotSpecsForMetrics derives a plot specification from a ChartArtifact's metric names: a
// summary box holding the latest value plus a step-indexed curve, per metric. A chart with
// no plots is not an empty chart to the data engine, it is a chart with nothing to query,
// so it renders blank however much data the work unit logged.
func plotSpecsForMetrics(ctx context.Context, metricNames []string) []*dashboard_apipb.PlotSpec {
	// Size to what the loop below will actually keep, not to the client-supplied list: the
	// break at 2*maxProvisionedMetrics means a list of a million names would otherwise
	// reserve two million pointers to hold forty.
	capacity := min(len(metricNames), maxProvisionedMetrics)
	plots := make([]*dashboard_apipb.PlotSpec, 0, 2*capacity)
	used := make(map[string]bool, capacity)
	for i, metric := range metricNames {
		metric = strings.TrimSpace(metric)
		if metric == "" {
			continue
		}
		if len(plots) >= 2*maxProvisionedMetrics {
			// The chart renders without them and nothing else says so, so the truncation is
			// otherwise invisible to whoever asks why a metric they logged has no plot.
			logging.Warn(ctx, "ChartArtifact names more metrics than can be provisioned; dropping the rest",
				"provisioned", maxProvisionedMetrics, "dropped", len(metricNames)-i)
			break
		}
		id := uniquePlotID(metric, used)
		plots = append(plots,
			&dashboard_apipb.PlotSpec{
				PlotId: id + "_latest",
				Title:  fmt.Sprintf("%s (latest)", metric),
				Type:   dashboard_apipb.PlotType_SUMMARY_BOX,
				Data:   &dashboard_apipb.DataSpec{Metrics: []string{metric}},
			},
			&dashboard_apipb.PlotSpec{
				PlotId:  id + "_curve",
				Title:   fmt.Sprintf("%s vs steps", metric),
				Type:    dashboard_apipb.PlotType_SCATTER,
				Data:    &dashboard_apipb.DataSpec{Metrics: []string{metric}},
				Mapping: &dashboard_apipb.MappingSpec{X: "step"},
			},
		)
	}
	return plots
}

// NewDashboardObserver creates an ArtifactObserver that provisions charts on Dashboard.
func NewDashboardObserver(client *toollibs.DashboardToolClient, s storage.Storage, wg *sync.WaitGroup) ArtifactObserver {
	return &dashboardObserver{
		client:  client,
		storage: s,
		wg:      wg,
	}
}

func (o *dashboardObserver) OnArtifactCreated(ctxBg context.Context, artifactToProvision *essmsgpb.Artifact) {
	// Only care about ChartArtifacts
	if artifactToProvision.GetPayload().GetChart() == nil {
		return
	}

	o.wg.Add(1)
	go func() {
		defer o.wg.Done()

		// Enforce timeout bounds
		ctxBg, cancel := context.WithTimeout(ctxBg, 2*time.Minute)
		defer cancel()

		defer func() {
			if r := recover(); r != nil {
				logging.Error(ctxBg, "Recovered from panic in dashboardObserver", "panic", r)
			}
		}()

		xid, wid, _ := validator.ValidateArtifactParent(artifactToProvision.GetParent())

		cArtifact := artifactToProvision.GetPayload().GetChart()
		title := cArtifact.GetTitle()
		if title == "" {
			title = fmt.Sprintf("Chart for XID %d WID %d", xid, wid)
		}

		ctxAuth, forwarded := withForwardedAuth(ctxBg)
		if !forwarded {
			logging.Warn(ctxBg, "No caller credentials to forward to Dashboard service; CreateChart will likely be rejected")
		}

		// Call Dashboard service.
		created, err := o.client.CreateChart(ctxAuth, &dashboard_pb.CreateChartRequest{
			Parent: fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
			Chart: &dashboard_apipb.Chart{
				Title: title,
				Xid:   xid,
				Wid:   wid,
				Plots: plotSpecsForMetrics(ctxBg, cArtifact.GetMetricNames()),
				PlotLayout: &dashboard_apipb.LayoutSpec{
					Type:    dashboard_apipb.LayoutSpec_GRID,
					Columns: plotLayoutColumns,
				},
			},
		})

		if err != nil {
			logging.Error(ctxBg, "Background Chart provisioning failed", "error", err)
			cArtifact.Status = essmsgpb.ChartArtifact_FAILED
			cArtifact.ErrorMessage = err.Error()
		} else {
			logging.Info(ctxBg, "Background Chart provisioning succeeded")
			cArtifact.Status = essmsgpb.ChartArtifact_SUCCESS
			// Record the chart the service assigned. It is the only link from the artifact to
			// its plots: without the server-assigned resource name a reader holds a list of
			// metric names and no way to find the chart that draws them.
			cArtifact.Chart = created
		}

		aid, _ := validator.ValidateArtifactName(artifactToProvision.GetName())

		// Recording the outcome has to outlive the deadline that governed CreateChart, or a
		// CreateChart that burned the whole two minutes leaves the artifact in PROVISIONING
		// for good.
		storeCtx, storeCancel := context.WithTimeout(context.WithoutCancel(ctxBg), 15*time.Second)
		defer storeCancel()

		if _, err := o.storage.UpdateArtifact(storeCtx, &storage.ArtifactUpdate{
			AID:        aid,
			Artifact:   artifactToProvision,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"payload"}},
		}); err != nil {
			logging.Error(ctxBg, "Failed to update ChartArtifact status in Spanner", "error", err)
		}
	}()
}
