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

// AcceleratorLabelKeys lists the Kubernetes node label keys used to identify accelerator types.
var AcceleratorLabelKeys = []string{
	"cloud.google.com/gke-accelerator",
	"cloud.google.com/gke-gpu-type",
	"cloud.google.com/gke-tpu-accelerator",
	"kueue.x-k8s.io/gpack-gpu",
	"gpu.nvidia.com/class",
}

// LongAcceleratorKeywords contains substring keywords identifying specific accelerator models.
var LongAcceleratorKeywords = []string{
	"h100", "a100", "v100", "p100", "b200", "gb200", "h200", "b40", "rtx",
	"v5e", "v5p", "gpack", "tesla",
}

// ShortAcceleratorKeywords contains short model designations requiring word-boundary or structured matching.
var ShortAcceleratorKeywords = []string{
	"l4", "v4", "v5", "v6", "t4", "p4",
}

// GenericKeywords contains keywords indicating non-specific or generic resource flavor names.
var GenericKeywords = []string{
	"spot", "ondemand", "default", "standard", "fallback", "generic", "shared", "custom", "flavor",
}

// GenericResourceNames contains standard generic compute resource identifiers.
var GenericResourceNames = []string{
	"gpu", "nvidia.com/gpu", "tpu", "google.com/tpu", "accelerator",
	"cpu", "memory", "storage",
}
