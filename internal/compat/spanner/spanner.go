// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package spanner

import (
	"cloud.google.com/go/spanner"
	"context"
	"google.golang.org/api/option"
	"time"
)

type NullProtoMessage = spanner.NullProtoMessage

type Statement = spanner.Statement
type NullString = spanner.NullString
type NullInt64 = spanner.NullInt64
type NullTime = spanner.NullTime
type Client = spanner.Client
type Row = spanner.Row
type GenericColumnValue = spanner.GenericColumnValue
type Mutation = spanner.Mutation
type Key = spanner.Key
type KeyRange = spanner.KeyRange
type KeySet = spanner.KeySet

func NewStatement(sql string) Statement {
	return spanner.NewStatement(sql)
}

func Insert(table string, cols []string, vals []interface{}) *Mutation {
	return spanner.Insert(table, cols, vals)
}

func Update(table string, cols []string, vals []interface{}) *Mutation {
	return spanner.Update(table, cols, vals)
}

func Delete(table string, ks KeySet) *Mutation {
	return spanner.Delete(table, ks)
}

func InsertOrUpdate(table string, cols []string, vals []interface{}) *Mutation {
	return spanner.InsertOrUpdate(table, cols, vals)
}

func AllKeys() KeySet {
	return spanner.AllKeys()
}

func NewRow(names []string, values []interface{}) (*Row, error) {
	return spanner.NewRow(names, values)
}

func NewClient(ctx context.Context, database string, opts ...option.ClientOption) (*Client, error) {
	return spanner.NewClient(ctx, database, opts...)
}

type RowIterator = spanner.RowIterator
type ReadWriteTransaction = spanner.ReadWriteTransaction
type TimestampBound = spanner.TimestampBound

func ExactStaleness(d time.Duration) TimestampBound {
	return spanner.ExactStaleness(d)
}

func StrongRead() TimestampBound {
	return spanner.StrongRead()
}

type SessionPoolConfig struct {
	TrackSessionHandles               bool
	InactiveTransactionRemovalOptions InactiveTransactionRemovalOptions
}

type InactiveTransactionRemovalOptions struct {
	ActionOnInactiveTransaction ActionOnInactiveTransaction
}

type ActionOnInactiveTransaction int

const (
	Warn ActionOnInactiveTransaction = iota
)

type ClientConfig struct {
	SessionPoolConfig SessionPoolConfig
}

func NewClientWithConfig(ctx context.Context, database string, config ClientConfig, opts ...option.ClientOption) (*Client, error) {
	realConfig := spanner.ClientConfig{
		SessionPoolConfig: spanner.SessionPoolConfig{
			TrackSessionHandles: config.SessionPoolConfig.TrackSessionHandles,
		},
	}
	return spanner.NewClientWithConfig(ctx, database, realConfig, opts...)
}
