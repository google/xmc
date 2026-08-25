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

package envtest

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

var componentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

func TestCRDLoaders(t *testing.T) {
	t.Run("LoadComponentCRD", func(t *testing.T) {
		crd, err := LoadComponentCRD()
		if err != nil {
			t.Fatalf("LoadComponentCRD failed: %v", err)
		}
		if crd.Name != "components.xmc.dev" {
			t.Errorf("got CRD name %q, want components.xmc.dev", crd.Name)
		}
	})

	t.Run("LoadJobSetCRDs", func(t *testing.T) {
		crds, err := LoadJobSetCRDs()
		if err != nil {
			t.Fatalf("LoadJobSetCRDs failed: %v", err)
		}
		if len(crds) == 0 {
			t.Fatal("expected at least 1 JobSet CRD, got 0")
		}
		found := false
		for _, c := range crds {
			if c.Name == "jobsets.jobset.x-k8s.io" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("jobsets.jobset.x-k8s.io not found in JobSet CRDs")
		}
	})

	t.Run("LoadKueueCRDs", func(t *testing.T) {
		crds, err := LoadKueueCRDs()
		if err != nil {
			t.Fatalf("LoadKueueCRDs failed: %v", err)
		}
		if len(crds) == 0 {
			t.Fatal("expected at least 1 Kueue CRD, got 0")
		}
		expectedCRDs := map[string]bool{
			"workloads.kueue.x-k8s.io":     false,
			"clusterqueues.kueue.x-k8s.io": false,
		}
		for _, c := range crds {
			if _, ok := expectedCRDs[c.Name]; ok {
				expectedCRDs[c.Name] = true
			}
		}
		for name, found := range expectedCRDs {
			if !found {
				t.Errorf("expected Kueue CRD %q was not found", name)
			}
		}
	})
}

func TestEnvtestHarness_StartAndResources(t *testing.T) {
	testEnv := SetupTestEnv(t)
	ctx := context.Background()

	ns := testEnv.CreateNamespace(ctx, t, "envtest-harness")

	t.Run("JobSet CRD integration", func(t *testing.T) {
		js := &jobsetv1alpha2.JobSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-jobset",
				Namespace: ns,
			},
			Spec: jobsetv1alpha2.JobSetSpec{
				ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
					{
						Name:     "workers",
						Replicas: 1,
						Template: batchv1.JobTemplateSpec{
							Spec: batchv1.JobSpec{
								Template: corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{
										RestartPolicy: corev1.RestartPolicyNever,
										Containers: []corev1.Container{
											{
												Name:  "worker",
												Image: "busybox",
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

		if err := testEnv.Client.Create(ctx, js); err != nil {
			t.Fatalf("failed to create JobSet: %v", err)
		}

		var fetched jobsetv1alpha2.JobSet
		if err := testEnv.Client.Get(ctx, ctrlclient.ObjectKey{Name: "test-jobset", Namespace: ns}, &fetched); err != nil {
			t.Fatalf("failed to get JobSet: %v", err)
		}

		if fetched.Name != "test-jobset" {
			t.Errorf("got JobSet name %q, want test-jobset", fetched.Name)
		}
	})

	t.Run("Kueue ClusterQueue and Workload integration", func(t *testing.T) {
		cq := &kueuev1beta1.ClusterQueue{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-cq",
			},
			Spec: kueuev1beta1.ClusterQueueSpec{
				NamespaceSelector: &metav1.LabelSelector{},
			},
		}
		if err := testEnv.Client.Create(ctx, cq); err != nil {
			t.Fatalf("failed to create ClusterQueue: %v", err)
		}

		wl := &kueuev1beta1.Workload{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-workload",
				Namespace: ns,
			},
			Spec: kueuev1beta1.WorkloadSpec{
				QueueName: "test-cq",
				PodSets: []kueuev1beta1.PodSet{
					{
						Name:  "main",
						Count: 1,
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								RestartPolicy: corev1.RestartPolicyNever,
								Containers: []corev1.Container{
									{
										Name:  "c",
										Image: "busybox",
									},
								},
							},
						},
					},
				},
			},
		}
		if err := testEnv.Client.Create(ctx, wl); err != nil {
			t.Fatalf("failed to create Workload: %v", err)
		}
	})

	t.Run("Component 1P CRD dynamic client integration", func(t *testing.T) {
		comp := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "xmc.dev/v1alpha1",
				"kind":       "Component",
				"metadata": map[string]any{
					"name":      "test-component",
					"namespace": ns,
				},
				"spec": map[string]any{
					"enabled":            true,
					"deletionProtection": true,
					"componentType":      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
					"componentClass":     "monitoring",
				},
			},
		}

		created, err := testEnv.DynamicClient.Resource(componentGVR).Namespace(ns).Create(ctx, comp, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create Component: %v", err)
		}

		if created.GetName() != "test-component" {
			t.Errorf("got component name %q, want test-component", created.GetName())
		}
	})
}
