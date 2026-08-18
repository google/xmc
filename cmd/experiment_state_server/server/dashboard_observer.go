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
	"sync"
	"time"

	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	dashboard_apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	dashboard_pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type dashboardObserver struct {
	client  *toollibs.DashboardToolClient
	storage storage.Storage
	wg      *sync.WaitGroup
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

		// Call Dashboard service.
		_, err := o.client.CreateChart(ctxBg, &dashboard_pb.CreateChartRequest{
			Parent: fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
			Chart: &dashboard_apipb.Chart{
				Title: title,
				Xid:   xid,
				Wid:   wid,
			},
		})

		if err != nil {
			logging.Error(ctxBg, "Background Chart provisioning failed", "error", err)
			cArtifact.Status = essmsgpb.ChartArtifact_FAILED
			cArtifact.ErrorMessage = err.Error()
		} else {
			logging.Info(ctxBg, "Background Chart provisioning succeeded")
			cArtifact.Status = essmsgpb.ChartArtifact_SUCCESS
		}

		aid, _ := validator.ValidateArtifactName(artifactToProvision.GetName())

		if _, err := o.storage.UpdateArtifact(ctxBg, &storage.ArtifactUpdate{
			AID:        aid,
			Artifact:   artifactToProvision,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"payload"}},
		}); err != nil {
			logging.Error(ctxBg, "Failed to update ChartArtifact status in Spanner", "error", err)
		}
	}()
}
