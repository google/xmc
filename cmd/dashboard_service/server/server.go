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

// Package server implements the gRPC server for the Dashboard Service.
package server

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	cstatus "task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// DashboardRepository defines the persistence contract for dashboards and charts.
type DashboardRepository interface {
	CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard) error
	GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error)
	ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error)
	UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error)
	DeleteDashboard(ctx context.Context, dashboardId string) error
	CreateChart(ctx context.Context, chart *dpb.Chart) error
	GetChart(ctx context.Context, chartId string) (*dpb.Chart, error)
}

// Config holds configuration for the dashboard service server.
type Config struct {
	Port           string
	GRPCPort       string
	ProjectID      string
	XidServiceAddr string
}

// Server implements the generated pb.DashboardServiceServer interface.
type Server struct {
	spb.UnimplementedDashboardServiceServer

	cfg        *Config
	repo       DashboardRepository
	auth       auth.AuthorizerInterface
	xidClient  xidapipb.XidServiceClient
	dataEngine *dataengine.DataEngine
}

var _ spb.DashboardServiceServer = (*Server)(nil)

// NewServer creates a new instance of Server.
func NewServer(cfg *Config, repo DashboardRepository, auth auth.AuthorizerInterface, xidClient xidapipb.XidServiceClient, de *dataengine.DataEngine) *Server {
	if repo == nil {
		panic("repo must not be nil")
	}
	if auth == nil {
		panic("auth must not be nil")
	}
	if xidClient == nil {
		panic("xidClient must not be nil")
	}
	return &Server{
		cfg:        cfg,
		repo:       repo,
		auth:       auth,
		xidClient:  xidClient,
		dataEngine: de,
	}
}

// propagateAuthContext forwards identity headers to downstream gRPC services without clobbering tracing metadata.
func propagateAuthContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	var pairs []string
	if jwt := md.Get("x-goog-iap-jwt-assertion"); len(jwt) > 0 && jwt[0] != "" {
		pairs = append(pairs, "x-goog-iap-jwt-assertion", jwt[0])
	}
	if email := md.Get("x-goog-user-email"); len(email) > 0 && email[0] != "" {
		pairs = append(pairs, "x-goog-user-email", email[0])
	}
	if authHeader := md.Get("authorization"); len(authHeader) > 0 && authHeader[0] != "" {
		pairs = append(pairs, "authorization", authHeader[0])
	}
	if len(pairs) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

// checkDashboardAuthorization verifies user permissions against the dashboard owner principal.
func (s *Server) checkDashboardAuthorization(ctx context.Context, user string, dash *dpb.Dashboard, role auth.Role) (bool, error) {
	if dash == nil {
		return false, nil
	}
	if s.auth == nil {
		return true, nil
	}
	acls := &xpb.XidAcls{
		Owners: []*xpb.XidAcls_Role{
			{
				Principal: &xpb.XidAcls_Role_User{
					User: dash.GetOwner(),
				},
			},
		},
	}
	return s.auth.IsUserAuthorized(ctx, user, acls, role)
}

// isNotFoundError checks if an error represents a NotFound error code.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.NotFound {
		return true
	}
	return cstatus.ExtractCanonicalCode(err) == cstatus.NotFound
}

// isAlreadyExistsError checks if an error represents an AlreadyExists error code.
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.AlreadyExists {
		return true
	}
	return cstatus.ExtractCanonicalCode(err) == cstatus.AlreadyExists
}

// GetChart retrieves a chart stub.
func (s *Server) GetChart(ctx context.Context, req *spb.GetChartRequest) (*dpb.Chart, error) {
	return &dpb.Chart{
		Name:  req.GetName(),
		Title: "Sample Chart",
	}, nil
}

// validateDataEngineDependencies ensures required service dependencies are configured.
func (s *Server) validateDataEngineDependencies() error {
	if s.dataEngine == nil {
		return status.Error(codes.FailedPrecondition, "data engine is not configured")
	}
	if s.repo == nil {
		return status.Error(codes.FailedPrecondition, "chart repository is not configured")
	}
	if s.xidClient == nil {
		return status.Error(codes.FailedPrecondition, "xid client is not configured")
	}
	return nil
}

// getChartForData retrieves the chart from repository and resolves its associated XID.
func (s *Server) getChartForData(ctx context.Context, chartName string) (*dpb.Chart, int64, error) {
	_, _, chartID, err := validator.ValidateChartName(chartName)
	if err != nil {
		return nil, 0, status.Errorf(codes.InvalidArgument, "invalid chart name %q: %v", chartName, err)
	}
	chart, err := s.repo.GetChart(ctx, chartID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, 0, status.Errorf(codes.NotFound, "chart %q not found", chartName)
		}
		return nil, 0, status.Errorf(codes.Internal, "failed to get chart %q: %v", chartName, err)
	}

	xid := chart.GetXid()
	if xid <= 0 {
		if parsed, err := validator.ParseChartName(chartName); err == nil && parsed.XID > 0 {
			xid = parsed.XID
		}
	}
	if xid <= 0 {
		return nil, 0, status.Errorf(codes.FailedPrecondition, "chart %q is missing a valid experiment XID (%d)", chartName, chart.GetXid())
	}
	return chart, xid, nil
}

// authorizeExperimentRead checks user read authorization against the XID service.
func (s *Server) authorizeExperimentRead(ctx context.Context, user string, xid int64) error {
	return s.authorizeExperiment(ctx, user, xid, xpb.XidAcls_READER)
}

// authorizeExperimentWrite checks user write authorization against the XID service.
func (s *Server) authorizeExperimentWrite(ctx context.Context, user string, xid int64) error {
	return s.authorizeExperiment(ctx, user, xid, xpb.XidAcls_WRITER)
}

// authorizeExperiment checks user authorization for a specific role against the XID service.
func (s *Server) authorizeExperiment(ctx context.Context, user string, xid int64, roleType xpb.XidAcls_RoleType) error {
	xidName := validator.ConstructXIDName(xid)
	xidResp, err := s.xidClient.CheckUserAuthorization(propagateAuthContext(ctx), &xidapipb.CheckUserAuthorizationRequest{
		Name:     &xidName,
		User:     &user,
		RoleType: &roleType,
	})
	if err != nil {
		logging.Error(ctx, "failed to check user authorization with XID service", "xid", xid, "user", user, "role", roleType.String(), "error", err)
		return status.Errorf(codes.Internal, "failed to verify permissions for experiment %d", xid)
	}
	if xidResp == nil || !xidResp.GetAuthorized() {
		action := "read"
		if roleType == xpb.XidAcls_WRITER {
			action = "modify"
		}
		return status.Errorf(codes.PermissionDenied, "user %q is not authorized to %s experiment %d", user, action, xid)
	}
	return nil
}

// GetChartData retrieves chart data after validating experiment-level read permissions via XID service.
func (s *Server) GetChartData(ctx context.Context, req *spb.GetChartDataRequest) (*dpb.ChartData, error) {
	if err := validator.ValidateGetChartDataRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid get chart data request: %v", err)
	}
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}
	if err := s.validateDataEngineDependencies(); err != nil {
		return nil, err
	}
	chart, xid, err := s.getChartForData(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeExperimentRead(ctx, user, xid); err != nil {
		return nil, err
	}

	return s.dataEngine.FetchChartData(ctx, chart, req)
}

// ListCharts retrieves a list of charts stub.
func (s *Server) ListCharts(ctx context.Context, req *spb.ListChartsRequest) (*spb.ListChartsResponse, error) {
	return &spb.ListChartsResponse{
		Charts: []*dpb.Chart{
			{
				Name:  req.GetParent() + "/charts/sample",
				Title: "Sample Chart",
			},
		},
	}, nil
}

// UpdateChart updates a chart stub.
func (s *Server) UpdateChart(ctx context.Context, req *spb.UpdateChartRequest) (*dpb.Chart, error) {
	return req.GetChart(), nil
}

// DeleteChart deletes a chart stub.
func (s *Server) DeleteChart(ctx context.Context, req *spb.DeleteChartRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
