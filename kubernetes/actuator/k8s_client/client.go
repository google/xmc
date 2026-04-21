// Package k8sclient provides a client for interacting with the Kubernetes API.
package k8sclient

import (
	"context"
	"fmt"

	longrunningpb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	spb "rpc/status_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/k8s_io/client_go/v/v0_23/dynamic/dynamic"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"golang/k8s_io/client_go/v/v0_23/rest/rest"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/preconditions_validator/preconditionsvalidator"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// ActuatorRuntimeConfig holds runtime configuration for Actuator.
type ActuatorRuntimeConfig struct {
	// Decides whether a new namespace can be created when creating new Work Units.
	CreateNewNamespaces bool

	// The execution environment to use.
	DefaultExecutionEnvironment string
	SpannerDatabaseName         string
	QuotaProjectID              string
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

// CreateWorkUnit creates a new WorkUnit in Kubernetes.
func (c *Client) CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	priorities := Priorities{
		WorkUnitPriority:   payload.GetWorkUnit().GetPriority(),
		ExperimentPriority: payload.GetAction().GetCreate().GetExperimentPriority(),
	}
	err := c.createJobSet(ctx, payload.GetWorkUnit(), payload.GetTarget(), payload.GetAction().GetCreate().GetAuthor(), payload.GetCreateTime().AsTime(), priorities)

	if c.storage == nil {
		logging.Warn(ctx, "Storage client is not initialized, skipping work unit operation update.")
		if err != nil {
			return fmt.Errorf("failed to create JobSet for target %q: %w", payload.GetTarget(), err)
		}
		return nil
	}

	if uErr := c.updateOperation(ctx, payload, err); uErr != nil {
		return uErr
	}

	if err != nil {
		logging.Warn(ctx, "Failed to create JobSet for target", "target", payload.GetTarget(), "error", err)
		return fmt.Errorf("failed to create JobSet for target %q: %w", payload.GetTarget(), err)
	}

	return nil
}

// DeleteWorkUnit deletes a WorkUnit in Kubernetes.
func (c *Client) DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	namespace, err := c.getNamespaceFromWorkUnitWithFallback(ctx, payload.GetWorkUnit(), payload.GetTarget())
	if err != nil {
		return fmt.Errorf("failed to get namespace for target %q: %w", payload.GetTarget(), err)
	}

	_, err = c.deleteJobSet(ctx, payload.GetTarget(), namespace, payload.GetCreateTime().AsTime())

	if c.storage == nil {
		logging.Warn(ctx, "Storage client is not initialized, skipping work unit operation update.")
		if err != nil {
			return fmt.Errorf("failed to delete JobSet for target %q: %w", payload.GetTarget(), err)
		}
		return nil
	}

	if uErr := c.updateOperation(ctx, payload, err); uErr != nil {
		return uErr
	}

	if err != nil {
		logging.Warn(ctx, "Failed to delete JobSet for target", "target", payload.GetTarget(), "error", err)
		return fmt.Errorf("failed to delete JobSet for target %q: %w", payload.GetTarget(), err)
	}

	return nil
}

// RestartWorkUnit restarts a WorkUnit in Kubernetes.
func (c *Client) RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())
	ctx = logging.WithOperationName(ctx, payload.GetMetadata().GetOperationName())

	priorities := Priorities{
		WorkUnitPriority:   payload.GetWorkUnit().GetPriority(),
		ExperimentPriority: payload.GetAction().GetRestart().GetExperimentPriority(),
	}
	err := c.restartJobSet(ctx, payload.GetWorkUnit(), payload.GetTarget(), payload.GetAction().GetRestart().GetAuthor(), payload.GetCreateTime().AsTime(), priorities)

	if c.storage == nil {
		logging.Warn(ctx, "Storage client is not initialized, skipping work unit operation update.")
		if err != nil {
			return fmt.Errorf("failed to restart JobSet for target %q: %w", payload.GetTarget(), err)
		}
		return nil
	}

	if uErr := c.updateOperation(ctx, payload, err); uErr != nil {
		return uErr
	}

	if err != nil {
		logging.Warn(ctx, "Failed to restart JobSet for target", "target", payload.GetTarget(), "error", err)
		return fmt.Errorf("failed to restart JobSet for target %q: %w", payload.GetTarget(), err)
	}
	return nil
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
