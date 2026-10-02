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

package linter

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMergeNestedManifests(t *testing.T) {
	t.Parallel()

	component := func(name, file string) (*RawManifest, *ComponentManifest) {
		meta := ObjectMetadata{Name: name}
		return &RawManifest{APIVersion: "xmc.dev/v1alpha1", Kind: "Component", Metadata: meta, FilePath: file},
			&ComponentManifest{APIVersion: "xmc.dev/v1alpha1", Kind: "Component", Metadata: meta, FilePath: file}
	}
	topRaw, topComp := component("components/shadowed", "top")
	shadowedRaw, shadowedComp := component("shadowed", "nested")
	keptRaw, keptComp := component("kept", "nested")
	unnamedRaw, unnamedComp := component("", "nested")
	workload := &RawManifest{APIVersion: "apps/v1", Kind: "Deployment", Metadata: ObjectMetadata{Name: "shadowed"}, FilePath: "nested"}
	topUnnamedRaw, topUnnamedComp := component("", "top")

	got := mergeNestedManifests(
		manifestSet{raws: []*RawManifest{topRaw, topUnnamedRaw}, comps: []*ComponentManifest{topComp, topUnnamedComp}},
		manifestSet{
			raws:  []*RawManifest{shadowedRaw, keptRaw, unnamedRaw, workload},
			comps: []*ComponentManifest{shadowedComp, keptComp, unnamedComp},
		},
	)

	// The nested "shadowed" Component is dropped because the top-level chart renders the same
	// canonical name. Non-Component documents and unnamed Components are never shadowed.
	wantRaws := []*RawManifest{topRaw, topUnnamedRaw, keptRaw, unnamedRaw, workload}
	wantComps := []*ComponentManifest{topComp, topUnnamedComp, keptComp, unnamedComp}
	if diff := cmp.Diff(wantRaws, got.raws); diff != "" {
		t.Errorf("mergeNestedManifests() raws mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantComps, got.comps); diff != "" {
		t.Errorf("mergeNestedManifests() comps mismatch (-want +got):\n%s", diff)
	}
}
