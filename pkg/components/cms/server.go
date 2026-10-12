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

// Package cms implements the ComponentManagerService gRPC server for XMC.
package cms

import (
	"context"
	"fmt"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
)

// ServerOption configures optional dependencies on a CMS Server.
type ServerOption func(*Server)

// WithAuthorizer configures the CallerAuthorizer used to authenticate and authorize RPC requests.
func WithAuthorizer(auth CallerAuthorizer) ServerOption {
	return func(s *Server) { s.auth = auth }
}

// Server implements the componentspb.ComponentManagerServiceServer interface.
type Server struct {
	componentspb.UnimplementedComponentManagerServiceServer
	store store.Store
	auth  CallerAuthorizer
}

// NewServer creates a new ComponentManagerService gRPC server backed by the provided Store.
func NewServer(s store.Store, opts ...ServerOption) *Server {
	srv := &Server{store: s}
	for _, opt := range opts {
		if opt != nil {
			opt(srv)
		}
	}
	return srv
}

// Store returns the underlying store instance.
func (s *Server) Store() store.Store {
	return s.store
}

func (s *Server) authenticate(ctx context.Context) error {
	if s.auth == nil {
		return status.Error(codes.Unauthenticated, "CMS authorizer is not configured")
	}
	_, err := s.auth.Authenticate(ctx)
	return err
}

// validateComponentName validates and returns the canonical component name.
func validateComponentName(name string) (string, error) {
	canonical := naming.CanonicalComponentName(name)
	if canonical == "" {
		return "", status.Error(codes.InvalidArgument, "component name is required")
	}
	if !naming.IsValidComponentName(canonical) {
		return "", status.Errorf(codes.InvalidArgument, "invalid component name %q", name)
	}
	return canonical, nil
}

// namedRequest defines a constraint for protobuf requests containing a resource name.
type namedRequest interface {
	comparable
	GetName() string
}

// validateNamedRequest checks that the request is non-nil, the store is initialized,
// and the component name is valid, returning the canonical component name.
func validateNamedRequest[T namedRequest](s *Server, req T) (string, error) {
	var zero T
	if req == zero {
		return "", status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return "", err
	}
	return validateComponentName(req.GetName())
}

func validateMutatingRequest[T namedRequest](ctx context.Context, s *Server, req T) (string, error) {
	if err := s.authorizeMutating(ctx); err != nil {
		return "", err
	}
	return validateNamedRequest(s, req)
}

// authorizeMutating fails closed unless the caller may mutate component state.
func (s *Server) authorizeMutating(ctx context.Context) error {
	if s.auth == nil {
		return status.Error(codes.Unauthenticated, "CMS authorizer is not configured")
	}
	_, err := s.auth.AuthorizeMutating(ctx)
	return err
}

func validateReadRequest[T namedRequest](ctx context.Context, s *Server, req T) (string, error) {
	if err := s.authenticate(ctx); err != nil {
		return "", err
	}
	return validateNamedRequest(s, req)
}

// checkStoreReady verifies that the component store is initialized.
func (s *Server) checkStoreReady() error {
	if s.store == nil {
		return status.Error(codes.Unavailable, "component store is uninitialized")
	}
	return nil
}

// SetComponentIntent applies or updates component intent within the store.
func (s *Server) SetComponentIntent(ctx context.Context, req *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
	canonicalName, err := validateMutatingRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	if req.GetSpec() == nil {
		return nil, status.Error(codes.InvalidArgument, "component spec is required")
	}
	src, err := requestSourceFromProto(req.GetSource())
	if err != nil {
		return nil, err
	}

	specToSet := req.GetSpec()
	if req.GetUpdateMask() != nil && len(req.GetUpdateMask().GetPaths()) > 0 {
		existingRec, err := s.store.GetComponent(ctx, canonicalName)
		if err != nil {
			return nil, err
		}
		specToSet = store.ApplySpecUpdateMask(existingRec.Spec, req.GetSpec(), req.GetUpdateMask().GetPaths())
	}

	if version := specToSet.GetVersion(); version != "" {
		if _, err := semver.Parse(version); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid component version %q: %v", version, err)
		}
	}
	deps, err := canonicalDependencies(canonicalName, specToSet.GetDependencies())
	if err != nil {
		return nil, err
	}
	if len(deps) > 0 {
		specToSet = proto.Clone(specToSet).(*componentspb.ComponentSpec)
		specToSet.Dependencies = deps
	}
	if req.GetValidateOnly() {
		protoComp := &componentspb.Component{
			Name: naming.FullComponentName(canonicalName),
			Spec: proto.Clone(specToSet).(*componentspb.ComponentSpec),
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			},
		}
		return &componentspb.SetComponentIntentResponse{
			Component:     protoComp,
			StatusMessage: "Validation successful (validate_only)",
		}, nil
	}

	rec, err := s.applyIntent(ctx, canonicalName, specToSet, src)
	if err != nil {
		return nil, err
	}
	return &componentspb.SetComponentIntentResponse{
		Component: store.ToProtoComponent(rec),
	}, nil
}

// requestSourceFromProto converts and validates the source of a request (including validate_only),
// or returns nil when src is nil.
func requestSourceFromProto(src *componentspb.RequestSource) (*store.RequestSource, error) {
	if src == nil {
		return nil, nil
	}
	storeSrc := store.RequestSource{
		UID: src.GetUid(), Generation: src.GetGeneration(), Namespace: src.GetNamespace(),
		ClusterUID: src.GetCluster().GetUid(), Epoch: src.GetCluster().GetEpoch(),
		ClusterName: src.GetCluster().GetName(), ExpectedOwnerUID: src.GetExpectedOwnerUid(),
	}
	if err := store.ValidateRequestSource(storeSrc); err != nil {
		return nil, err
	}
	return &storeSrc, nil
}

// requestSourcesFromProto converts an optional RequestSource proto into a variadic slice for store calls.
func requestSourcesFromProto(src *componentspb.RequestSource) ([]store.RequestSource, error) {
	storeSrc, err := requestSourceFromProto(src)
	if err != nil || storeSrc == nil {
		return nil, err
	}
	return []store.RequestSource{*storeSrc}, nil
}

// applyIntent stores spec ordered by src when it is set, and by spec.generation otherwise.
func (s *Server) applyIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, src *store.RequestSource) (*store.ComponentRecord, error) {
	if src == nil {
		return s.store.SetComponentIntent(ctx, canonicalName, spec, nil, spec.GetGeneration())
	}
	return s.store.SetSourcedComponentIntent(ctx, canonicalName, spec, nil, *src)
}

// canonicalDependencies returns spec.dependencies as deduplicated canonical component names in
// declaration order. This is the single canonicalization point at the CMS API boundary, so the
// persisted spec holds canonical names. It rejects entries that are not valid component names or
// that reference the component itself, so a typo cannot silently drop a dependency edge and a
// self-reference cannot block readiness forever.
func canonicalDependencies(canonicalName string, deps []string) ([]string, error) {
	var res []string
	seen := make(map[string]bool, len(deps))
	for _, dep := range deps {
		canonicalDep := naming.CanonicalComponentName(dep)
		if !naming.IsValidComponentName(canonicalDep) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid dependency name %q: must be a DNS-1123 component name", dep)
		}
		if canonicalDep == canonicalName {
			return nil, status.Errorf(codes.InvalidArgument, "component %q cannot depend on itself", canonicalName)
		}
		if !seen[canonicalDep] {
			seen[canonicalDep] = true
			res = append(res, canonicalDep)
		}
	}
	return res, nil
}

// PrepareDeactivation evaluates the safety of deactivating a component and initiates draining.
func (s *Server) PrepareDeactivation(ctx context.Context, req *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
	canonicalName, err := validateMutatingRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	if dt := req.GetDrainingTimeout(); dt != nil {
		if err := dt.CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid draining timeout: %v", err)
		}
		if dt.AsDuration() < 0 {
			return nil, status.Error(codes.InvalidArgument, "draining timeout cannot be negative")
		}
	}
	srcs, err := requestSourcesFromProto(req.GetSource())
	if err != nil {
		return nil, err
	}

	res, err := s.store.PrepareDeactivation(ctx, canonicalName, req.GetForce(), srcs...)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, status.Error(codes.Internal, "store returned nil result without error")
	}

	drainSec := calculateEffectiveDrainSeconds(res.RecommendedDrainSec, req.GetDrainingTimeout())

	msg := fmt.Sprintf("Component %s deactivation prepared successfully (drain: %ds)", canonicalName, drainSec)
	if len(res.CascadedDeactivations) > 0 {
		msg = fmt.Sprintf("Component %s and %d dependent(s) deactivation prepared successfully (drain: %ds)", canonicalName, len(res.CascadedDeactivations), drainSec)
	}

	return &componentspb.PrepareDeactivationResponse{
		State:                 componentspb.ComponentState_COMPONENT_STATE_DISABLING,
		DeactivationStartTime: timestamppb.Now(),
		Message:               msg,
	}, nil
}

// calculateEffectiveDrainSeconds computes the drain timeout in seconds from an optional durationpb.Duration,
// rounding up sub-second durations and capping at math.MaxInt32 to prevent integer overflow.
func calculateEffectiveDrainSeconds(defaultSec int32, dt *durationpb.Duration) int32 {
	if dt == nil || (dt.Seconds <= 0 && dt.Nanos <= 0) {
		return defaultSec
	}
	secs := dt.Seconds
	if dt.Nanos > 0 {
		secs++
	}
	if secs > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(secs)
}

// CompleteDeactivation finalizes component deactivation after draining is complete.
func (s *Server) CompleteDeactivation(ctx context.Context, req *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
	canonicalName, err := validateMutatingRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	srcs, err := requestSourcesFromProto(req.GetSource())
	if err != nil {
		return nil, err
	}

	if err := s.store.CompleteDeactivation(ctx, canonicalName, req.GetForce(), srcs...); err != nil {
		return nil, err
	}

	return &componentspb.CompleteDeactivationResponse{
		State:          componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		CompletionTime: timestamppb.Now(),
		Message:        fmt.Sprintf("Component %s deactivation completed successfully", canonicalName),
	}, nil
}

// UpdateObservedGeneration records the controller-observed generation and access coordinates.
func (s *Server) UpdateObservedGeneration(ctx context.Context, req *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	canonicalName, err := validateMutatingRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	if req.GetObservedGeneration() < 0 {
		return nil, status.Error(codes.InvalidArgument, "observed generation cannot be negative")
	}
	if req.GetState() < componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED || req.GetState() > componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return nil, status.Errorf(codes.InvalidArgument, "invalid component state %d", req.GetState())
	}
	srcs, err := requestSourcesFromProto(req.GetSource())
	if err != nil {
		return nil, err
	}

	rec, err := s.store.UpdateObservedGeneration(ctx, canonicalName, req.GetObservedGeneration(), nil, req.GetState(), req.GetStatusMessage(), srcs...)
	if err != nil {
		return nil, err
	}
	return &componentspb.UpdateObservedGenerationResponse{
		Component: store.ToProtoComponent(rec),
	}, nil
}

// ExecuteDeletion executes component deletion according to policy and deletion protection.
func (s *Server) ExecuteDeletion(ctx context.Context, req *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	canonicalName, err := validateMutatingRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}

	policy := req.GetDeletionPolicy()
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		policy = componentspb.DeletionPolicy_DELETION_POLICY_DELETE
	}

	if err := s.store.ExecuteDeletion(ctx, canonicalName, policy, req.GetForce()); err != nil {
		return nil, err
	}

	return &componentspb.ExecuteDeletionResponse{
		State:   componentspb.ComponentState_COMPONENT_STATE_DELETED,
		Message: fmt.Sprintf("Component %s deleted successfully", canonicalName),
	}, nil
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *Server) ResolveComponentSnapshot(ctx context.Context, req *componentspb.ResolveComponentSnapshotRequest) (*componentspb.ResolveComponentSnapshotResponse, error) {
	canonicalName, err := validateReadRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	gen := req.GetGeneration()
	if gen < 0 {
		return nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	rec, coords, err := s.store.ResolveComponentSnapshot(ctx, canonicalName, gen)
	if err != nil {
		return nil, err
	}

	resp := &componentspb.ResolveComponentSnapshotResponse{
		Component: store.ToProtoComponent(rec),
		State:     rec.State,
	}
	if coords != nil {
		resp.Coordinates = proto.Clone(coords).(*componentspb.ResolvedComponentCoordinates)
	}
	return resp, nil
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *Server) ValidateComponentRequirement(ctx context.Context, req *componentspb.ValidateComponentRequirementRequest) (*componentspb.ValidateComponentRequirementResponse, error) {
	canonicalName, err := validateReadRequest(ctx, s, req)
	if err != nil {
		return nil, err
	}
	if minVersion := req.GetMinimumVersion(); minVersion != "" {
		if _, err := semver.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}
	if _, ok := componentspb.ComponentType_name[int32(req.GetRequiredType())]; !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid required type %d", req.GetRequiredType())
	}

	res, err := s.store.ValidateComponentRequirement(ctx, canonicalName, req.GetRequiredType(), req.GetMinimumVersion())
	if err != nil {
		return nil, err
	}

	resp := &componentspb.ValidateComponentRequirementResponse{
		Satisfied: res.IsSatisfied,
		Reason:    res.Reason,
	}
	if res.Component != nil {
		resp.Component = store.ToProtoComponent(res.Component)
	}
	return resp, nil
}

// GetPlatformHealth retrieves overall system component health metrics and status.
// If pageSize is unspecified or 0, the store defaults to 50 (max 1000) per AIP-158.
func (s *Server) GetPlatformHealth(ctx context.Context, req *componentspb.GetPlatformHealthRequest) (*componentspb.GetPlatformHealthResponse, error) {
	if err := s.authenticate(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return nil, err
	}
	if _, err := store.NormalizePageSize(req.GetPageSize()); err != nil {
		return nil, err
	}

	return s.store.GetPlatformHealth(ctx, req.GetFilter(), req.GetPageSize(), req.GetPageToken())
}

// DeclareClusterEpoch declares the calling cluster the writer of Component intents at its epoch.
func (s *Server) DeclareClusterEpoch(ctx context.Context, req *componentspb.DeclareClusterEpochRequest) (*componentspb.DeclareClusterEpochResponse, error) {
	// A declaration can take over every component a cluster owns, so it needs the same
	// authorization as the other mutating RPCs.
	if err := s.authorizeMutating(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return nil, err
	}
	c := req.GetCluster()
	if c == nil {
		return nil, status.Error(codes.InvalidArgument, "cluster identity is required")
	}
	res, err := s.store.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: c.GetUid(), Name: c.GetName(), Epoch: c.GetEpoch()})
	if err != nil {
		return nil, err
	}
	return &componentspb.DeclareClusterEpochResponse{
		ActiveCluster:     &componentspb.ClusterIdentity{Uid: res.Active.UID, Name: res.Active.Name, Epoch: res.Active.Epoch},
		RecordsReassigned: res.RecordsReassigned,
		Outcome:           declarationOutcomes[res.Outcome],
	}, nil
}

// declarationOutcomes maps store declaration outcomes to their API values.
var declarationOutcomes = map[store.DeclarationOutcome]componentspb.DeclareClusterEpochResponse_Outcome{
	store.DeclarationCreated:   componentspb.DeclareClusterEpochResponse_OUTCOME_CREATED,
	store.DeclarationRefreshed: componentspb.DeclareClusterEpochResponse_OUTCOME_REFRESHED,
	store.DeclarationRaised:    componentspb.DeclareClusterEpochResponse_OUTCOME_RAISED,
	store.DeclarationTakeover:  componentspb.DeclareClusterEpochResponse_OUTCOME_TAKEOVER,
}
