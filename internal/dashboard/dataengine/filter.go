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

package dataengine

import (
	"slices"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/pkg/metricsquery"
)

// metricFilter translates a chart's experiment, work unit, and plot data spec into a
// metricsquery.MetricFilter. A wid of zero or less selects every work unit. The filter does not
// alias the proto message.
func metricFilter(xid, wid int64, dataSpec *dpb.DataSpec) metricsquery.MetricFilter {
	f := metricsquery.MetricFilter{
		XID:         xid,
		WorkUnitID:  wid,
		MetricNames: slices.Clone(dataSpec.GetMetrics()),
	}
	for _, r := range dataSpec.GetRanges() {
		if r == nil {
			continue
		}
		f.Ranges = append(f.Ranges, metricRange(r))
	}
	return f
}

// metricRange translates a data range, dropping invalid timestamp bounds. Bounds are copied so the
// filter does not alias the proto message.
func metricRange(r *dpb.DataSpec_DataRange) metricsquery.Range {
	var out metricsquery.Range
	switch s := r.GetStart().(type) {
	case *dpb.DataSpec_DataRange_StartStep:
		step := s.StartStep
		out.StartStep = &step
	case *dpb.DataSpec_DataRange_StartTime:
		if s.StartTime.IsValid() {
			t := s.StartTime.AsTime()
			out.StartTime = &t
		}
	}
	switch e := r.GetEnd().(type) {
	case *dpb.DataSpec_DataRange_EndStep:
		step := e.EndStep
		out.EndStep = &step
	case *dpb.DataSpec_DataRange_EndTime:
		if e.EndTime.IsValid() {
			t := e.EndTime.AsTime()
			out.EndTime = &t
		}
	}
	return out
}
