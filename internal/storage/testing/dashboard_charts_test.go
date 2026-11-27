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

package storagetest

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/storage"
)

type pagedChartStore struct{ *storage.MockStorage }

func (p *pagedChartStore) UpdateChart(_ context.Context, chart *dpb.Chart, _ *fieldmaskpb.FieldMask) (*dpb.Chart, error) {
	p.Chart = &dpb.Chart{Name: chart.GetName(), Title: chart.GetTitle(), Xid: chart.GetXid(), Wid: chart.GetWid()}
	return p.Chart, nil
}

func (p *pagedChartStore) ListCharts(_ context.Context, _ string, _ int32, tok, _, _ string, _ time.Duration) ([]*dpb.Chart, string, error) {
	if tok == "" {
		return []*dpb.Chart{{Name: "experiments/100/charts/a"}}, "tok1", nil
	}
	return []*dpb.Chart{{Name: "experiments/100/charts/b"}}, "", nil
}

func TestVerifyExperimentLevelChartRoundTrip(t *testing.T) {
	c := ExperimentChartProto("study-a", 100)
	VerifyExperimentLevelChartRoundTrip(t.Context(), t, &pagedChartStore{&storage.MockStorage{Chart: c}}, c)
}

func TestCollectChartPages(t *testing.T) {
	if diff := cmp.Diff([]string{"experiments/100/charts/a", "experiments/100/charts/b"}, CollectChartPages(t.Context(), t, &pagedChartStore{&storage.MockStorage{}}, "", 1, "", "")); diff != "" {
		t.Errorf("CollectChartPages mismatch (-want +got):\n%s", diff)
	}
}
