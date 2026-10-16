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

package store

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorInfoDomain is the google.rpc.ErrorInfo domain of machine-readable store rejections.
const ErrorInfoDomain = "components.xmc.dev"

// google.rpc.ErrorInfo reasons of store rejections. Callers branch on the reason, never on the
// message. All of them use code FailedPrecondition: never Aborted, which the Spanner client
// retries when a read-write transaction body returns it.
const (
	// ReasonDependencyNotRegistered rejects an intent that declares a dependency that is not
	// registered (or is soft-deleted). The caller waits for the dependency and retries.
	ReasonDependencyNotRegistered = "DEPENDENCY_NOT_REGISTERED"
	// ReasonDependencyCycle rejects an intent whose dependencies would form a dependency cycle.
	// Metadata "cycle" lists the canonical names on the cycle, comma-separated. The cycle always
	// passes through metadata "component": every intent is validated before it is applied, so the
	// stored dependency graph stays acyclic and only the rejected intent can close a cycle.
	ReasonDependencyCycle = "DEPENDENCY_CYCLE"
	// ReasonActiveDependentsExist rejects a PrepareDeactivation or a non-cascading ExecuteDeletion
	// of a component that active (neither disabled nor deleted) components still depend on,
	// directly or transitively. Metadata "dependents" lists their
	// canonical names, comma-separated.
	ReasonActiveDependentsExist = "ACTIVE_DEPENDENTS_EXIST"
	// ReasonStaleGeneration rejects a changed intent from an older generation of the owner object.
	ReasonStaleGeneration = "STALE_GENERATION"
	// ReasonSourceNamespaceConflict rejects an object of the same name in another namespace; the
	// first owner keeps the record.
	ReasonSourceNamespaceConflict = "SOURCE_NAMESPACE_CONFLICT"
	// ReasonSourceIncarnationMismatch rejects another incarnation (a different uid) of the object
	// in the owner's namespace. After proving that its own object is live, the caller may retry
	// once with expected_owner_uid set to the owner_uid metadata.
	ReasonSourceIncarnationMismatch = "SOURCE_INCARNATION_MISMATCH"
)

// google.rpc.ErrorInfo metadata keys of store rejections, part of the contract callers match.
const (
	metadataKeyOperation  = "operation"
	metadataKeyComponent  = "component"
	metadataKeyDependency = "dependency"
	metadataKeyCycle      = "cycle"
	metadataKeyDependents = "dependents"
)

// ErrorInfo metadata keys naming the recorded owner in source-ownership rejections. Callers such as
// xmc-bridge read them to decide a same-cluster CAS retry.
const (
	MetadataKeyOwnerNamespace  = "owner_namespace"
	MetadataKeyOwnerUID        = "owner_uid"
	MetadataKeyOwnerClusterUID = "owner_cluster_uid"
	MetadataKeyOwnerGeneration = "owner_generation"
)

// Values of metadata "operation": the rejected CMS operation.
const (
	operationSetComponentIntent  = "SetComponentIntent"
	operationPrepareDeactivation = "PrepareDeactivation"
	operationExecuteDeletion     = "ExecuteDeletion"
)

// errorWithInfo returns a status error with code and msg carrying a google.rpc.ErrorInfo with
// reason, ErrorInfoDomain and metadata.
func errorWithInfo(code codes.Code, reason string, metadata map[string]string, msg string) error {
	st := status.New(code, msg)
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: ErrorInfoDomain, Metadata: metadata})
	if err != nil {
		// WithDetails fails only for an OK code or an unmarshalable detail, neither of which occurs.
		return st.Err()
	}
	return detailed.Err()
}
