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

package postgresdata

import (
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// ExperimentRow represents a row in the PostgreSQL Experiments table.
type ExperimentRow struct {
	Xid          int64              `db:"xid"`
	Title        string             `db:"title"`
	Creator      string             `db:"creator"`
	Priority     int64              `db:"priority"`
	Urls         [][]byte           `db:"urls"`
	Tags         []string           `db:"tags"`
	Notes        string             `db:"notes"`
	Resources    [][]byte           `db:"resources"`
	Status       []byte             `db:"status"`
	ActiveState  int64              `db:"activestate"`
	LaunchState  int64              `db:"launchstate"`
	CreateTime   time.Time          `db:"createtime"`
	UpdateTime   time.Time          `db:"updatetime"`
	CompleteTime pgtype.Timestamptz `db:"completetime"`
}

// GetInsertArgs returns the strict named arguments required by the Experiments INSERT statement.
func (r ExperimentRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":          r.Xid,
		"title":        r.Title,
		"creator":      r.Creator,
		"priority":     r.Priority,
		"urls":         r.Urls,
		"tags":         r.Tags,
		"notes":        r.Notes,
		"resources":    r.Resources,
		"status":       r.Status,
		"activestate":  r.ActiveState,
		"launchstate":  r.LaunchState,
		"createtime":   r.CreateTime,
		"updatetime":   r.UpdateTime,
		"completetime": r.CompleteTime,
	}
}

// GetUpdateArgs returns the strict named arguments required by the Experiments metadata UPDATE statement.
func (r ExperimentRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":        r.Xid,
		"title":      r.Title,
		"priority":   r.Priority,
		"urls":       r.Urls,
		"tags":       r.Tags,
		"notes":      r.Notes,
		"updatetime": r.UpdateTime,
	}
}

// GetStatusUpdateArgs returns the strict named arguments required by the Experiments status UPDATE statement.
func (r ExperimentRow) GetStatusUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":          r.Xid,
		"status":       r.Status,
		"activestate":  r.ActiveState,
		"launchstate":  r.LaunchState,
		"updatetime":   r.UpdateTime,
		"completetime": r.CompleteTime,
	}
}

// GetResourcesUpdateArgs returns the strict named arguments required by the Experiments resources UPDATE statement.
func (r ExperimentRow) GetResourcesUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":        r.Xid,
		"resources":  r.Resources,
		"updatetime": r.UpdateTime,
	}
}

// ExperimentRowToProto converts a scanned PostgreSQL ExperimentRow into an essmsgpb.Experiment proto.
func ExperimentRowToProto(row ExperimentRow) (*essmsgpb.Experiment, error) {
	urls, err := unmarshalProtoSlice[essmsgpb.Url](row.Urls)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Urls: %w", err)
	}
	resources, err := unmarshalProtoSlice[essmsgpb.ResourceSet](row.Resources)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Resources: %w", err)
	}
	var status *essmsgpb.ExperimentStatus
	if row.Status != nil {
		status, err = UnmarshalProto[essmsgpb.ExperimentStatus](row.Status)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Status: %w", err)
		}
	}
	exp := &essmsgpb.Experiment{
		Name:       validator.ConstructExperimentName(row.Xid),
		Title:      row.Title,
		Creator:    row.Creator,
		Priority:   essmsgpb.Priority(row.Priority),
		Urls:       urls,
		Tags:       slices.Clone(row.Tags),
		Notes:      row.Notes,
		Resources:  resources,
		Status:     status,
		CreateTime: timestamppb.New(row.CreateTime),
		UpdateTime: timestamppb.New(row.UpdateTime),
	}
	if row.CompleteTime.Valid {
		exp.CompleteTime = timestamppb.New(row.CompleteTime.Time)
	}
	return exp, nil
}

// ExperimentProtoToRow converts an essmsgpb.Experiment proto into an ExperimentRow for persistence.
func ExperimentProtoToRow(exp *essmsgpb.Experiment) (ExperimentRow, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return ExperimentRow{}, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	urls, err := marshalProtoSlice(exp.GetUrls())
	if err != nil {
		return ExperimentRow{}, fmt.Errorf("failed to marshal Urls: %w", err)
	}
	resources, err := marshalProtoSlice(exp.GetResources())
	if err != nil {
		return ExperimentRow{}, fmt.Errorf("failed to marshal Resources: %w", err)
	}
	statusBytes, err := MarshalNullableProto(exp.GetStatus())
	if err != nil {
		return ExperimentRow{}, fmt.Errorf("failed to marshal Status: %w", err)
	}
	return ExperimentRow{
		Xid:          xid,
		Title:        exp.GetTitle(),
		Creator:      exp.GetCreator(),
		Priority:     int64(exp.GetPriority()),
		Urls:         urls,
		Tags:         nonNilStrings(exp.GetTags()),
		Notes:        exp.GetNotes(),
		Resources:    resources,
		Status:       statusBytes,
		ActiveState:  int64(exp.GetStatus().GetActiveState()),
		LaunchState:  int64(exp.GetStatus().GetLaunchState()),
		CreateTime:   exp.GetCreateTime().AsTime(),
		UpdateTime:   experimentUpdateTime(exp),
		CompleteTime: nullableTimestamptz(exp.GetCompleteTime()),
	}, nil
}

func experimentUpdateTime(exp *essmsgpb.Experiment) time.Time {
	if exp.GetUpdateTime() != nil {
		return exp.GetUpdateTime().AsTime()
	}
	return exp.GetStatus().GetUpdateTime().AsTime()
}

func marshalProtoSlice[T any, PT ProtoPointer[T]](items []PT) ([][]byte, error) {
	out := make([][]byte, len(items))
	for i, item := range items {
		b, err := MarshalProto(item)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

func unmarshalProtoSlice[T any, PT ProtoPointer[T]](raw [][]byte) ([]PT, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]PT, len(raw))
	for i, b := range raw {
		msg, err := UnmarshalProto[T, PT](b)
		if err != nil {
			return nil, err
		}
		out[i] = msg
	}
	return out, nil
}

func nonNilStrings(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	return slices.Clone(items)
}

func nullableTimestamptz(ts *timestamppb.Timestamp) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: ts.AsTime(), Valid: true}
}
