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

package shared

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsPermanentStorageError reports whether a WorkUnitExecution update failed in a
// way that retrying cannot fix (for example, the work unit no longer exists).
// All other errors, including deadlines and Unavailable/Aborted, are treated as
// transient. Spanner's "Session not found" is also NotFound, but the Spanner
// client retries it internally, so it is not expected to surface here.
func IsPermanentStorageError(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied:
		return true
	default:
		return false
	}
}
