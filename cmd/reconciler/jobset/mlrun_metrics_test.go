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

package jobset

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/xmc/cmd/reconciler/hypercomputeclient"
	"github.com/prometheus/client_golang/prometheus/testutil"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestMLRunResultLabel(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error is success",
			err:  nil,
			want: mlRunResultSuccess,
		},
		{
			name: "http 409 conflict is already_exists",
			err:  &hypercomputeclient.APIError{StatusCode: http.StatusConflict},
			want: mlRunResultAlreadyExists,
		},
		{
			name: "operation AlreadyExists code is already_exists",
			err:  &hypercomputeclient.OperationError{Code: hypercomputeclient.CodeAlreadyExists},
			want: mlRunResultAlreadyExists,
		},
		{
			name: "wrapped 409 is still already_exists",
			err:  fmtWrap(&hypercomputeclient.APIError{StatusCode: http.StatusConflict}),
			want: mlRunResultAlreadyExists,
		},
		{
			name: "non-conflict api error is error",
			err:  &hypercomputeclient.APIError{StatusCode: http.StatusInternalServerError},
			want: mlRunResultError,
		},
		{
			name: "generic error is error",
			err:  errors.New("boom"),
			want: mlRunResultError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mlRunResultLabel(tc.err); got != tc.want {
				t.Errorf("mlRunResultLabel(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// fmtWrap wraps err so tests exercise the errors.As unwrapping path used by
// isMLRunAlreadyExistsError.
func fmtWrap(err error) error {
	return errWrapper{err}
}

type errWrapper struct{ err error }

func (w errWrapper) Error() string { return "wrapped: " + w.err.Error() }
func (w errWrapper) Unwrap() error { return w.err }

// fakeMLRunCreator is a configurable MLRunCreator used to verify the decorator
// delegates every call and records exactly one metric sample per call.
type fakeMLRunCreator struct {
	err                                error
	createCalls, getCalls, updateCalls int
}

func (f *fakeMLRunCreator) CreateMLRun(_ context.Context, _, _, _ string, _ *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	f.createCalls++
	return map[string]any{"op": "create"}, f.err
}

func (f *fakeMLRunCreator) GetMLRun(_ context.Context, _ string) (map[string]any, error) {
	f.getCalls++
	return map[string]any{"op": "get"}, f.err
}

func (f *fakeMLRunCreator) UpdateMLRun(_ context.Context, _ string, _ *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	f.updateCalls++
	return map[string]any{"op": "update"}, f.err
}

func TestNewInstrumentedMLRunCreatorNilPassthrough(t *testing.T) {
	if got := NewInstrumentedMLRunCreator(nil); got != nil {
		t.Errorf("NewInstrumentedMLRunCreator(nil) = %v, want nil so the reconciler nil-guard is preserved", got)
	}
}

func TestInstrumentedMLRunCreatorDelegatesAndRecords(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		err        error
		wantResult string
	}{
		{name: "success path", err: nil, wantResult: mlRunResultSuccess},
		{name: "error path", err: errors.New("boom"), wantResult: mlRunResultError},
		{name: "already exists path", err: &hypercomputeclient.APIError{StatusCode: http.StatusConflict}, wantResult: mlRunResultAlreadyExists},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeMLRunCreator{err: tc.err}
			dec := NewInstrumentedMLRunCreator(fake)

			for _, op := range []string{mlRunOpCreate, mlRunOpGet, mlRunOpUpdate} {
				before := testutil.ToFloat64(mlRunRequestsTotal.WithLabelValues(op, tc.wantResult))

				switch op {
				case mlRunOpCreate:
					if _, err := dec.CreateMLRun(ctx, "n", "d", "ACTIVE", nil); !errors.Is(err, tc.err) {
						t.Fatalf("CreateMLRun err = %v, want %v", err, tc.err)
					}
				case mlRunOpGet:
					if _, err := dec.GetMLRun(ctx, "n"); !errors.Is(err, tc.err) {
						t.Fatalf("GetMLRun err = %v, want %v", err, tc.err)
					}
				case mlRunOpUpdate:
					if _, err := dec.UpdateMLRun(ctx, "n", nil); !errors.Is(err, tc.err) {
						t.Fatalf("UpdateMLRun err = %v, want %v", err, tc.err)
					}
				}

				after := testutil.ToFloat64(mlRunRequestsTotal.WithLabelValues(op, tc.wantResult))
				if after-before != 1 {
					t.Errorf("op %s result %s: counter delta = %v, want 1", op, tc.wantResult, after-before)
				}
			}

			if fake.createCalls != 1 || fake.getCalls != 1 || fake.updateCalls != 1 {
				t.Errorf("delegation mismatch: create=%d get=%d update=%d, want 1 each", fake.createCalls, fake.getCalls, fake.updateCalls)
			}
		})
	}
	assertMLRunMetricsOnManagerRegistry(t)
}

func assertMLRunMetricsOnManagerRegistry(t *testing.T) {
	t.Helper()
	mfs, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("ctrlmetrics.Registry.Gather() failed: %v", err)
	}
	registered := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		registered[mf.GetName()] = true
	}
	for _, name := range []string{"jobset_reconciler_mlrun_requests_total", "jobset_reconciler_mlrun_request_duration_seconds"} {
		if !registered[name] {
			t.Errorf("metric family %q not registered on ctrlmetrics.Registry", name)
		}
	}
}
