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

// Package healthprobetest provides an in-memory test double for active network health probing.
package healthprobetest

import (
	"context"
	"errors"
	"sync"
	"time"

	"xmanager_cloud/pkg/components/bridge/healthprobe"
)

// FakeHealthProber is an in-memory test double for HealthProber.
type FakeHealthProber struct {
	mu       sync.Mutex
	Results  map[string]*healthprobe.HealthProbeResult
	ProbeErr error
}

var _ healthprobe.HealthProber = (*FakeHealthProber)(nil)

// NewFakeHealthProber creates a new FakeHealthProber.
func NewFakeHealthProber() *FakeHealthProber {
	return &FakeHealthProber{
		Results: make(map[string]*healthprobe.HealthProbeResult),
	}
}

// SetResult records the probe result for a given target.
func (f *FakeHealthProber) SetResult(target string, res *healthprobe.HealthProbeResult) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Results == nil {
		f.Results = make(map[string]*healthprobe.HealthProbeResult)
	}
	f.Results[target] = res
}

// Probe returns the preconfigured probe result for target.
func (f *FakeHealthProber) Probe(ctx context.Context, target string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	if f == nil {
		return nil, errors.New("fake health prober is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ProbeErr != nil {
		return nil, f.ProbeErr
	}
	if res, ok := f.Results[target]; ok {
		return res, nil
	}
	return &healthprobe.HealthProbeResult{Healthy: true, Message: "Probe succeeded"}, nil
}

// ProbeHTTP delegates to Probe.
func (f *FakeHealthProber) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	return f.Probe(ctx, targetURL, timeout)
}

// ProbeTCP delegates to Probe.
func (f *FakeHealthProber) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	return f.Probe(ctx, address, timeout)
}
