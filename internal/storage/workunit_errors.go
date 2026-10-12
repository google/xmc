// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package storage

import (
	"fmt"

	"xmanager_cloud/internal/grpcerr"
)

// WorkUnitNotFoundError reports the work units of an experiment that a write
// referenced but that do not exist. It wraps grpcerr.ErrNotFound, so
// errors.Is(err, grpcerr.ErrNotFound) holds and status.Code(err) is NotFound.
type WorkUnitNotFoundError struct {
	XID int64
	// WIDs lists the missing work unit IDs, sorted and deduplicated.
	WIDs []int64
}

// Error implements error.
func (e *WorkUnitNotFoundError) Error() string {
	return fmt.Sprintf("work units %v of experiment %d not found", e.WIDs, e.XID)
}

// Unwrap returns grpcerr.ErrNotFound so callers can classify the error by code.
func (e *WorkUnitNotFoundError) Unwrap() error {
	return grpcerr.ErrNotFound
}
