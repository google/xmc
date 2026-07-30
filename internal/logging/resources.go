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
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/validator/validator"
)

const (
	keyXID    = "xid"
	keyWID    = "wid"
	keyEID    = "eid"
	keyAID    = "aid"
	keyOID    = "oid"
	keyJobSet = "jobset"
)

// WithXIDName adds xid group to logger in context.
func WithXIDName(ctx context.Context, name string) context.Context {
	xid, err := validator.ValidateXIDName(name)
	if err != nil {
		Warn(ctx, "Failed to parse XID name in logging.WithXIDName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)))
}

// WithExperimentName adds experiment group to logger in context.
func WithExperimentName(ctx context.Context, name string) context.Context {
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		Warn(ctx, "Failed to parse experiment name in logging.WithExperimentName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)))
}

// WithWorkUnitName adds work_unit group to logger in context.
func WithWorkUnitName(ctx context.Context, name string) context.Context {
	xid, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		Warn(ctx, "Failed to parse work unit name in logging.WithWorkUnitName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)), slog.String(keyWID, fmt.Sprintf("%d", wid)))
}

// WithWorkUnitExecutionName adds work_unit_execution group to logger in context.
func WithWorkUnitExecutionName(ctx context.Context, name string) context.Context {
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(name)
	if err != nil {
		Warn(ctx, "Failed to parse work unit execution name in logging.WithWorkUnitExecutionName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)), slog.String(keyWID, fmt.Sprintf("%d", wid)), slog.String(keyEID, fmt.Sprintf("%d", eid)))
}

// WithOperationName adds operation group to logger in context.
func WithOperationName(ctx context.Context, name string) context.Context {
	xid, wid, oid, err := validator.ValidateOperationName(name)
	if err != nil {
		Warn(ctx, "Failed to parse operation name in logging.WithOperationName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)), slog.String(keyWID, fmt.Sprintf("%d", wid)), slog.String(keyOID, oid))
}

// WithArtifactName adds artifact group to logger in context.
func WithArtifactName(ctx context.Context, name string) context.Context {
	aid, err := validator.ValidateArtifactName(name)
	if err != nil {
		Warn(ctx, "Failed to parse artifact name in logging.WithArtifactName", "error", err)
		return ctx
	}
	return WithLabels(ctx, slog.String(keyAID, fmt.Sprintf("%d", aid)))
}

// WithArtifactParent adds artifact parent group to logger in context.
func WithArtifactParent(ctx context.Context, name string) context.Context {
	xid, wid, err := validator.ValidateArtifactParent(name)
	if err != nil {
		Warn(ctx, "Failed to parse artifact parent in logging.WithArtifactParent", "error", err)
		return ctx
	}
	return withExperimentOrWorkUnitLabels(ctx, xid, wid)
}

// WithStatusMessageParent adds status message parent group to logger in context.
func WithStatusMessageParent(ctx context.Context, name string) context.Context {
	xid, wid, err := validator.ValidateStatusMessageParent(name)
	if err != nil {
		Warn(ctx, "Failed to parse status message parent in logging.WithStatusMessageParent", "error", err)
		return ctx
	}
	return withExperimentOrWorkUnitLabels(ctx, xid, wid)
}

// WithJobSetName adds job_set group to logger in context. We can't use utils here, because
// of circular dependencies, so we have to extract the labels manually.
func WithJobSetName(ctx context.Context, name string, labels map[string]string) context.Context {
	if labels != nil {
		ctx = withJobSetLabels(ctx, labels)
	} else {
		ctx = withJobSetParsedName(ctx, name)
	}
	return WithLabels(ctx, slog.String(keyJobSet, name))
}

func withJobSetLabels(ctx context.Context, labels map[string]string) context.Context {
	if xid := labels[constants.ExperimentIDLabel]; xid != "" {
		ctx = WithLabels(ctx, slog.String(keyXID, xid))
	}
	if wid := labels[constants.WorkUnitIDLabel]; wid != "" {
		ctx = WithLabels(ctx, slog.String(keyWID, wid))
	}
	if eid := labels[constants.ExecutionIDLabel]; eid != "" {
		ctx = WithLabels(ctx, slog.String(keyEID, eid))
	}
	return ctx
}

// TODO: Evaluate enforcing/validating label presence when all fields are missing.
func withJobSetParsedName(ctx context.Context, name string) context.Context {
	split := strings.Split(name, "-")
	if len(split) != 4 {
		Warn(ctx, "Failed to parse job set name in logging.WithJobSetName", "jobset", name, "split", split)
		return ctx
	}
	if xid := split[2]; xid != "" {
		ctx = WithLabels(ctx, slog.String(keyXID, xid))
	}
	if wid := split[3]; wid != "" {
		ctx = WithLabels(ctx, slog.String(keyWID, wid))
	}
	return ctx
}

// withExperimentOrWorkUnitLabels adds experiment or work unit labels to logger in contexts.
func withExperimentOrWorkUnitLabels(ctx context.Context, xid int64, wid int64) context.Context {
	if wid != 0 {
		return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)), slog.String(keyWID, fmt.Sprintf("%d", wid)))
	}
	return WithLabels(ctx, slog.String(keyXID, fmt.Sprintf("%d", xid)))
}
