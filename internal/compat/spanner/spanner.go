package spanner

import (
	"cloud.google.com/go/spanner"
	"context"
	"fmt"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
	"time"
)

type NullProtoMessage struct {
	ProtoMessageVal proto.Message
	Valid           bool
}

func (m *NullProtoMessage) Encode() (interface{}, error) {
	if m == nil || !m.Valid || m.ProtoMessageVal == nil {
		return nil, nil
	}
	return proto.Marshal(m.ProtoMessageVal)
}

func (m *NullProtoMessage) Decode(val interface{}) error {
	if val == nil {
		m.Valid = false
		return nil
	}
	bytes, ok := val.([]byte)
	if !ok {
		if str, ok := val.(string); ok {
			bytes = []byte(str)
		} else {
			return fmt.Errorf("NullProtoMessage: expected []byte or string column value, got %T", val)
		}
	}
	if err := proto.Unmarshal(bytes, m.ProtoMessageVal); err != nil {
		return err
	}
	m.Valid = true
	return nil
}

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
