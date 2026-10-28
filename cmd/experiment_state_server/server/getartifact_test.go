// Copyright 2025 Google LLC
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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	msgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/featureflags"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
)

func TestGetArtifact_HappyPath(t *testing.T) {
	ctx := context.Background()
	server, _ := createExperimentStateServer(ctx, t)
	exp := mustCreateExperiment(ctx, t, server, nil)

	artifact := &msgpb.Artifact{
		Url: &msgpb.Url{
			Url: "http://www.google.com",
		},
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_Url{
				Url: &msgpb.UrlArtifact{
					Url: "http://www.google.com",
				},
			},
		},
		Acls: &xidmsgpb.XidAcls{
			Writers: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdArtifact := mustCreateArtifact(ctx, t, server, exp.GetName(), artifact)

	req := &msgpb.GetArtifactRequest{Name: createdArtifact.GetName()}
	got, err := server.GetArtifact(ctx, req)
	if err != nil {
		t.Fatalf("GetArtifact(name=%q) returned an unexpected error: %v", req.GetName(), err)
	}

	opts := []cmp.Option{
		protocmp.Transform(),
	}
	if diff := cmp.Diff(createdArtifact, got, opts...); diff != "" {
		t.Errorf("GetArtifact(name=%q) mismatch (-want +got):\n%s", req.GetName(), diff)
	}
}

func TestGetArtifact_BadPaths(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)

	testCases := []struct {
		name    string
		req     *msgpb.GetArtifactRequest
		wantErr error
	}{
		{
			name:    "invalid_name",
			req:     &msgpb.GetArtifactRequest{Name: "invalid-name"},
			wantErr: grpcerr.ErrInvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.GetArtifact(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetArtifact(name=%q) returned error %v, want %v", tc.req.GetName(), err, tc.wantErr)
			}
		})
	}
}

func TestGetArtifact_DependencyFailures(t *testing.T) {
	ctx := context.Background()

	goodReq := &msgpb.GetArtifactRequest{Name: "artifacts/123"}

	testCases := []struct {
		name          string
		authenticator auth.AuthorizerInterface
		xidClient     xidmsgpb.XidServiceClient
		storage       storage.Storage
		wantErr       error
	}{
		{
			name:          "auth_fails",
			authenticator: &mockAuthenticator{err: grpcerr.ErrUnauthenticated},
			xidClient:     &mockXidClient{},
			storage:       &storage.MockStorage{},
			wantErr:       grpcerr.ErrUnauthenticated,
		},
		{
			name: "xid_get_fails",
			xidClient: &mockXidClient{
				getXidEntryErr: fmt.Errorf("XID get failed"),
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "xid_get_permission_denied",
			xidClient: &mockXidClient{
				getXidEntryErr: grpcerr.ErrPermissionDenied,
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrPermissionDenied,
		},
		{
			name: "xid_get_not_found",
			xidClient: &mockXidClient{
				getXidEntryErr: grpcerr.ErrNotFound,
			},
			storage: &storage.MockStorage{},
			wantErr: grpcerr.ErrNotFound,
		},
		{
			name: "storage_fetch_fails",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchArtifactErr: fmt.Errorf("storage fetch failed"),
			},
			wantErr: grpcerr.ErrInternal,
		},
		{
			name: "storage_fetch_not_found",
			xidClient: &mockXidClient{
				getXidEntryResp: &xidmsgpb.XidEntry{Name: proto.String("xids/123")},
			},
			storage: &storage.MockStorage{
				FetchArtifactErr: grpcerr.ErrNotFound,
			},
			wantErr: grpcerr.ErrNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []testServerOpt
			if tc.storage != nil {
				opts = append(opts, withStorage(tc.storage))
			}
			if tc.authenticator != nil {
				opts = append(opts, withAuthorizer(tc.authenticator))
			}
			if tc.xidClient != nil {
				opts = append(opts, withXidClient(tc.xidClient))
			}
			server := newTestServer(t, opts...)
			_, err := server.GetArtifact(ctx, goodReq)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("GetArtifact(name=%q) returned error %v, want %v", goodReq.GetName(), err, tc.wantErr)
			}
		})
	}
}

func TestEnrichManagedXprof(t *testing.T) {
	ctx := context.Background()

	baseManagedXprofArtifact := &msgpb.Artifact{
		Parent: "experiments/1/workUnits/1",
		Payload: &msgpb.ArtifactPayload{
			Type: &msgpb.ArtifactPayload_ManagedXprof{
				ManagedXprof: &msgpb.ManagedXprofArtifact{},
			},
		},
	}

	testCases := []struct {
		name         string
		artifact     *msgpb.Artifact
		mockStorage  *storage.MockStorage
		wantEnriched bool
	}{
		{
			name: "not_managed_xprof",
			artifact: &msgpb.Artifact{
				Payload: &msgpb.ArtifactPayload{},
			},
			wantEnriched: false,
		},
		{
			name:         "malformed_parent",
			artifact:     prototest.Modify(baseManagedXprofArtifact, func(a *msgpb.Artifact) { a.Parent = "invalid-parent" }),
			wantEnriched: false,
		},
		{
			name:     "storage_error",
			artifact: proto.Clone(baseManagedXprofArtifact).(*msgpb.Artifact),
			mockStorage: &storage.MockStorage{
				FetchCurrentWorkUnitExecutionErr: fmt.Errorf("storage failed"),
			},
			wantEnriched: false,
		},
		{
			// A NotFound execution is an expected pre-execution state, so enrichment
			// is skipped without surfacing an error.
			name:     "storage_not_found",
			artifact: proto.Clone(baseManagedXprofArtifact).(*msgpb.Artifact),
			mockStorage: &storage.MockStorage{
				FetchCurrentWorkUnitExecutionErr: grpcerr.ErrNotFound,
			},
			wantEnriched: false,
		},
		{
			name:     "success_enrichment",
			artifact: proto.Clone(baseManagedXprofArtifact).(*msgpb.Artifact),
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: true,
		},
		{
			// An experiment-scoped artifact has no single MLRun (each work unit
			// execution owns its own), so it must not be enriched even when storage
			// has an MLRun URL.
			name:     "no_enrichment_experiment_parent",
			artifact: prototest.Modify(baseManagedXprofArtifact, func(a *msgpb.Artifact) { a.Parent = "experiments/1" }),
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, withStorage(tc.mockStorage))
			s.enrichManagedXprof(ctx, tc.artifact)

			xprof := tc.artifact.GetPayload().GetManagedXprof()
			if xprof == nil {
				if tc.wantEnriched {
					t.Error("enrichManagedXprof() expected enrichment but payload is not ManagedXprof")
				}
				return
			}

			gotURL := xprof.GetXprof().GetXprofUrl()
			if (gotURL != "") != tc.wantEnriched {
				t.Errorf("enrichManagedXprof() enriched = %v, want %v (URL: %q)", gotURL != "", tc.wantEnriched, gotURL)
			}

			// The top-level artifact URL must also carry the MLRun URL so clients that
			// read artifact.url link directly to the run.
			gotArtifactURL := tc.artifact.GetUrl().GetUrl()
			if (gotArtifactURL != "") != tc.wantEnriched {
				t.Errorf("enrichManagedXprof() top-level url enriched = %v, want %v (URL: %q)", gotArtifactURL != "", tc.wantEnriched, gotArtifactURL)
			}
		})
	}
}

func TestEnrichArtifactOutputFields(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name         string
		artifact     *msgpb.Artifact
		mockStorage  *storage.MockStorage
		wantEnriched bool
	}{
		{
			name: "ineligible_phase_unspecified",
			artifact: &msgpb.Artifact{
				LifecyclePhase: msgpb.Artifact_LIFECYCLE_PHASE_UNSPECIFIED,
				Parent:         "experiments/1/workUnits/1",
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{},
					},
				},
			},
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: false,
		},
		{
			name: "ineligible_phase_input",
			artifact: &msgpb.Artifact{
				LifecyclePhase: msgpb.Artifact_INPUT,
				Parent:         "experiments/1/workUnits/1",
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{},
					},
				},
			},
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: false,
		},
		{
			name: "eligible_phase_execution",
			artifact: &msgpb.Artifact{
				LifecyclePhase: msgpb.Artifact_EXECUTION,
				Parent:         "experiments/1/workUnits/1",
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{},
					},
				},
			},
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: true,
		},
		{
			name: "eligible_phase_output",
			artifact: &msgpb.Artifact{
				LifecyclePhase: msgpb.Artifact_OUTPUT,
				Parent:         "experiments/1/workUnits/1",
				Payload: &msgpb.ArtifactPayload{
					Type: &msgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &msgpb.ManagedXprofArtifact{},
					},
				},
			},
			mockStorage: &storage.MockStorage{
				WorkUnitExecution: &msgpb.WorkUnitExecution{
					Metadata: &msgpb.WorkUnitExecutionMetadata{
						MlrunUrl: "http://mlrun.url",
					},
				},
			},
			wantEnriched: true,
		},
		{
			name: "eligible_phase_but_not_managed_xprof",
			artifact: &msgpb.Artifact{
				LifecyclePhase: msgpb.Artifact_EXECUTION,
				Payload:        &msgpb.ArtifactPayload{},
			},
			mockStorage:  &storage.MockStorage{},
			wantEnriched: false,
		},
		{
			name:         "nil_artifact",
			artifact:     nil,
			mockStorage:  &storage.MockStorage{},
			wantEnriched: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, withStorage(tc.mockStorage))
			s.enrichArtifactOutputFields(ctx, tc.artifact)

			xprof := tc.artifact.GetPayload().GetManagedXprof()
			if xprof == nil {
				if tc.wantEnriched {
					t.Error("enrichArtifactOutputFields() expected enrichment but payload is not ManagedXprof")
				}
				return
			}

			gotURL := xprof.GetXprof().GetXprofUrl()
			if (gotURL != "") != tc.wantEnriched {
				t.Errorf("enrichArtifactOutputFields() enriched = %v, want %v (URL: %q)", gotURL != "", tc.wantEnriched, gotURL)
			}
		})
	}
}

func TestEnrichArtifactOutputFieldsNilSafety(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)

	// Calling enrichArtifactOutputFields with a nil artifact should return immediately without panicking.
	s.enrichArtifactOutputFields(ctx, nil)
}

func TestIsWorkUnitExecutionNotFound(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil_error", err: nil, want: false},
		{name: "not_found_sentinel", err: grpcerr.ErrNotFound, want: true},
		{name: "wrapped_not_found", err: fmt.Errorf("WorkUnit 1 for XID 1 not found: %w", grpcerr.ErrNotFound), want: true},
		{name: "other_error", err: fmt.Errorf("boom"), want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWorkUnitExecutionNotFound(tc.err); got != tc.want {
				t.Errorf("isWorkUnitExecutionNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestEnrichManagedXprof_StoredXprofURL covers artifacts whose xprof_url is already
// stored. With the artifact lifecycle disabled the read path matches main (nothing is
// changed); enabled, the stored xprof_url is mirrored onto an unset top-level url and
// is never replaced by the current execution's MLRun URL (e.g. right after a restart).
func TestEnrichManagedXprof_StoredXprofURL(t *testing.T) {
	const storedURL = "http://stored.mlrun.url"
	stored := &msgpb.Artifact{
		Parent: "experiments/1/workUnits/1",
		Payload: &msgpb.ArtifactPayload{Type: &msgpb.ArtifactPayload_ManagedXprof{
			ManagedXprof: &msgpb.ManagedXprofArtifact{Xprof: &msgpb.XProfUIInstance{XprofUrl: storedURL}},
		}},
	}
	tests := []struct {
		name     string
		enabled  bool
		artifact *msgpb.Artifact
		want     *msgpb.Artifact
	}{
		{
			name:     "disabled leaves stored artifact unchanged",
			artifact: proto.Clone(stored).(*msgpb.Artifact),
			want:     proto.Clone(stored).(*msgpb.Artifact),
		},
		{
			name:     "enabled mirrors stored xprof_url onto empty url",
			enabled:  true,
			artifact: proto.Clone(stored).(*msgpb.Artifact),
			want:     prototest.Modify(stored, func(a *msgpb.Artifact) { a.Url = &msgpb.Url{Url: storedURL} }),
		},
		{
			name:     "enabled keeps an explicit url",
			enabled:  true,
			artifact: prototest.Modify(stored, func(a *msgpb.Artifact) { a.Url = &msgpb.Url{Url: "http://explicit.url"} }),
			want:     prototest.Modify(stored, func(a *msgpb.Artifact) { a.Url = &msgpb.Url{Url: "http://explicit.url"} }),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The current execution carries a different MLRun URL, as right after a restart.
			store := &storage.MockStorage{WorkUnitExecution: &msgpb.WorkUnitExecution{
				Metadata: &msgpb.WorkUnitExecutionMetadata{MlrunUrl: "http://new.execution.mlrun.url"},
			}}
			cfg := &config.Config{FeatureFlags: featureflags.Flags{ArtifactLifecycleEnabled: tc.enabled}}
			s := newTestServer(t, withStorage(store), withConfig(cfg))

			s.enrichManagedXprof(context.Background(), tc.artifact)

			if diff := cmp.Diff(tc.want, tc.artifact, protocmp.Transform()); diff != "" {
				t.Errorf("enrichManagedXprof() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
