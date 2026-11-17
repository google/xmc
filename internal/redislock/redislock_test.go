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

package redislock

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"
)

type setCall struct {
	Key, Mode string
	TTL       time.Duration
}

type evalCall struct {
	Keys []string
	Args []any
}

type fakeClient struct {
	redis.Scripter
	setErr, evalErr error
	evalVal         int64
	lastSet         setCall
	lastValue       any
	lastEval        evalCall
}

func (f *fakeClient) SetArgs(_ context.Context, key string, value any, a redis.SetArgs) *redis.StatusCmd {
	f.lastSet, f.lastValue = setCall{Key: key, Mode: a.Mode, TTL: a.TTL}, value
	if f.setErr != nil {
		return redis.NewStatusResult("", f.setErr)
	}
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeClient) EvalSha(_ context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	f.lastEval = evalCall{Keys: keys, Args: args}
	cmd := redis.NewCmd(context.Background())
	if f.evalErr != nil {
		cmd.SetErr(f.evalErr)
		return cmd
	}
	cmd.SetVal(f.evalVal)
	return cmd
}

func (f *fakeClient) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return f.EvalSha(ctx, "", keys, args...)
}

func TestNew_Validation(t *testing.T) {
	t.Parallel()
	valid := &fakeClient{}
	tests := []struct {
		name    string
		client  Client
		ttl     time.Duration
		wantErr bool
	}{
		{name: "nil client", client: nil, ttl: time.Second, wantErr: true},
		{name: "typed nil redis client", client: (*redis.Client)(nil), ttl: time.Second, wantErr: true},
		{name: "zero ttl", client: valid, ttl: 0, wantErr: true},
		{name: "negative ttl", client: valid, ttl: -time.Second, wantErr: true},
		{name: "sub-millisecond ttl", client: valid, ttl: 999 * time.Microsecond, wantErr: true},
		{name: "exact 1ms boundary", client: valid, ttl: time.Millisecond},
		{name: "standard 15s ttl", client: valid, ttl: 15 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := New(tc.client, tc.ttl)
			if (err != nil) != tc.wantErr || (!tc.wantErr && got == nil) {
				t.Fatalf("New(_, %v) = (%v, %v), wantErr %v", tc.ttl, got, err, tc.wantErr)
			}
		})
	}
}

func TestLocker_Acquire(t *testing.T) {
	t.Parallel()
	transportErr, randErr := errors.New("connection refused"), errors.New("entropy unavailable")
	tests := []struct {
		name     string
		key      string
		setErr   error
		randRead func([]byte) (int, error)
		wantErr  error
		anyErr   bool
	}{
		{name: "acquired", key: "xmc:study-reconcile-lock:42"},
		{name: "multibyte key", key: "xmc:study-reconcile-lock:実験-42"},
		{name: "empty key", key: "", anyErr: true},
		{name: "already held", key: "xmc:study-reconcile-lock:42", setErr: redis.Nil, wantErr: ErrNotAcquired},
		{name: "transport error", key: "xmc:study-reconcile-lock:42", setErr: transportErr, wantErr: transportErr},
		{name: "token rng error", key: "xmc:study-reconcile-lock:42", randRead: func([]byte) (int, error) { return 0, randErr }, wantErr: randErr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := &fakeClient{setErr: tc.setErr}
			locker, _ := New(fc, 15*time.Second)
			if tc.randRead != nil {
				locker.randRead = tc.randRead
			}
			lease, err := locker.Acquire(context.Background(), tc.key)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Acquire(%q) error = %v, want %v", tc.key, err, tc.wantErr)
				}
				return
			}
			if tc.anyErr || err != nil {
				if !tc.anyErr || err == nil {
					t.Fatalf("Acquire(%q) error = %v, anyErr %v", tc.key, err, tc.anyErr)
				}
				return
			}
			if diff := cmp.Diff(setCall{Key: tc.key, Mode: "NX", TTL: 15 * time.Second}, fc.lastSet); diff != "" {
				t.Errorf("SetArgs mismatch (-want +got):\n%s", diff)
			}
			token, _ := fc.lastValue.(string)
			decoded, decErr := hex.DecodeString(token)
			if decErr != nil || len(decoded) != tokenBytes || lease.token != token || lease.key != tc.key {
				t.Errorf("lease={%q,%q} decoded=%d decErr=%v, want %d hex bytes", lease.key, lease.token, len(decoded), decErr, tokenBytes)
			}
		})
	}
}

func TestLease_ExtendAndRelease(t *testing.T) {
	t.Parallel()
	transportErr := errors.New("broken pipe")
	withTTL := func(tok string) []any { return []any{tok, int64(15000)} }
	onlyTok := func(tok string) []any { return []any{tok} }
	tests := []struct {
		name     string
		extend   bool
		evalVal  int64
		evalErr  error
		wantArgs func(string) []any
		wantErr  error
	}{
		{name: "extend succeeds", extend: true, evalVal: 1, wantArgs: withTTL},
		{name: "extend detects lost lease", extend: true, evalVal: 0, wantArgs: withTTL, wantErr: ErrLeaseLost},
		{name: "extend wraps transport error", extend: true, evalErr: transportErr, wantArgs: withTTL, wantErr: transportErr},
		{name: "release succeeds", evalVal: 1, wantArgs: onlyTok},
		{name: "release detects lost lease", evalVal: 0, wantArgs: onlyTok, wantErr: ErrLeaseLost},
		{name: "release wraps transport error", evalErr: transportErr, wantArgs: onlyTok, wantErr: transportErr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := &fakeClient{evalVal: tc.evalVal, evalErr: tc.evalErr}
			locker, _ := New(fc, 15*time.Second)
			lease, _ := locker.Acquire(context.Background(), "xmc:study-reconcile-lock:99")
			opErr := lease.Release(context.Background())
			if tc.extend {
				opErr = lease.Extend(context.Background())
			}
			if (tc.wantErr != nil && !errors.Is(opErr, tc.wantErr)) || (tc.wantErr == nil && opErr != nil) {
				t.Fatalf("op error = %v, want %v", opErr, tc.wantErr)
			}
			if diff := cmp.Diff(evalCall{Keys: []string{"xmc:study-reconcile-lock:99"}, Args: tc.wantArgs(lease.token)}, fc.lastEval); diff != "" {
				t.Errorf("script call mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
