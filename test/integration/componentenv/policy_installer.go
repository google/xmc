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
	"context"
	"fmt"
	"io"
	"os"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
)

// manifestDecodeBufferBytes is the look-ahead window the YAML decoder uses to detect JSON input.
const manifestDecodeBufferBytes = 4096

// applyPolicy creates every document in the deletion-protection policy manifest on the API server.
//
// It reads the shipped manifest verbatim rather than reconstructing the policy in Go, so a change
// to the production CEL expressions is immediately reflected in these tests.
func applyPolicy(ctx context.Context, clients kubernetes.Interface, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening policy manifest: %w", err)
	}
	defer file.Close()

	decoder := utilyaml.NewYAMLOrJSONDecoder(file, manifestDecodeBufferBytes)
	for {
		var doc unstructured.Unstructured
		err := decoder.Decode(&doc)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decoding policy manifest %q: %w", path, err)
		}
		if len(doc.Object) == 0 {
			continue
		}
		if err := createAdmissionObject(ctx, clients, &doc); err != nil {
			return err
		}
	}
}

// createAdmissionObject creates a single ValidatingAdmissionPolicy or policy binding document.
func createAdmissionObject(ctx context.Context, clients kubernetes.Interface, doc *unstructured.Unstructured) error {
	api := clients.AdmissionregistrationV1()
	switch kind := doc.GetKind(); kind {
	case "ValidatingAdmissionPolicy":
		policy := &admissionv1.ValidatingAdmissionPolicy{}
		if err := convert(doc, policy); err != nil {
			return err
		}
		if _, err := api.ValidatingAdmissionPolicies().Create(ctx, policy, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating ValidatingAdmissionPolicy %q: %w", policy.GetName(), err)
		}
		return nil
	case "ValidatingAdmissionPolicyBinding":
		binding := &admissionv1.ValidatingAdmissionPolicyBinding{}
		if err := convert(doc, binding); err != nil {
			return err
		}
		if _, err := api.ValidatingAdmissionPolicyBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating ValidatingAdmissionPolicyBinding %q: %w", binding.GetName(), err)
		}
		return nil
	default:
		return fmt.Errorf("policy manifest contains unsupported kind %q", kind)
	}
}

// convert decodes an unstructured manifest document into the supplied typed object.
func convert(doc *unstructured.Unstructured, target any) error {
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(doc.Object, target); err != nil {
		return fmt.Errorf("converting %s %q: %w", doc.GetKind(), doc.GetName(), err)
	}
	return nil
}
