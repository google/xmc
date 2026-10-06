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

package bridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// probeCall records the arguments of a single endpointProber.Probe invocation.
type probeCall struct {
	Target  string
	Timeout time.Duration
}

// recordingEndpointProber is an endpointProber that returns a canned outcome and records its calls.
type recordingEndpointProber struct {
	res   *HealthProbeResult
	err   error
	calls []probeCall
}

func (p *recordingEndpointProber) Probe(_ context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	p.calls = append(p.calls, probeCall{Target: target, Timeout: timeout})
	return p.res, p.err
}

func TestProbeEndpointHealth(t *testing.T) {
	const (
		endpoint = "http://svc.xmc:8080"
		timeout  = 3 * time.Second
	)
	errDial := errors.New("dial tcp: connection refused")
	probed := []probeCall{{Target: endpoint, Timeout: timeout}}

	tests := []struct {
		name      string
		nilProber bool
		endpoint  string
		probeRes  *HealthProbeResult
		probeErr  error
		want      *HealthProbeResult
		wantErr   error
		wantCalls []probeCall
	}{
		{name: "empty endpoint skips probe", want: &HealthProbeResult{Healthy: true}},
		{name: "nil prober skips probe", nilProber: true, endpoint: endpoint, want: &HealthProbeResult{Healthy: true}},
		{name: "probe error is returned", endpoint: endpoint, probeErr: errDial, wantErr: errDial, wantCalls: probed},
		{name: "nil result is reported as error", endpoint: endpoint, wantErr: errNilProbeResult, wantCalls: probed},
		{
			name:      "unhealthy result is returned",
			endpoint:  endpoint,
			probeRes:  &HealthProbeResult{StatusCode: 503, Message: "HTTP 503 (unhealthy status)"},
			want:      &HealthProbeResult{StatusCode: 503, Message: "HTTP 503 (unhealthy status)"},
			wantCalls: probed,
		},
		{
			name:      "healthy result is returned",
			endpoint:  endpoint,
			probeRes:  &HealthProbeResult{Healthy: true, StatusCode: 200, Message: "HTTP 200"},
			want:      &HealthProbeResult{Healthy: true, StatusCode: 200, Message: "HTTP 200"},
			wantCalls: probed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &recordingEndpointProber{res: tt.probeRes, err: tt.probeErr}
			var prober endpointProber = recorder
			if tt.nilProber {
				prober = nil
			}
			got, err := probeEndpointHealth(context.Background(), prober, tt.endpoint, timeout)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("probeEndpointHealth() error = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("probeEndpointHealth() result mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, recorder.calls); diff != "" {
				t.Errorf("probeEndpointHealth() Probe calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
