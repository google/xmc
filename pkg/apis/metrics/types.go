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

package metrics

import (
	"errors"
	"fmt"
	"math"

	pb "github.com/google/xmc/api/protos/metrics"
)

// Metric wraps the generated proto Metric to add validation methods.
type Metric struct {
	*pb.Metric
}

// IsValid checks whether the metric fields comply with validation rules.
func (m *Metric) IsValid() error {
	if m == nil || m.Metric == nil {
		return errors.New("metric is nil")
	}
	if !isValidMetricName(m.MetricName) {
		return fmt.Errorf("invalid metric name %q: must match regex ^[a-zA-Z0-9_\\.\\-\\/]{1,255}$", m.MetricName)
	}
	if math.IsNaN(m.MetricValue) || math.IsInf(m.MetricValue, 0) {
		return fmt.Errorf("invalid metric value %f: must be a finite number", m.MetricValue)
	}
	if m.Step < 0 {
		return fmt.Errorf("invalid step %d: must be >= 0", m.Step)
	}
	return nil
}

func isValidMetricName(name string) bool {
	if len(name) < 1 || len(name) > 255 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '.' || c == '-' || c == '/' {
			continue
		}
		return false
	}
	return true
}
