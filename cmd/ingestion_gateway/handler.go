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

package main

import (
	"encoding/json"
	"io"
	"net/http"
)

const (
	// MaxDrainBytes is the maximum amount of remaining request body to drain on error.
	MaxDrainBytes = 5 * 1024 * 1024 // 5MB
)

// MetricsIngestionHandler processes incoming streaming JSON metrics payloads.
func MetricsIngestionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	res := DecodeAndValidateMetrics(r.Body, nil)
	if res.Err != nil {
		drainRequestBody(r.Body, MaxDrainBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(BadRequestErrorPayload{
			Error:   "bad_request",
			Message: res.Err.Error(),
		})
		return
	}

	if res.Validation != nil {
		drainRequestBody(r.Body, MaxDrainBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(res.Validation)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "accepted",
		"count":  res.ValidCount,
	})
}

func drainRequestBody(r io.Reader, maxBytes int64) {
	if r == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(r, maxBytes))
}
