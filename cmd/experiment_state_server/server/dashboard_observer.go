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

	"golang/grpc/metadata/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// authForwardKeys are the credential headers the Dashboard Service reads off a call, in the
// order it looks for them. See jwtFromMetadata in internal/auth.
var authForwardKeys = []string{"x-goog-iap-jwt-assertion", "authorization"}

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
	outgoing := metadata.MD{}
	for _, key := range authForwardKeys {
		if values := incoming.Get(key); len(values) > 0 {
			outgoing.Set(key, values...)
		}
	}
	if len(outgoing) == 0 {
		return ctx, false
	}
	return metadata.NewOutgoingContext(ctx, outgoing), true
}

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

		ctxAuth, forwarded := withForwardedAuth(ctxBg)
		if !forwarded {
			// Not fatal here -- let the Dashboard Service be the one to decide -- but the call
			// is almost certainly about to come back Unauthenticated, and that is far easier to
			// act on from this line than from the error it turns into.
			logging.Warn(ctxBg, "No caller credentials to forward to Dashboard service; CreateChart will likely be rejected")
		}

		// Call Dashboard service.
		_, err := o.client.CreateChart(ctxAuth, &dashboard_pb.CreateChartRequest{
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
