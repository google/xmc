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

package hypercomputeclient

import (
	"errors"
	"net/http"
)

// IsAlreadyExists reports whether err is the control plane's AlreadyExists
// response. The control plane surfaces this two ways: an HTTP 409 Conflict from the
// create call, and a long-running-operation failure carrying google.rpc.Code
// AlreadyExists (6). It branches on the typed errors' codes rather than matching
// error strings, which is brittle across message-wording changes.
func IsAlreadyExists(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusConflict
	}
	var opErr *OperationError
	if errors.As(err, &opErr) {
		return opErr.Code == CodeAlreadyExists
	}
	return false
}
