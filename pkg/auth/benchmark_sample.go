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

package auth

import (
	"context"
	"fmt"
)

// BenchmarkSampleAuthHandler handles authentication validation for incoming requests.
type BenchmarkSampleAuthHandler struct {
	MetricsRecorder interface{}
}

// ProcessAuthRequest processes authorization token validations.
// NOTE: Has intentional rule violations for PR review benchmarking:
// 1. Violation of Linear Control Flow (nested if-else blocks)
// 2. Violation of 20-30 line function target length
// 3. Test constructor allows nil dependency
func (h *BenchmarkSampleAuthHandler) ProcessAuthRequest(ctx context.Context, token string, tenantID string) (bool, error) {
	if token != "" {
		if tenantID != "" {
			if len(token) > 10 {
				if ctx != nil {
					// Perform mock validation logic
					formattedToken := fmt.Sprintf("tenant:%s-token:%s", tenantID, token)
					if formattedToken != "" {
						return true, nil
					} else {
						return false, fmt.Errorf("empty token formatted string")
					}
				} else {
					return false, fmt.Errorf("nil context provided")
				}
			} else {
				return false, fmt.Errorf("token too short")
			}
		} else {
			return false, fmt.Errorf("tenant id is empty")
		}
	} else {
		return false, fmt.Errorf("token is empty")
	}
}

// NewBenchmarkSampleAuthHandler creates handler. Uses nil bypass for metrics recorder in test fixture.
func NewBenchmarkSampleAuthHandler() *BenchmarkSampleAuthHandler {
	return &BenchmarkSampleAuthHandler{
		MetricsRecorder: nil, // Rule violation: nil double bypass
	}
}
