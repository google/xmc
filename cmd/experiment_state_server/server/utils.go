// Copyright 2025 Google LLC
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
	"fmt"

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "protobuf/timestamp_go_proto"
	rpcstatuspb "rpc/status_go_proto"
	statusutils "task/go/status"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	ee "xmanager_cloud/toollibs/execution_environments/executionenvironments"
	"xmanager_cloud/validator/validator"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func propagateIAPJWT(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)

	if !ok {
		logging.Warn(ctx, "No metadata found in incoming context to Experiment State Server")
		return ctx
	}

	if jwtValues := md.Get("x-goog-iap-jwt-assertion"); len(jwtValues) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-goog-iap-jwt-assertion", jwtValues[0])
		logging.Debug(ctx, "Propagating x-goog-iap-jwt-assertion to XID service")
	} else {
		logging.Warn(ctx, "x-goog-iap-jwt-assertion not found in incoming metadata to Experiment State Server")
	}
	if jwtValues := md.Get("x-goog-user-email"); len(jwtValues) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-goog-user-email", jwtValues[0])
		logging.Debug(ctx, "Propagating x-goog-user-email to XID service")
	} else {
		logging.Warn(ctx, "x-goog-user-email not found in incoming metadata to Experiment State Server")
	}
	if authValues := md.Get("authorization"); len(authValues) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", authValues[0])
		logging.Debug(ctx, "Propagating authorization to XID service")
	}

	return ctx
}

func (b *baseHandler) getXid(ctx context.Context, name string) (*xidmsgpb.XidEntry, error) {
	if b.xidClient == nil {
		return nil, fmt.Errorf("xidClient is nil: %w", statusutils.ErrInternal)
	}
	req := &xidapipb.GetXidEntryRequest{
		Name: &name,
	}

	res, err := b.xidClient.GetXidEntry(propagateIAPJWT(ctx), req)
	if err != nil {
		return nil, fmt.Errorf("rpc GetXid failed: %w", err)
	}
	return res, nil
}

func (b *baseHandler) checkUserAuthorization(ctx context.Context, name string, user string, roleType *xidmsgpb.XidAcls_RoleType) (bool, error) {
	if b.xidClient == nil {
		return false, fmt.Errorf("xidClient is nil: %w", statusutils.ErrInternal)
	}
	req := &xidapipb.CheckUserAuthorizationRequest{
		Name:     &name,
		User:     &user,
		RoleType: roleType,
	}

	resp, err := b.xidClient.CheckUserAuthorization(propagateIAPJWT(ctx), req)
	if err != nil {
		return false, fmt.Errorf("rpc CheckUserAuthorization failed: %w", err)
	}
	if resp == nil || resp.Authorized == nil {
		return false, fmt.Errorf("rpc CheckUserAuthorization returned nil response or authorized field: %w", statusutils.ErrInternal)
	}
	return *resp.Authorized, nil
}

func newWorkUnitExecution(xid int64, wid int64) *msgpb.WorkUnitExecution {
	name := validator.ConstructWorkUnitExecutionName(xid, wid, 1)
	workUnitName := validator.ConstructWorkUnitName(xid, wid)
	return &msgpb.WorkUnitExecution{
		Name: name,
		Status: &msgpb.WorkUnitStatus{
			State: msgpb.WorkUnitStatus_INITIALIZING,
			SystemMessage: &msgpb.StructuredMessage{
				Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
				Severity:       msgpb.StructuredMessage_DEBUG,
				UserFacingText: "Work unit execution is being initialized.",
				Parent:         workUnitName,
			},
		},
		State:    msgpb.WorkUnitExecution_ACTIVE,
		Metadata: &msgpb.WorkUnitExecutionMetadata{},
	}
}

func (h *WorkUnitHandler) stopOperationOnError(ctx context.Context, operation *lropb.Operation, err error) {
	if err == nil {
		return
	}

	status, ok := statusutils.FromError(err)
	if !ok {
		logging.Error(ctx, "Failed to convert error to status", "error", err)
		return
	}
	// The error field in lropb.Operation expects a google.rpc.Status,
	// while status.FromError returns a util.task.Status. We need to convert.
	rpcStatus := &rpcstatuspb.Status{
		Code:    int32(status.CanonicalCode()),
		Message: status.Msg,
	}

	xid, wid, oid, err := validator.ValidateOperationName(operation.GetName())
	if err != nil {
		logging.Error(ctx, "Failed to validate operation name", "error", err)
		return
	}
	_, err = h.storage.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
		XID: xid,
		WID: wid,
		OID: oid,
		Operation: &lropb.Operation{
			Done: true,
			Result: &lropb.Operation_Error{
				Error: rpcStatus,
			},
		},
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{"done", "error"},
		},
	})

	if err != nil {
		logging.Error(ctx, "Failed to update work unit operation status on failure", "error", err)
	}
}

func (h *WorkUnitHandler) updateWorkUnitExecutionStatusMessage(ctx context.Context, xid int64, wid int64, eid int64, msg *msgpb.StructuredMessage, forceUpdate bool) error {
	msg.Source = msgpb.StructuredMessage_XMANAGER_BACKEND
	msg.CreateTime = timestamppb.Now()

	_, err := h.storage.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{
		{
			WID: wid,
			EID: eid,
			WorkUnitExecution: &msgpb.WorkUnitExecution{
				Status: &msgpb.WorkUnitStatus{
					SystemMessage: msg,
				},
			},
			UpdateMask:               &fmpb.FieldMask{Paths: []string{"status.system_message"}},
			ForceStatusMessageUpdate: forceUpdate,
		},
	})
	return err
}

func (h *WorkUnitHandler) handleEnqueueActionError(ctx context.Context, action string, workUnit *msgpb.WorkUnit, xid, wid int64, severity msgpb.StructuredMessage_Severity, err error) error {
	logging.Error(ctx, fmt.Sprintf("Failed to enqueue %s request for work unit", action), "error", err)
	_, _, eid, valErr := validator.ValidateWorkUnitExecutionName(workUnit.GetCurrentWorkUnitExecution().GetName())
	if valErr != nil {
		logging.Error(ctx, "Failed to validate work unit execution name", "error", valErr)
		return fmt.Errorf("failed to validate work unit execution name: %w (%v)", statusutils.ErrInternal, valErr)
	}

	userFacingText := fmt.Sprintf("Failed to %s work unit. Please try again.", action)
	if statusMsgErr := h.updateWorkUnitExecutionStatusMessage(ctx, xid, wid, eid, &msgpb.StructuredMessage{
		UserFacingText: userFacingText,
		Severity:       severity,
		Source:         msgpb.StructuredMessage_XMANAGER_BACKEND,
	}, false); statusMsgErr != nil {
		logging.Error(ctx, fmt.Sprintf("Failed to update work unit execution status message after failed %s attempt", action), "error", statusMsgErr)
	}
	return fmt.Errorf("failed to enqueue %s request for work unit %s: %w (%v)", action, workUnit.GetName(), statusutils.ErrInternal, err)
}

func newExperimentStatus(name string) *msgpb.ExperimentStatus {
	return &msgpb.ExperimentStatus{
		ActiveState:      msgpb.ExperimentStatus_ACTIVE,
		LaunchState:      msgpb.ExperimentStatus_LAUNCHING,
		LaunchId:         uuid.NewString(),
		ChildStateCounts: []*msgpb.ExperimentStatus_StateCount{},
		StructuredStatusMessage: &msgpb.StructuredMessage{
			Source:         msgpb.StructuredMessage_XMANAGER_CLIENT,
			Severity:       msgpb.StructuredMessage_DEBUG,
			UserFacingText: "Experiment is being launched.",
			Parent:         name,
		},
	}
}

func (b *baseHandler) createXid(ctx context.Context, creator, endpoint string, acls *xidmsgpb.XidAcls, parentXID string) (*xidmsgpb.XidEntry, error) {
	req := &xidapipb.CreateXidEntryRequest{
		XidEntry: &xidmsgpb.XidEntry{
			Creator: &creator,
			Origin: &xidmsgpb.OriginatingSystem{
				Endpoint: &endpoint,
			},
			Acls:      acls,
			ParentXid: &parentXID,
		},
	}

	xidEntry, err := b.xidClient.CreateXidEntry(propagateIAPJWT(ctx), req)
	if err != nil {
		return nil, fmt.Errorf("rpc CreateXid failed: %w", err)
	}

	return xidEntry, nil
}

func (b *baseHandler) defaultExecutionEnvironment() (ee.Environment, error) {
	if b.cfg == nil {
		return nil, fmt.Errorf("config is nil: %w", statusutils.ErrInternal)
	}
	executionEnv, ok := ee.Lookup(b.cfg.DefaultExecutionEnvironment)
	if !ok {
		return nil, fmt.Errorf("failed to get execution environment %q: %w", b.cfg.DefaultExecutionEnvironment, statusutils.ErrInternal)
	}
	return executionEnv, nil
}
