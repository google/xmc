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

package componentenv

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// deniedByPolicyErr builds the error an API server returns when a ValidatingAdmissionPolicy
// rejects a request: HTTP 422 with reason Invalid, carrying the policy marker in the message.
func deniedByPolicyErr() error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Code:    422,
		Reason:  metav1.StatusReasonInvalid,
		Message: `components.xmc.dev "probe" is forbidden: ValidatingAdmissionPolicy 'xmc-component-deletion-protection' denied request`,
	}}
}

func TestPollUntilPolicyEnforced(t *testing.T) {
	tests := []struct {
		name string
		// deleteResults are returned by deleteAsSubject on successive attempts. A nil entry means
		// the deletion succeeded, i.e. the policy was not yet enforced.
		deleteResults []error
		wantErr       bool
		wantAttempts  int
	}{
		{
			name:          "denied on the first attempt returns immediately",
			deleteResults: []error{deniedByPolicyErr()},
			wantAttempts:  1,
		},
		{
			// This is the regression guard. The policy informer had not caught up yet, so the
			// first two deletions actually succeeded and destroyed the probe. An earlier version
			// treated that as a fatal error and gave up on attempt 1.
			name:          "retries while the policy is not yet enforced",
			deleteResults: []error{nil, nil, deniedByPolicyErr()},
			wantAttempts:  3,
		},
		{
			name:          "unexpected deletion error is fatal",
			deleteResults: []error{errors.New("connection refused")},
			wantErr:       true,
			wantAttempts:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			adminDeletes := 0
			ops := probeOps{
				create: func(context.Context) error { return nil },
				deleteAsSubject: func(context.Context) error {
					attempts++
					return tt.deleteResults[attempts-1]
				},
				deleteAsAdmin: func(context.Context) error {
					adminDeletes++
					return nil
				},
			}

			err := pollUntilPolicyEnforced(context.Background(), ops, 10*time.Second)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("pollUntilPolicyEnforced() error = %v, wantErr %v", err, tt.wantErr)
			}
			if attempts != tt.wantAttempts {
				t.Errorf("deleteAsSubject called %d times, want %d", attempts, tt.wantAttempts)
			}
			// The probe must be cleaned up exactly when the policy is confirmed enforced.
			wantAdminDeletes := 0
			if !tt.wantErr {
				wantAdminDeletes = 1
			}
			if adminDeletes != wantAdminDeletes {
				t.Errorf("deleteAsAdmin called %d times, want %d", adminDeletes, wantAdminDeletes)
			}
		})
	}
}

func TestPollUntilPolicyEnforcedTimesOut(t *testing.T) {
	attempts := 0
	ops := probeOps{
		create: func(context.Context) error { return nil },
		// The policy never becomes enforced, so every deletion succeeds.
		deleteAsSubject: func(context.Context) error { attempts++; return nil },
		deleteAsAdmin:   func(context.Context) error { return nil },
	}

	err := pollUntilPolicyEnforced(context.Background(), ops, 200*time.Millisecond)
	if err == nil {
		t.Fatal("pollUntilPolicyEnforced() = nil, want a timeout error")
	}
	// Proves the loop actually polled rather than bailing out on the first success.
	if attempts < 2 {
		t.Errorf("deleteAsSubject called %d times, want the loop to retry at least twice", attempts)
	}
}

func TestPollUntilPolicyEnforcedHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ops := probeOps{
		create: func(context.Context) error { return nil },
		deleteAsSubject: func(context.Context) error {
			cancel()
			return nil
		},
		deleteAsAdmin: func(context.Context) error { return nil },
	}

	if err := pollUntilPolicyEnforced(ctx, ops, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("pollUntilPolicyEnforced() error = %v, want context.Canceled", err)
	}
}
