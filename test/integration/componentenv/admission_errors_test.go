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

package componentenv

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newStatusError builds an API status error with the given code, reason and message.
func newStatusError(code int32, reason metav1.StatusReason, message string) error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    code,
		Reason:  reason,
		Message: message,
	}}
}

func TestIsDeniedByPolicy(t *testing.T) {
	// The API server reports ValidatingAdmissionPolicy denials with 422/Invalid and phrases the
	// message as "is forbidden", which is why neither the code nor the reason alone identifies
	// them. These cases lock in that discrimination.
	const denialMessage = `components.xmc.dev "c" is forbidden: ValidatingAdmissionPolicy ` +
		`'component-deletion-protection' with binding 'component-deletion-protection-binding' ` +
		`denied request: Deletion rejected: Component has deletionProtection enabled`
	const celMessage = `Component.xmc.dev "c" is invalid: spec.deletionPolicy: Unsupported value: "Retain"`

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "policy denial is recognized",
			err:  newStatusError(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, denialMessage),
			want: true,
		},
		{
			name: "wrapped policy denial is recognized",
			err:  fmt.Errorf("deleting component: %w", newStatusError(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, denialMessage)),
			want: true,
		},
		{
			name: "CEL schema rejection is not a policy denial",
			err:  newStatusError(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, celMessage),
			want: false,
		},
		{
			name: "RBAC forbidden error is not a policy denial",
			err:  newStatusError(http.StatusForbidden, metav1.StatusReasonForbidden, `components.xmc.dev "c" is forbidden: User cannot delete`),
			want: false,
		},
		{
			name: "not found error is not a policy denial",
			err:  newStatusError(http.StatusNotFound, metav1.StatusReasonNotFound, `components.xmc.dev "c" not found`),
			want: false,
		},
		{
			name: "plain error is not a policy denial",
			err:  errors.New("connection refused"),
			want: false,
		},
		{
			name: "nil error is not a policy denial",
			err:  nil,
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsDeniedByPolicy(test.err); got != test.want {
				t.Errorf("IsDeniedByPolicy(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}
