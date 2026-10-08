// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prototest_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"xmanager_cloud/internal/prototest"
)

func TestModify_ClonesAndMutatesWithoutAffectingBase(t *testing.T) {
	base := &fieldmaskpb.FieldMask{Paths: []string{"name"}}

	modified := prototest.Modify(base, func(m *fieldmaskpb.FieldMask) {
		m.Paths = append(m.Paths, "display_name")
	})

	if len(base.GetPaths()) != 1 || base.GetPaths()[0] != "name" {
		t.Fatalf("base was mutated: got %v, want [name]", base.GetPaths())
	}
	want := &fieldmaskpb.FieldMask{Paths: []string{"name", "display_name"}}
	if !proto.Equal(modified, want) {
		t.Errorf("Modify() = %v, want %v", modified, want)
	}

	t.Run("nil base", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("Modify(nil) did not panic, want panic")
			}
		}()
		var nilBase *fieldmaskpb.FieldMask
		prototest.Modify(nilBase, func(m *fieldmaskpb.FieldMask) {
			m.Paths = []string{"test"}
		})
	})

	t.Run("nil fn", func(t *testing.T) {
		res := prototest.Modify(base, nil)
		if !proto.Equal(res, base) {
			t.Errorf("Modify(base, nil) = %v, want %v", res, base)
		}
	})
}
