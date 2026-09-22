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

package graph

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDetectCycles(t *testing.T) {
	tests := []struct {
		name      string
		graph     DependencyGraph
		wantCycle bool
		wantPath  []string
	}{
		{
			name:      "EmptyGraph",
			graph:     DependencyGraph{},
			wantCycle: false,
		},
		{
			name: "DiamondDAG_NoCycles",
			graph: DependencyGraph{
				"a": {"b", "c"},
				"b": {"d"},
				"c": {"d"},
				"d": {},
			},
			wantCycle: false,
		},
		{
			name: "SelfCycle",
			graph: DependencyGraph{
				"a": {"a"},
			},
			wantCycle: true,
			wantPath:  []string{"a", "a"},
		},
		{
			name: "TwoNodeCycle",
			graph: DependencyGraph{
				"a": {"b"},
				"b": {"a"},
			},
			wantCycle: true,
			wantPath:  []string{"a", "b", "a"},
		},
		{
			name: "ThreeNodeCycle",
			graph: DependencyGraph{
				"a": {"c", "b"},
				"b": {"c"},
				"c": {"a"},
			},
			wantCycle: true,
			wantPath:  []string{"a", "b", "c", "a"},
		},
		{
			name: "DisconnectedSubgraphCycle",
			graph: DependencyGraph{
				"ok1": {"ok2"},
				"ok2": {},
				"x":   {"y"},
				"y":   {"x"},
			},
			wantCycle: true,
			wantPath:  []string{"x", "y", "x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotCycle := DetectCycles(tc.graph)
			if gotCycle != tc.wantCycle {
				t.Fatalf("DetectCycles() cycle = %v, want %v (path: %v)", gotCycle, tc.wantCycle, gotPath)
			}
			if diff := cmp.Diff(tc.wantPath, gotPath); diff != "" {
				t.Errorf("DetectCycles() path mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDetectAllCycles(t *testing.T) {
	tests := []struct {
		name          string
		nodes         []string
		graph         DependencyGraph
		isValidTarget func(string) bool
		wantFindings  []CycleFinding
	}{
		{
			name:  "NoCycles",
			nodes: []string{"a", "b"},
			graph: DependencyGraph{
				"a": {"b"},
				"b": {},
			},
			wantFindings: nil,
		},
		{
			name:  "MultipleOverlappingCycles",
			nodes: []string{"c1", "c2", "c3"},
			graph: DependencyGraph{
				"c1": {"c2", "c3"},
				"c2": {"c1", "c3"},
				"c3": {"c1"},
			},
			wantFindings: []CycleFinding{
				{SourceNode: "c2", CyclePath: []string{"c1", "c2", "c1"}},
				{SourceNode: "c3", CyclePath: []string{"c1", "c2", "c3", "c1"}},
			},
		},
		{
			name:  "SkipsInvalidTarget",
			nodes: []string{"a", "b"},
			graph: DependencyGraph{
				"a": {"missing", "b"},
				"b": {"a"},
			},
			isValidTarget: func(s string) bool { return s != "missing" },
			wantFindings: []CycleFinding{
				{SourceNode: "b", CyclePath: []string{"a", "b", "a"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectAllCycles(tc.nodes, tc.graph, tc.isValidTarget)
			if diff := cmp.Diff(tc.wantFindings, got); diff != "" {
				t.Errorf("DetectAllCycles() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFormatCycle(t *testing.T) {
	got := FormatCycle([]string{"a", "b", "a"})
	want := "a -> b -> a"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FormatCycle() mismatch (-want +got):\n%s", diff)
	}
}

func TestFindTransitiveDependents(t *testing.T) {
	dependentsGraph := DependencyGraph{
		"c": {"b", "d"},
		"b": {"a"},
		"d": {"a"},
		"a": nil,
	}

	tests := []struct {
		name string
		root string
		want []string
	}{
		{
			name: "EmptyRoot",
			root: "",
			want: nil,
		},
		{
			name: "LeafNodeNoDependents",
			root: "a",
			want: nil,
		},
		{
			name: "TransitiveDeduplicatedAndSorted",
			root: "c",
			want: []string{"a", "b", "d"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindTransitiveDependents(dependentsGraph, tc.root)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("FindTransitiveDependents(%q) mismatch (-want +got):\n%s", tc.root, diff)
			}
		})
	}
}
