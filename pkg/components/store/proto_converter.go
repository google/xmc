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

package store

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	componentPrefix = "components/"
	maxNameLength   = 63
)

// CanonicalComponentName strips whitespace, leading/trailing slashes, and the AIP-122
// "components/" resource prefix to return the canonical resource ID.
func CanonicalComponentName(name string) string {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "components" {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(name, componentPrefix), "/")
}

// FullComponentName formats a canonical or relative name into AIP-122
// resource name format "components/{name}". Returns "" if the input name is empty.
func FullComponentName(name string) string {
	canonical := CanonicalComponentName(name)
	if canonical == "" {
		return ""
	}
	return fmt.Sprintf("%s%s", componentPrefix, canonical)
}

// IsValidComponentName validates that a component name conforms to DNS-1123 / AIP-122
// identifier format (lowercase alphanumeric and hyphens, 1-63 chars, start/end with alphanumeric).
func IsValidComponentName(name string) bool {
	if len(name) < 1 || len(name) > maxNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		if c == '-' {
			if i == 0 || i == len(name)-1 {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// ToProto converts an internal ComponentRecord to its protobuf representation.
// Returns nil if record is nil.
func ToProto(record *ComponentRecord) *componentspb.Component {
	if record == nil {
		return nil
	}
	comp := &componentspb.Component{
		Name:       FullComponentName(record.Name),
		Etag:       record.Etag,
		Labels:     copyLabels(record.Labels),
		CreateTime: toTimestamp(record.CreateTime),
		UpdateTime: toTimestamp(record.UpdateTime),
		DeleteTime: toTimestampPtr(record.DeleteTime),
	}
	if record.Spec != nil {
		comp.Spec = proto.Clone(record.Spec).(*componentspb.ComponentSpec)
		comp.Spec.Generation = record.Generation
	}
	if record.Status != nil {
		comp.Status = proto.Clone(record.Status).(*componentspb.ComponentAccessStatus)
	}
	if comp.Status != nil || record.State != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED || record.ObservedGeneration != nil {
		if comp.Status == nil {
			comp.Status = &componentspb.ComponentAccessStatus{}
		}
		comp.Status.State = record.State
		if record.ObservedGeneration != nil {
			comp.Status.ObservedGeneration = *record.ObservedGeneration
		}
	}
	return comp
}

// FromProto converts a protobuf Component to its internal ComponentRecord representation.
// Returns nil if comp is nil.
func FromProto(comp *componentspb.Component) *ComponentRecord {
	if comp == nil {
		return nil
	}
	rec := &ComponentRecord{
		Name:       CanonicalComponentName(comp.GetName()),
		Etag:       comp.GetEtag(),
		Labels:     copyLabels(comp.GetLabels()),
		CreateTime: fromTimestamp(comp.GetCreateTime()),
		UpdateTime: fromTimestamp(comp.GetUpdateTime()),
		DeleteTime: fromTimestampPtr(comp.GetDeleteTime()),
	}
	if comp.GetSpec() != nil {
		rec.Spec = proto.Clone(comp.GetSpec()).(*componentspb.ComponentSpec)
		rec.Generation = comp.GetSpec().GetGeneration()
	}
	if comp.GetStatus() != nil {
		rec.Status = proto.Clone(comp.GetStatus()).(*componentspb.ComponentAccessStatus)
		rec.State = comp.GetStatus().GetState()
		obsGen := comp.GetStatus().GetObservedGeneration()
		rec.ObservedGeneration = &obsGen
	}
	return rec
}

// copyLabels creates an isolated shallow copy of a label map.
func copyLabels(labels map[string]string) map[string]string {
	return maps.Clone(labels)
}

func toTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func toTimestampPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func fromTimestamp(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func fromTimestampPtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
