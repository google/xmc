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
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/experimentschema"
	"xmanager_cloud/internal/validator"
)

// ExperimentRowToProto converts a Spanner Row containing Experiments table data into an Experiment proto message.
func ExperimentRowToProto(row *spanner.Row) (*pb.Experiment, error) {
	experiment := &pb.Experiment{ExperimentType: pb.Experiment_STANDARD}

	var xid int64
	var experimentType pb.Experiment_ExperimentType
	nullExperimentType := spanner.NullProtoEnum{ProtoEnumVal: &experimentType}
	err := readColumns(row,
		colDest{"Xid", &xid},
		colDest{"Title", &experiment.Title},
		colDest{"Creator", &experiment.Creator},
		colDest{"Priority", &experiment.Priority},
		colDest{"ExperimentType", &nullExperimentType},
		colDest{"Urls", &experiment.Urls},
		colDest{"Notes", &experiment.Notes},
		colDest{"Tags", &experiment.Tags},
		colDest{"Resources", &experiment.Resources},
		colDest{"Status", &experiment.Status},
	)
	if err != nil {
		return nil, err
	}
	experiment.Name = validator.ConstructExperimentName(xid)
	if nullExperimentType.Valid {
		experiment.ExperimentType = experimentType
	}
	studyConfig, err := studyConfigFromRow(row)
	if err != nil {
		return nil, err
	}
	if studyConfig != nil {
		experiment.TypeConfig = &pb.Experiment_StudyConfig{StudyConfig: studyConfig}
	}

	err = readNullTimestamps(row,
		tsDest{"CreateTime", &experiment.CreateTime},
		tsDest{"UpdateTime", &experiment.UpdateTime},
		tsDest{"CompleteTime", &experiment.CompleteTime},
	)
	if err != nil {
		return nil, err
	}
	return experiment, nil
}

// studyConfigFromRow reads the nullable StudyConfig column. NULL, stored for experiments that are
// not studies, reads as nil.
func studyConfigFromRow(row *spanner.Row) (*pb.StudyConfig, error) {
	column := spanner.NullProtoMessage{ProtoMessageVal: &pb.StudyConfig{}}
	if err := row.ColumnByName("StudyConfig", &column); err != nil {
		return nil, fmt.Errorf("failed to read column StudyConfig: %w", err)
	}
	return studyConfigFromNullProto(column)
}

// studyConfigFromNullProto returns the StudyConfig held by column, or nil when column is NULL.
func studyConfigFromNullProto(column spanner.NullProtoMessage) (*pb.StudyConfig, error) {
	if !column.Valid {
		return nil, nil
	}
	studyConfig, ok := column.ProtoMessageVal.(*pb.StudyConfig)
	if !ok {
		return nil, fmt.Errorf("column StudyConfig holds %T, want *StudyConfig", column.ProtoMessageVal)
	}
	return studyConfig, nil
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
			exp.ExperimentType,      // ExperimentType
			exp.GetStudyConfig(),    // StudyConfig
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
