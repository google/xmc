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

package resourceaccumulator

import (
	"sync"
	"testing"

	"golang/cmp/cmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestAccumulator(t *testing.T) {
	t.Run("empty accumulator order returns non-nil empty slice", func(t *testing.T) {
		acc := New()
		order := acc.Order()
		if order == nil {
			t.Errorf("Order() returned nil, want non-nil empty slice []string{}")
		}
		if len(order) != 0 {
			t.Errorf("Order() len = %d, want 0", len(order))
		}
	})

	t.Run("accumulate CPU and GPU resource set", func(t *testing.T) {
		acc := New()
		acc.AccumulateResourceSet(&essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_CpuResources{
				CpuResources: &essmsgpb.CpuResources{
					CpuMillicores: 4000,
					MemoryBytes:   8192,
				},
			},
		})
		acc.AccumulateResourceSet(&essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_GpuResources{
				GpuResources: &essmsgpb.GpuResources{
					Type:  "nvidia-tesla-h100",
					Count: 8,
				},
			},
		})

		totals := acc.Totals()
		wantTotals := map[string]float64{
			"cpu":               4.0,
			"memory":            8192.0,
			"nvidia-tesla-h100": 8.0,
		}
		if diff := cmp.Diff(wantTotals, totals); diff != "" {
			t.Errorf("Totals() diff (-want +got):\n%s", diff)
		}

		intTotals := acc.IntTotals()
		wantIntTotals := map[string]int64{
			"cpu":               4000,
			"memory":            8192,
			"nvidia-tesla-h100": 8,
		}
		if diff := cmp.Diff(wantIntTotals, intTotals); diff != "" {
			t.Errorf("IntTotals() diff (-want +got):\n%s", diff)
		}

		order := acc.Order()
		wantOrder := []string{"cpu", "memory", "nvidia-tesla-h100"}
		if diff := cmp.Diff(wantOrder, order); diff != "" {
			t.Errorf("Order() diff (-want +got):\n%s", diff)
		}
	})

	t.Run("accumulate TPU topology resource set", func(t *testing.T) {
		acc := New()
		acc.AccumulateResourceSet(&essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_TpuResources{
				TpuResources: &essmsgpb.TpuResources{
					Type:     "v5p-8",
					Topology: "2x2x2",
				},
			},
		})

		intTotals := acc.IntTotals()
		wantIntTotals := map[string]int64{
			"v5p-8": 8,
		}
		if diff := cmp.Diff(wantIntTotals, intTotals); diff != "" {
			t.Errorf("IntTotals() diff (-want +got):\n%s", diff)
		}
	})

	t.Run("case insensitive resType matching", func(t *testing.T) {
		acc := New()
		acc.Add("cpu", 2.0)
		acc.Add("CPU", 3.0)

		totals := acc.Totals()
		wantTotals := map[string]float64{
			"cpu": 5.0,
		}
		if diff := cmp.Diff(wantTotals, totals); diff != "" {
			t.Errorf("Totals() diff (-want +got):\n%s", diff)
		}
	})

	t.Run("zero-value initialization safe", func(t *testing.T) {
		var acc Accumulator
		acc.Add("cpu", 2.0)
		totals := acc.Totals()
		wantTotals := map[string]float64{
			"cpu": 2.0,
		}
		if diff := cmp.Diff(wantTotals, totals); diff != "" {
			t.Errorf("Totals() diff (-want +got):\n%s", diff)
		}
	})
}

func TestAccumulateExecutable(t *testing.T) {
	t.Run("nil executable ignored", func(t *testing.T) {
		acc := New()
		acc.AccumulateExecutable(nil)
		if len(acc.Totals()) != 0 {
			t.Errorf("Totals() len = %d, want 0", len(acc.Totals()))
		}
	})

	t.Run("accumulate executable with sidecars and jobset", func(t *testing.T) {
		acc := New()
		exec := &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Replicas: 2,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 4000,
											MemoryBytes:   8192,
										},
									},
								},
							},
							Sidecars: []*essmsgpb.ExecutableSpec{
								{
									Resources: &essmsgpb.ResourceSet{
										Resources: &essmsgpb.ResourceSet_GpuResources{
											GpuResources: &essmsgpb.GpuResources{
												Type:  "nvidia-tesla-h100",
												Count: 2,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		acc.AccumulateExecutable(exec)
		totals := acc.Totals()
		wantTotals := map[string]float64{
			"cpu":               8.0,
			"memory":            16384.0,
			"nvidia-tesla-h100": 4.0,
		}
		if diff := cmp.Diff(wantTotals, totals); diff != "" {
			t.Errorf("AccumulateExecutable Totals() diff (-want +got):\n%s", diff)
		}
	})
}

func TestConcurrentGetters(t *testing.T) {
	acc := New()
	acc.Add("cpu", 4.0)
	acc.Add("nvidia-tesla-h100", 8.0)
	acc.Add("nvidia.com/gpu", 2.0)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = acc.Totals()
		}()
		go func() {
			defer wg.Done()
			_ = acc.IntTotals()
		}()
		go func() {
			defer wg.Done()
			_ = acc.Order()
		}()
	}
	wg.Wait()
}

func TestNilAccumulatorReceiverSafety(t *testing.T) {
	var acc *Accumulator

	t.Run("Totals on nil accumulator", func(t *testing.T) {
		totals := acc.Totals()
		if totals == nil {
			t.Errorf("acc.Totals() on nil returned nil, want non-nil empty map")
		}
		if len(totals) != 0 {
			t.Errorf("acc.Totals() len = %d, want 0", len(totals))
		}
	})

	t.Run("IntTotals on nil accumulator", func(t *testing.T) {
		intTotals := acc.IntTotals()
		if intTotals == nil {
			t.Errorf("acc.IntTotals() on nil returned nil, want non-nil empty map")
		}
		if len(intTotals) != 0 {
			t.Errorf("acc.IntTotals() len = %d, want 0", len(intTotals))
		}
	})

	t.Run("Order on nil accumulator", func(t *testing.T) {
		order := acc.Order()
		if order == nil {
			t.Errorf("acc.Order() on nil returned nil, want non-nil empty slice")
		}
		if len(order) != 0 {
			t.Errorf("acc.Order() len = %d, want 0", len(order))
		}
	})

	t.Run("mutator methods on nil accumulator do not panic", func(t *testing.T) {
		acc.Add("cpu", 1.0)
		acc.AccumulateResourceSet(nil)
		acc.AccumulateExecutable(nil)
	})
}
