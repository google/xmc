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
// Package k8sclient provides a client for interacting with the Kubernetes API.
package k8sclient

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/k8s_io/client_go/v/v0_23/dynamic/dynamic"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"golang/k8s_io/client_go/v/v0_23/rest/rest"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	longrunningpb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	spb "rpc/status_go_proto"
	"task/go/status"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/preconditions_validator/preconditionsvalidator"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
)

// ActuatorRuntimeConfig holds runtime configuration for Actuator.
type ActuatorRuntimeConfig struct {
	// Decides whether a new namespace can be created when creating new Work Units.
	CreateNewNamespaces bool

	// The execution environment to use.
	DefaultExecutionEnvironment string
	SpannerDatabaseName         string
	QuotaProjectID              string

	// Decides whether XMC metrics are enabled.
	MetricsEnabled bool
}

// Priorities holds experiment and work unit priorities.
type Priorities struct {
	WorkUnitPriority   esspb.Priority
	ExperimentPriority esspb.Priority
}

// Client interacts with the Kubernetes API.
type Client struct {
	clientset              kubernetes.Interface
	dynamicClient          dynamic.Interface
	ManagerClient          runtimeclient.Reader
	storage                storage.Storage
	spannerClient          *spanner.Client
	RuntimeConfig          ActuatorRuntimeConfig
	preconditionsValidator preconditionsvalidator.Validator
}

// New creates a new Kubernetes client. It uses in-cluster config by default.
func New(runtimeConfig ActuatorRuntimeConfig, storage storage.Storage, spannerClient *spanner.Client) (*Client, error) {
	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic kubernetes client: %w", err)
	}

	return &Client{
		clientset:              clientset,
		dynamicClient:          dynamicClient,
		storage:                storage,
		spannerClient:          spannerClient,
		RuntimeConfig:          runtimeConfig,
		preconditionsValidator: preconditionsvalidator.New(),
	}, nil
}

// NewWithClients creates a new Kubernetes client with the given clientset and dynamic client.
func NewWithClients(clientset kubernetes.Interface, dynamicClient dynamic.Interface, runtimeConfig ActuatorRuntimeConfig, preconditionsValidator preconditionsvalidator.Validator, storage storage.Storage, spannerClient *spanner.Client) *Client {
	return &Client{
		clientset:              clientset,
		dynamicClient:          dynamicClient,
		storage:                storage,
		spannerClient:          spannerClient,
		RuntimeConfig:          runtimeConfig,
		preconditionsValidator: preconditionsValidator,
	}
}

func (c *Client) executeWorkUnitAction(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, actionName string, actionFn func(context.Context) error) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	err := actionFn(ctx)

	if c.storage == nil {
		logging.Warn(ctx, "Storage client is not initialized, skipping work unit operation update.")
		if err != nil {
			return fmt.Errorf("failed to %s JobSet for target %q: %w", actionName, payload.GetTarget(), err)
		}
		return nil
	}

	if uErr := c.updateOperation(ctx, payload, err); uErr != nil {
		return uErr
	}

	if err != nil {
		logging.Warn(ctx, fmt.Sprintf("Failed to %s JobSet for target", actionName), "target", payload.GetTarget(), "error", err)
		return fmt.Errorf("failed to %s JobSet for target %q: %w", actionName, payload.GetTarget(), err)
	}

	return nil
}

func (c *Client) executePrioritizedAction(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, actionName string, expPriority esspb.Priority, author string, targetFn func(context.Context, *esspb.WorkUnit, string, string, time.Time, Priorities) error) error {
	return c.executeWorkUnitAction(ctx, payload, actionName, func(ctx context.Context) error {
		priorities := Priorities{
			WorkUnitPriority:   payload.GetWorkUnit().GetPriority(),
			ExperimentPriority: expPriority,
		}
		return targetFn(ctx, payload.GetWorkUnit(), payload.GetTarget(), author, payload.GetCreateTime().AsTime(), priorities)
	})
}

// CreateWorkUnit creates a new WorkUnit in Kubernetes.
func (c *Client) CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	act := payload.GetAction().GetCreate()
	return c.executePrioritizedAction(ctx, payload, "create", act.GetExperimentPriority(), act.GetAuthor(), c.createJobSet)
}

// DeleteWorkUnit deletes a WorkUnit in Kubernetes.
func (c *Client) DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	return c.executeWorkUnitAction(ctx, payload, "delete", func(ctx context.Context) error {
		namespace, err := c.getNamespaceFromWorkUnitWithFallback(ctx, payload.GetWorkUnit(), payload.GetTarget())
		if err != nil {
			return fmt.Errorf("failed to get namespace for target %q: %w", payload.GetTarget(), err)
		}
		_, err = c.deleteJobSet(ctx, payload.GetTarget(), namespace, payload.GetCreateTime().AsTime())
		return err
	})
}

// RestartWorkUnit restarts a WorkUnit in Kubernetes.
func (c *Client) RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	act := payload.GetAction().GetRestart()
	return c.executePrioritizedAction(ctx, payload, "restart", act.GetExperimentPriority(), act.GetAuthor(), c.restartJobSet)
}

// Clientset returns the kubernetes clientset.
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// UpdateWorkUnit updates a WorkUnit in Kubernetes. Currently only updates the kueue priority of the
// corresponding JobSet.
func (c *Client) UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	priorities := Priorities{
		WorkUnitPriority:   payload.GetWorkUnit().GetPriority(),
		ExperimentPriority: payload.GetAction().GetUpdate().GetExperimentPriority(),
	}
	if err := c.updateJobSetPriority(ctx, payload.GetTarget(), priorities); err != nil {
		logging.Warn(ctx, "Failed to update JobSet priority for target", "target", payload.GetTarget(), "error", err)
		return fmt.Errorf("failed to update JobSet priority for target %q: %w", payload.GetTarget(), err)
	}
	return nil
}

func (c *Client) updateOperation(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, jobSetErr error) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	operationName := payload.GetMetadata().GetOperationName()
	xid, wid, oid, vErr := validator.ValidateOperationName(operationName)
	if vErr != nil {
		logging.Warn(ctx, "Failed to parse operation name to update work unit operation", "operationName", operationName, "error", vErr)
		return fmt.Errorf("failed to parse target %q to update work unit operation: %w", operationName, vErr)
	}

	var update *storage.OperationUpdate
	if jobSetErr != nil {
		update = createFailedOperationUpdate(xid, wid, oid, jobSetErr)
	} else {
		var uErr error
		update, uErr = createSuccessfulOperationUpdate(payload.GetWorkUnit(), xid, wid, oid)
		if uErr != nil {
			return uErr
		}
	}

	_, sErr := c.storage.UpdateWorkUnitOperation(ctx, update)
	if sErr != nil {
		return fmt.Errorf("failed to update work unit operation for %q: %w", payload.GetTarget(), sErr)
	}
	return nil
}

func createSuccessfulOperationUpdate(workUnit *esspb.WorkUnit, xid int64, wid int64, oid string) (*storage.OperationUpdate, error) {
	workUnitAny, err := anypb.New(workUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal work unit to any proto: %w", err)
	}
	op := &longrunningpb.Operation{}
	op.Result = &longrunningpb.Operation_Response{Response: workUnitAny}
	op.Done = true
	update := &storage.OperationUpdate{
		XID:       xid,
		WID:       wid,
		OID:       oid,
		Operation: op,
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{"done", "response"},
		},
	}
	return update, nil
}

func createFailedOperationUpdate(xid int64, wid int64, oid string, jobSetErr error) *storage.OperationUpdate {
	op := &longrunningpb.Operation{}
	op.Result = &longrunningpb.Operation_Error{Error: &spb.Status{Code: int32(status.ExtractCanonicalCode(jobSetErr)), Message: jobSetErr.Error()}}
	op.Done = true
	return &storage.OperationUpdate{
		XID:       xid,
		WID:       wid,
		OID:       oid,
		Operation: op,
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{"done", "error"},
		},
	}
}

// ManagedXProfGCSBucket returns the cluster-wide managed-xprof GCS bucket path.
// The bucket is provided to the actuator via the MANAGED_XPROF_GCS_PATH env var,
// which the Helm chart sources from the managed-xprof-gcs-config ConfigMap. It
// returns an empty string when managed xprof is not configured for the cluster.
func (c *Client) ManagedXProfGCSBucket(ctx context.Context) (string, error) {
	return os.Getenv(constants.XmcEnvVarManagedXprofGCSPath), nil
}
