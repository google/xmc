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
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/api/protos/experiment"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/mlflow"
)

const mlflowLaunchTimeout = 5 * time.Second

type clientComponentGetter struct {
	c *Client
}

func (g *clientComponentGetter) GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	if g.c == nil || (g.c.ManagerClient == nil && g.c.dynamicClient == nil) {
		return nil, apierrors.NewNotFound(crreader.ComponentGVR().GroupResource(), name)
	}
	return g.c.fetchComponentResource(ctx, name, namespace)
}

func (c *Client) configureMLflowForJobSet(ctx context.Context, xid, wid, eid int64, namespace string, jobSet *jobsetv1alpha2.JobSet) {
	cfg, enabled, err := mlflow.ResolveConfig(ctx, &clientComponentGetter{c: c}, c.secretReader, namespace)
	if !enabled {
		return
	}
	if err != nil {
		logging.Warn(ctx, "Failed to resolve MLflow configuration; continuing launch", "error", err)
		c.recordMLflowWarning(ctx, xid, wid, eid, fmt.Sprintf("MLflow configuration warning: %v", err))
		if cfg != nil {
			injectMLflowEnvIntoJobSet(jobSet, cfg, "", "")
		}
		return
	}

	title, eligible, err := c.resolveMLflowExperimentTitle(ctx, xid)
	if err != nil {
		logging.Warn(ctx, "Failed to check experiment eligibility for MLflow; continuing launch", "error", err)
		c.recordMLflowWarning(ctx, xid, wid, eid, fmt.Sprintf("MLflow experiment lookup failed: %v", err))
		injectMLflowEnvIntoJobSet(jobSet, cfg, "", "")
		return
	}
	if !eligible {
		return
	}

	ids, err := c.ensureMLflowRuns(ctx, cfg, title, xid, wid, eid)
	if err != nil {
		logging.Warn(ctx, "Failed to create MLflow runs at launch; continuing without MLFLOW_RUN_ID", "error", err)
		c.recordMLflowWarning(ctx, xid, wid, eid, fmt.Sprintf("MLflow run setup failed (work unit will still run): %v", err))
		injectMLflowEnvIntoJobSet(jobSet, cfg, ids.experimentID, "")
		return
	}

	annotateMLflowJobSet(jobSet, ids.experimentID, ids.parentRunID, ids.childRunID)
	c.persistMLflowMetadata(ctx, xid, wid, eid, ids.experimentID, ids.parentRunID, ids.childRunID)
	injectMLflowEnvIntoJobSet(jobSet, cfg, ids.experimentID, ids.childRunID)
}

func (c *Client) resolveMLflowExperimentTitle(ctx context.Context, xid int64) (string, bool, error) {
	fallbackTitle := fmt.Sprintf("xmc-experiment-%d", xid)
	if c.storage == nil {
		return fallbackTitle, true, nil
	}
	exp, err := c.storage.FetchExperiment(ctx, xid)
	if err != nil {
		return "", false, err
	}
	expType := exp.GetExperimentType()
	if expType != esspb.Experiment_STANDARD && expType != esspb.Experiment_EXPERIMENT_TYPE_UNSPECIFIED {
		return "", false, nil
	}
	if title := strings.TrimSpace(exp.GetTitle()); title != "" {
		return title, true, nil
	}
	return fallbackTitle, true, nil
}

type mlflowRunIDs struct {
	experimentID string
	parentRunID  string
	childRunID   string
}

func (c *Client) ensureMLflowRuns(ctx context.Context, cfg *mlflow.Config, title string, xid, wid, eid int64) (mlflowRunIDs, error) {
	httpClient := c.mlflowHTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: mlflowLaunchTimeout}
	}
	client, err := mlflow.NewClient(cfg, httpClient)
	if err != nil {
		return mlflowRunIDs{}, err
	}
	mlflowCtx, cancel := context.WithTimeout(ctx, mlflowLaunchTimeout)
	defer cancel()

	expID, err := client.EnsureExperiment(mlflowCtx, title)
	if err != nil {
		return mlflowRunIDs{}, err
	}

	mu := c.lockMLflowExperiment(xid)
	mu.Lock()
	parentRunID, err := client.EnsureParentRun(mlflowCtx, expID, xid)
	mu.Unlock()
	if err != nil {
		return mlflowRunIDs{experimentID: expID}, err
	}

	childRunID, err := client.EnsureChildRun(mlflowCtx, expID, parentRunID, xid, wid, eid)
	if err != nil {
		return mlflowRunIDs{experimentID: expID, parentRunID: parentRunID}, err
	}
	return mlflowRunIDs{experimentID: expID, parentRunID: parentRunID, childRunID: childRunID}, nil
}

func (c *Client) lockMLflowExperiment(xid int64) *sync.Mutex {
	c.mlflowExpMu.Lock()
	defer c.mlflowExpMu.Unlock()
	if c.mlflowExpLocks == nil {
		c.mlflowExpLocks = make(map[int64]*sync.Mutex)
	}
	mu, ok := c.mlflowExpLocks[xid]
	if !ok {
		mu = &sync.Mutex{}
		c.mlflowExpLocks[xid] = mu
	}
	return mu
}

func annotateMLflowJobSet(jobSet *jobsetv1alpha2.JobSet, expID, parentRunID, childRunID string) {
	if jobSet == nil {
		return
	}
	if jobSet.Annotations == nil {
		jobSet.Annotations = make(map[string]string)
	}
	if expID != "" {
		jobSet.Annotations[constants.MLflowExperimentIDAnnotation] = expID
	}
	if parentRunID != "" {
		jobSet.Annotations[constants.MLflowParentRunIDAnnotation] = parentRunID
	}
	if childRunID != "" {
		jobSet.Annotations[constants.MLflowRunIDAnnotation] = childRunID
	}
}

func (c *Client) persistMLflowMetadata(ctx context.Context, xid, wid, eid int64, expID, parentRunID, childRunID string) {
	if c.storage == nil {
		return
	}
	var paths []string
	meta := &esspb.WorkUnitExecutionMetadata{}
	if expID != "" {
		meta.MlflowExperimentId = expID
		paths = append(paths, "metadata.mlflow_experiment_id")
	}
	if parentRunID != "" {
		meta.MlflowParentRunId = parentRunID
		paths = append(paths, "metadata.mlflow_parent_run_id")
	}
	if childRunID != "" {
		meta.MlflowRunId = childRunID
		paths = append(paths, "metadata.mlflow_run_id")
	}
	if len(paths) == 0 {
		return
	}
	execName := validator.ConstructWorkUnitExecutionName(xid, wid, eid)
	update := &storage.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &esspb.WorkUnitExecution{
			Name:     execName,
			Metadata: meta,
		},
		UpdateMask: &fmpb.FieldMask{Paths: paths},
	}
	err := c.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		_, err := workunit.UpdateWorkUnitExecutionsInTx(ctx, tx, xid, []*storage.WorkUnitExecutionUpdate{update})
		return err
	})
	if err != nil {
		logging.Warn(ctx, "Failed to persist MLflow metadata onto WorkUnitExecution", "error", err)
	}
}

func (c *Client) recordMLflowWarning(ctx context.Context, xid, wid, eid int64, msg string) {
	if c.storage == nil {
		return
	}
	now := timestamppb.Now()
	execName := validator.ConstructWorkUnitExecutionName(xid, wid, eid)
	update := &storage.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &esspb.WorkUnitExecution{
			Name: execName,
			Status: &esspb.WorkUnitStatus{
				SystemMessage: &esspb.StructuredMessage{
					Severity:       esspb.StructuredMessage_WARNING,
					Source:         esspb.StructuredMessage_XMANAGER_BACKEND,
					Parent:         validator.ConstructWorkUnitName(xid, wid),
					UserFacingText: msg,
					CreateTime:     now,
				},
			},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"status.system_message"}},
	}
	err := c.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		_, err := workunit.UpdateWorkUnitExecutionsInTx(ctx, tx, xid, []*storage.WorkUnitExecutionUpdate{update})
		return err
	})
	if err != nil {
		logging.Warn(ctx, "Failed to record MLflow warning status message", "error", err)
	}
}

func injectMLflowEnvIntoJobSet(jobSet *jobsetv1alpha2.JobSet, cfg *mlflow.Config, expID, runID string) {
	if jobSet == nil || cfg == nil {
		return
	}
	envVars := buildMLflowEnvVars(cfg, expID, runID)
	if len(envVars) == 0 {
		return
	}
	for i := range jobSet.Spec.ReplicatedJobs {
		podSpec := &jobSet.Spec.ReplicatedJobs[i].Template.Spec.Template.Spec
		appendMissingEnvVars(podSpec.Containers, envVars)
		appendMissingEnvVars(podSpec.InitContainers, envVars)
	}
}

func buildMLflowEnvVars(cfg *mlflow.Config, expID, runID string) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	if cfg.TrackingURI != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "MLFLOW_TRACKING_URI", Value: cfg.TrackingURI})
	}
	if expID != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "MLFLOW_EXPERIMENT_ID", Value: expID})
	}
	if runID != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "MLFLOW_RUN_ID", Value: runID})
	}
	if cfg.SecretName != "" {
		if cfg.SecretTokenKey != "" {
			envVars = append(envVars, secretRefEnvVar("MLFLOW_TRACKING_TOKEN", cfg.SecretName, cfg.SecretTokenKey))
		}
		if cfg.SecretUsernameKey != "" {
			envVars = append(envVars, secretRefEnvVar("MLFLOW_TRACKING_USERNAME", cfg.SecretName, cfg.SecretUsernameKey))
		}
		if cfg.SecretPasswordKey != "" {
			envVars = append(envVars, secretRefEnvVar("MLFLOW_TRACKING_PASSWORD", cfg.SecretName, cfg.SecretPasswordKey))
		}
	}
	return envVars
}

func secretRefEnvVar(envName, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}

func appendMissingEnvVars(containers []corev1.Container, envVars []corev1.EnvVar) {
	for i := range containers {
		existing := make(map[string]bool, len(containers[i].Env))
		for _, e := range containers[i].Env {
			existing[e.Name] = true
		}
		for _, ev := range envVars {
			if !existing[ev.Name] {
				containers[i].Env = append(containers[i].Env, ev)
			}
		}
	}
}
