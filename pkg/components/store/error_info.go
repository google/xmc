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
