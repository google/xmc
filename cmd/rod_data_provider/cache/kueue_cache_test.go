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

package cache

import (
	"testing"
)

func TestKueueCacheInitialization(t *testing.T) {
	cache := NewKueueCache()
	if cache.ClusterQueues == nil {
		t.Errorf("ClusterQueues cache is nil")
	}
	if cache.LocalQueues == nil {
		t.Errorf("LocalQueues cache is nil")
	}
	if cache.ResourceFlavors == nil {
		t.Errorf("ResourceFlavors cache is nil")
	}
}
