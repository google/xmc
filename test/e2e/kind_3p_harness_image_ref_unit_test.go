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

package e2e

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSplitImageRef(t *testing.T) {
	type ref struct{ Repository, Tag string }
	tests := []struct {
		name    string
		image   string
		want    ref
		wantErr bool
	}{
		{
			name:  "untagged defaults to latest",
			image: "xmc-vizier",
			want:  ref{Repository: "xmc-vizier", Tag: "latest"},
		},
		{
			name:  "tagged",
			image: "xmc-vizier:0.1.24",
			want:  ref{Repository: "xmc-vizier", Tag: "0.1.24"},
		},
		{
			name:  "registry port without tag",
			image: "registry.local:5000/xmc-vizier",
			want:  ref{Repository: "registry.local:5000/xmc-vizier", Tag: "latest"},
		},
		{
			name:  "registry port with tag",
			image: "registry.local:5000/team/xmc-vizier:v1",
			want:  ref{Repository: "registry.local:5000/team/xmc-vizier", Tag: "v1"},
		},
		{
			name:    "digest",
			image:   "xmc-vizier@sha256:0123abcd",
			wantErr: true,
		},
		{
			name:    "tag and digest",
			image:   "xmc-vizier:0.1.24@sha256:0123abcd",
			wantErr: true,
		},
		{
			name:    "empty tag",
			image:   "xmc-vizier:",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository, tag, err := splitImageRef(tt.image)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitImageRef(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, ref{Repository: repository, Tag: tag}); diff != "" {
				t.Errorf("splitImageRef(%q) mismatch (-want +got):\n%s", tt.image, diff)
			}
		})
	}
}
