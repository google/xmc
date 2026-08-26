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
	"strings"

	"xmanager_cloud/kubernetes/common/constants"
)

var (
	gpuKeywords      = append([]string{"nvidia-", "tesla-"}, constants.LongAcceleratorKeywords...)
	shortGPUKeywords = constants.ShortAcceleratorKeywords

	tpuKeywords = []string{
		"v5p", "v5e", "tpu-v4", "tpu-v5", "tpu-v6", "tpu-",
	}
)

// IsSpecificAcceleratorName returns true if name describes a specific GPU or TPU accelerator model.
func IsSpecificAcceleratorName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	if IsGenericAcceleratorFlavor(lower) {
		return false
	}
	if IsSpecificGPUKey(lower) || IsSpecificTPUKey(lower) {
		return true
	}
	for _, kw := range constants.LongAcceleratorKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// IsGenericAcceleratorFlavor returns true if lower matches generic compute names or flavor keywords.
func IsGenericAcceleratorFlavor(lower string) bool {
	for _, exact := range constants.GenericResourceNames {
		if lower == exact {
			return true
		}
	}
	for _, kw := range constants.GenericKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// IsSpecificGPUKey returns true if key identifies a specific GPU accelerator model.
func IsSpecificGPUKey(key string) bool {
	if IsGenericAcceleratorFlavor(key) || IsSpecificTPUKey(key) {
		return false
	}
	for _, kw := range gpuKeywords {
		if strings.Contains(key, kw) {
			return true
		}
	}
	for _, kw := range shortGPUKeywords {
		if isStructuredShortGPU(key, kw) {
			return true
		}
	}
	return false
}

func isStructuredShortGPU(key, kw string) bool {
	if key == kw || strings.HasPrefix(key, kw+"-") {
		return true
	}
	return MatchesShortAcceleratorKeyword(key, kw) && (strings.Contains(key, "nvidia") || strings.Contains(key, "gpu"))
}

// IsSpecificTPUKey returns true if key identifies a specific TPU accelerator model.
func IsSpecificTPUKey(key string) bool {
	if IsGenericAcceleratorFlavor(key) {
		return false
	}
	for _, kw := range tpuKeywords {
		if strings.Contains(key, kw) {
			return true
		}
	}
	tpuPrefixes := []string{"v4-", "v5-", "v6-"}
	for _, pfx := range tpuPrefixes {
		if strings.HasPrefix(key, pfx) {
			return true
		}
	}
	return key == "v4" || key == "v5" || key == "v6"
}

// MatchesShortAcceleratorKeyword checks if kw occurs in s bounded by non-alphanumeric delimiters.
func MatchesShortAcceleratorKeyword(s, kw string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], kw)
		if i == -1 {
			return false
		}
		pos := idx + i
		prevOK := (pos == 0) || !isASCIILetter(s[pos-1])
		nextPos := pos + len(kw)
		nextOK := (nextPos == len(s)) || !isASCIIAlphanumeric(s[nextPos])
		if prevOK && nextOK {
			return true
		}
		idx = pos + 1
	}
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isASCIIAlphanumeric(c byte) bool {
	return isASCIILetter(c) || (c >= '0' && c <= '9')
}

// IsAcceleratorResource returns true if resName represents an accelerator resource type.
func IsAcceleratorResource(resName string) bool {
	lower := strings.ToLower(resName)
	return strings.Contains(lower, "gpu") || strings.Contains(lower, "tpu") || strings.Contains(lower, "accelerator")
}

// IsMemoryOrStorageResource returns true if resourceType represents memory or disk storage.
func IsMemoryOrStorageResource(resourceType string) bool {
	lower := strings.ToLower(resourceType)
	return strings.Contains(lower, "memory") || strings.Contains(lower, "storage")
}

// IsGenericGPU returns true if resType is a generic GPU identifier.
func IsGenericGPU(resType string) bool {
	lower := strings.ToLower(resType)
	return lower == "gpu" || lower == "nvidia.com/gpu"
}

// IsGenericTPU returns true if resType is a generic TPU identifier.
func IsGenericTPU(resType string) bool {
	lower := strings.ToLower(resType)
	return lower == "tpu" || lower == "google.com/tpu"
}
