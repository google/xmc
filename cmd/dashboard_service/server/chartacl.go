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

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "github.com/google/xmc/api/protos/dashboard"
	"github.com/google/xmc/internal/logging"
)

// filterAuthorizedCharts drops the charts whose experiment the caller cannot read. A dashboard may
// reference charts from several experiments, so its chart set has to be pruned per chart rather
// than authorized as a whole. Each distinct xid is checked once, which bounds the XID service round
// trips by the number of experiments in the result rather than the number of charts.
func (s *Server) filterAuthorizedCharts(ctx context.Context, user string, charts []*dpb.Chart) ([]*dpb.Chart, error) {
	authorized := make([]*dpb.Chart, 0, len(charts))
	if len(charts) == 0 {
		return authorized, nil
	}
	if s.xidClient == nil {
		return nil, status.Error(codes.FailedPrecondition, "xid client is not configured")
	}

	readable := map[int64]bool{}
	for _, chart := range charts {
		allowed, checked := readable[chart.GetXid()]
		if !checked {
			var err error
			if allowed, err = s.isChartReadable(ctx, user, chart); err != nil {
				return nil, err
			}
			readable[chart.GetXid()] = allowed
		}
		if allowed {
			authorized = append(authorized, chart)
		}
	}
	return authorized, nil
}

// authorizeChartExperimentWrites requires WRITER on every experiment the given charts belong to.
// Unlike a read, a partial result is not an option here: a caller who cannot write one of the
// experiments must not have the dashboard created at all, so the first denial aborts the request.
// Each distinct xid is checked once, bounding the XID service round trips by the number of
// experiments rather than the number of charts.
func (s *Server) authorizeChartExperimentWrites(ctx context.Context, user string, charts []*dpb.Chart) error {
	if len(charts) == 0 {
		return nil
	}
	if s.xidClient == nil {
		return status.Error(codes.FailedPrecondition, "xid client is not configured")
	}

	checked := make(map[int64]bool, len(charts))
	for _, chart := range charts {
		if checked[chart.GetXid()] {
			continue
		}
		checked[chart.GetXid()] = true
		if err := s.authorizeExperimentWrite(ctx, user, chart.GetXid()); err != nil {
			return err
		}
	}
	return nil
}

// authorizeChartReferences requires READER on the experiment of every chart a dashboard links by
// ID. Linking is not a write on the chart itself, so read access is the bar — but a caller must not
// be able to attach a chart they cannot see, and an ID that names no chart has to be rejected here
// rather than surfacing as a foreign key violation at commit time.
func (s *Server) authorizeChartReferences(ctx context.Context, user string, chartIDs []string) error {
	if len(chartIDs) == 0 {
		return nil
	}
	if s.xidClient == nil {
		return status.Error(codes.FailedPrecondition, "xid client is not configured")
	}
	if s.repo == nil {
		return status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	checked := make(map[int64]bool, len(chartIDs))
	for _, chartID := range chartIDs {
		chart, err := s.fetchReferencedChart(ctx, chartID)
		if err != nil {
			return err
		}
		if checked[chart.GetXid()] {
			continue
		}
		checked[chart.GetXid()] = true
		if err := s.authorizeExperimentRead(ctx, user, chart.GetXid()); err != nil {
			return err
		}
	}
	return nil
}

// fetchReferencedChart loads a chart named by ID and rejects one that cannot be authorized.
func (s *Server) fetchReferencedChart(ctx context.Context, chartID string) (*dpb.Chart, error) {
	chart, err := s.repo.GetChart(ctx, chartID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.InvalidArgument, "chart %q does not exist", chartID)
		}
		return nil, status.Errorf(codes.Internal, "failed to read chart %q: %v", chartID, err)
	}
	if chart.GetXid() <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "chart %q is missing a valid experiment XID (%d)", chartID, chart.GetXid())
	}
	return chart, nil
}

// isChartReadable reports whether the user holds READER on the chart's experiment. A denial is a
// decision rather than an error, but any other failure is propagated so that an unreachable XID
// service is never mistaken for an empty result.
func (s *Server) isChartReadable(ctx context.Context, user string, chart *dpb.Chart) (bool, error) {
	if chart.GetXid() <= 0 {
		logging.Warn(ctx, "dropping chart without a valid experiment XID", "chart", chart.GetName(), "xid", chart.GetXid())
		return false, nil
	}
	err := s.authorizeExperimentRead(ctx, user, chart.GetXid())
	if err == nil {
		return true, nil
	}
	if status.Code(err) == codes.PermissionDenied {
		return false, nil
	}
	return false, err
}
