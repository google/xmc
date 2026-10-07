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
package spannerdata

import (
	"fmt"

	"cloud.google.com/go/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
	"xmanager_cloud/validator/validator"
)

// ExperimentRowToProto converts a Spanner Row containing Experiments table data into an Experiment proto message.
func ExperimentRowToProto(row *spanner.Row) (*pb.Experiment, error) {
	experiment := &pb.Experiment{}

	var xid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	experiment.Name = validator.ConstructExperimentName(xid)

	if err := row.ColumnByName("Title", &experiment.Title); err != nil {
		return nil, fmt.Errorf("failed to read column Title: %w", err)
	}
	if err := row.ColumnByName("Creator", &experiment.Creator); err != nil {
		return nil, fmt.Errorf("failed to read column Creator: %w", err)
	}

	if err := row.ColumnByName("Priority", &experiment.Priority); err != nil {
		return nil, fmt.Errorf("failed to read column Priority: %w", err)
	}

	if err := row.ColumnByName("Urls", &experiment.Urls); err != nil {
		return nil, fmt.Errorf("failed to read column Urls: %w", err)
	}
	if err := row.ColumnByName("Notes", &experiment.Notes); err != nil {
		return nil, fmt.Errorf("failed to read column Notes: %w", err)
	}
	if err := row.ColumnByName("Tags", &experiment.Tags); err != nil {
		return nil, fmt.Errorf("failed to read column Tags: %w", err)
	}

	if err := row.ColumnByName("Resources", &experiment.Resources); err != nil {
		return nil, fmt.Errorf("failed to read column Resources: %w", err)
	}

	if err := row.ColumnByName("Status", &experiment.Status); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}

	// Time fields.
	var createTime, updateTime, completeTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if createTime.Valid {
		experiment.CreateTime = timestamppb.New(createTime.Time)
	}

	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read column UpdateTime: %w", err)
	}
	if updateTime.Valid {
		experiment.UpdateTime = timestamppb.New(updateTime.Time)
	}

	if err := row.ColumnByName("CompleteTime", &completeTime); err != nil {
		return nil, fmt.Errorf("failed to read column CompleteTime: %w", err)
	}
	if completeTime.Valid {
		experiment.CompleteTime = timestamppb.New(completeTime.Time)
	}

	return experiment, nil
}

// ExperimentProtoToInsertMutation creates a Spanner mutation to insert an Experiment.
func ExperimentProtoToInsertMutation(exp *pb.Experiment) (*spanner.Mutation, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	var completeTime spanner.NullTime
	if exp.CompleteTime != nil {
		completeTime.Time = exp.CompleteTime.AsTime()
		completeTime.Valid = true
	}

	return spanner.Insert(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentColumns,
		[]any{
			xid,                     // XID
			exp.Title,               // Title
			exp.Creator,             // Creator
			int64(exp.Priority),     // Priority
			exp.Urls,                // Urls
			exp.Tags,                // Tags
			exp.Notes,               // Notes
			exp.Resources,           // Resources
			exp.Status,              // Status
			exp.CreateTime.AsTime(), // CreateTime
			exp.UpdateTime.AsTime(), // UpdateTime
			completeTime,            // CompleteTime
		},
	), nil
}

// ExperimentProtoToUpdateMutation creates a Spanner mutation to update an Experiment's fields.
func ExperimentProtoToUpdateMutation(exp *pb.Experiment) (*spanner.Mutation, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	return spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentUpdateColumns,
		[]any{
			data.ReverseBits(xid),   // BitReversedXID
			exp.Title,               // Title
			int64(exp.Priority),     // Priority
			exp.Urls,                // Urls
			exp.Tags,                // Tags
			exp.Notes,               // Notes
			exp.UpdateTime.AsTime(), // UpdateTime
		},
	), nil
}

// ExperimentProtoToStatusUpdateMutation creates a Spanner mutation to update an Experiment's status.
func ExperimentProtoToStatusUpdateMutation(exp *pb.Experiment) (*spanner.Mutation, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	var completeTime spanner.NullTime
	if exp.CompleteTime != nil {
		completeTime.Time = exp.CompleteTime.AsTime()
		completeTime.Valid = true
	}
	var updateTime any = spanner.NullTime{}
	if exp.GetStatus() != nil {
		updateTime = exp.Status.UpdateTime.AsTime()
	}

	return spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentStatusUpdateColumns,
		[]any{
			data.ReverseBits(xid), // BitReversedXID
			exp.Status,            // Status
			updateTime,            // UpdateTime
			completeTime,          // CompleteTime
		},
	), nil
}

// ExperimentProtoToResourcesUpdateMutation creates a Spanner mutation to update an Experiment's resources.
func ExperimentProtoToResourcesUpdateMutation(exp *pb.Experiment) (*spanner.Mutation, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	return spanner.Update(
		experimentschema.ExperimentTableName,
		experimentschema.ExperimentResourcesUpdateColumns,
		[]any{
			data.ReverseBits(xid),   // BitReversedXID
			exp.Resources,           // Resources
			exp.UpdateTime.AsTime(), // UpdateTime
		},
	), nil
}
