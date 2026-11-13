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
package spannerdata

import (
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type colDest struct {
	name string
	ptr  any
}

func readColumns(row *spanner.Row, cols ...colDest) error {
	for _, col := range cols {
		if err := row.ColumnByName(col.name, col.ptr); err != nil {
			return fmt.Errorf("failed to read column %s: %w", col.name, err)
		}
	}
	return nil
}

func readNullTimestamp(row *spanner.Row, col string) (*timestamppb.Timestamp, error) {
	var ts spanner.NullTime
	if err := row.ColumnByName(col, &ts); err != nil {
		return nil, fmt.Errorf("failed to read column %s: %w", col, err)
	}
	if !ts.Valid {
		return nil, nil
	}
	return timestamppb.New(ts.Time), nil
}

type tsDest struct {
	name string
	dst  **timestamppb.Timestamp
}

func readNullTimestamps(row *spanner.Row, cols ...tsDest) error {
	for _, col := range cols {
		ts, err := readNullTimestamp(row, col.name)
		if err != nil {
			return err
		}
		*col.dst = ts
	}
	return nil
}

func readNullProto[T proto.Message](row *spanner.Row, col string, target T) (T, error) {
	nullMsg := spanner.NullProtoMessage{ProtoMessageVal: target}
	if err := row.ColumnByName(col, &nullMsg); err != nil {
		var zero T
		return zero, fmt.Errorf("failed to read column %s: %w", col, err)
	}
	if !nullMsg.Valid {
		var zero T
		return zero, nil
	}
	val, ok := nullMsg.ProtoMessageVal.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("column %s holds %T, want %T", col, nullMsg.ProtoMessageVal, zero)
	}
	return val, nil
}
