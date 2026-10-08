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

package k8sclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// componentSnapshot is one entry of the component-snapshots annotation. The entry is either a bare
// locked CMS generation (N) or an object {"generation": N, "componentId": "<id>"} that also locks
// the incarnation of the component.
type componentSnapshot struct {
	// Generation is the locked CMS component generation; a value <= 0 locks no generation.
	Generation int64
	// ComponentID is the locked CMS ComponentId in canonical decimal form, or "" when the entry
	// does not lock an incarnation.
	ComponentID string
}

// UnmarshalJSON accepts a bare generation or a {"generation", "componentId"} object. componentId
// may be a JSON string (preferred: IDs exceed the integer precision of JSON tooling) or an integer.
// Unknown object fields are rejected so that a misspelled key does not silently lock nothing.
func (s *componentSnapshot) UnmarshalJSON(data []byte) error {
	if data = bytes.TrimSpace(data); len(data) > 0 && data[0] != '{' {
		return json.Unmarshal(data, &s.Generation)
	}
	var entry struct {
		Generation  int64           `json:"generation"`
		ComponentID json.RawMessage `json:"componentId"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entry); err != nil {
		return err
	}
	id, err := parseSnapshotComponentID(entry.ComponentID)
	if err != nil {
		return err
	}
	*s = componentSnapshot{Generation: entry.Generation, ComponentID: id}
	return nil
}

// parseSnapshotComponentID returns the canonical decimal form of a componentId given as a JSON
// string or integer, or "" when it is absent, null or the empty string.
func parseSnapshotComponentID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		text = string(raw)
	} else if text == "" {
		return "", nil
	}
	id, ok := canonicalComponentID(text)
	if !ok {
		return "", fmt.Errorf("componentId %s is not a positive integer", raw)
	}
	return id, nil
}

// canonicalComponentID returns text as a canonical decimal ComponentId, or false when it is not a
// positive 64-bit integer.
func canonicalComponentID(text string) (string, bool) {
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id <= 0 {
		return "", false
	}
	return strconv.FormatInt(id, 10), true
}

// extractComponentID returns status.componentId of the Component in canonical decimal form, or ""
// when it is absent or invalid. The CRD types it as a string; integers are tolerated. Floats are
// rejected because they cannot represent every ComponentId exactly.
func extractComponentID(comp *unstructured.Unstructured) string {
	if comp == nil || comp.Object == nil {
		return ""
	}
	val, ok, err := unstructured.NestedFieldNoCopy(comp.Object, "status", "componentId")
	if err != nil || !ok {
		return ""
	}
	var text string
	switch val := val.(type) {
	case string:
		text = val
	case int64:
		text = strconv.FormatInt(val, 10)
	case json.Number:
		text = val.String()
	}
	id, _ := canonicalComponentID(text)
	return id
}
