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

package redisclient

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"
)

// fakePipeliner queues the commands of Pipelined on an unconnected go-redis pipeline and returns
// them with cmdErrs[i] set on command i and pipeErr as the Pipelined error.
type fakePipeliner struct {
	cmdErrs map[int]error
	pipeErr error
	calls   int
	cmds    []redis.Cmder
}

func (f *fakePipeliner) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	f.calls++
	client := redis.NewClient(&redis.Options{})
	defer client.Close()
	pipe := client.Pipeline()
	if err := fn(pipe); err != nil {
		return nil, err
	}
	f.cmds = pipe.Cmds()
	for i, cmd := range f.cmds {
		if err, ok := f.cmdErrs[i]; ok {
			cmd.SetErr(err)
		}
	}
	return f.cmds, f.pipeErr
}

// xAddSummary returns the stream of a queued XADD command followed by its data and failedAttempts
// values, sorted because go-redis writes map values in random order.
func xAddSummary(cmd redis.Cmder) []string {
	args := cmd.Args()
	var values []string
	for i := 2; i+1 < len(args); i++ {
		if key, ok := args[i].(string); ok && (key == "data" || key == "failedAttempts") {
			values = append(values, key+"="+args[i+1].(string))
		}
	}
	sort.Strings(values)
	return append([]string{args[0].(string), args[1].(string)}, values...)
}

func TestAddMessages(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")
	tests := []struct {
		name      string
		data      []string
		fake      *fakePipeliner
		wantCalls int
		wantErrs  []bool
	}{
		{name: "empty batch skips the round trip", data: nil, fake: &fakePipeliner{}, wantCalls: 0, wantErrs: []bool{}},
		{name: "all messages added in one round trip", data: []string{"a", "b", "c"}, fake: &fakePipeliner{}, wantCalls: 1, wantErrs: []bool{false, false, false}},
		{name: "per-message failure", data: []string{"a", "b", "c"}, fake: &fakePipeliner{cmdErrs: map[int]error{1: errBoom}, pipeErr: errBoom}, wantCalls: 1, wantErrs: []bool{false, true, false}},
		{name: "failed round trip fails every message", data: []string{"a", "b"}, fake: &fakePipeliner{cmdErrs: map[int]error{0: errBoom, 1: errBoom}, pipeErr: errBoom}, wantCalls: 1, wantErrs: []bool{true, true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := addMessages(ctx, tt.fake, "stream", tt.data)
			if tt.fake.calls != tt.wantCalls {
				t.Errorf("Pipelined calls = %d, want %d", tt.fake.calls, tt.wantCalls)
			}
			gotErrs := make([]bool, len(errs))
			for i, err := range errs {
				gotErrs[i] = err != nil
				if err != nil && !errors.Is(err, errBoom) {
					t.Errorf("errs[%d] = %v, want it to wrap %v", i, err, errBoom)
				}
			}
			if diff := cmp.Diff(tt.wantErrs, gotErrs); diff != "" {
				t.Errorf("addMessages() failures mismatch (-want +got):\n%s", diff)
			}
			for i, cmd := range tt.fake.cmds {
				want := []string{"xadd", "stream", "data=" + tt.data[i], "failedAttempts=0"}
				if diff := cmp.Diff(want, xAddSummary(cmd)); diff != "" {
					t.Errorf("command %d mismatch (-want +got):\n%s", i, diff)
				}
			}
		})
	}
}

func TestPublisherClientAddMessages_UsesClientPipeline(t *testing.T) {
	fake := &fakeRedisClient{}
	p := &PublisherClient{baseManager: baseManager{Client: &pipelinedRedisClient{fakeRedisClient: fake, pipeliner: &fakePipeliner{}}, StreamName: "s"}}
	for i, err := range p.AddMessages(context.Background(), []string{"x"}) {
		if err != nil {
			t.Errorf("AddMessages()[%d] = %v, want nil", i, err)
		}
	}
}

// pipelinedRedisClient adds a fake Pipelined to fakeRedisClient.
type pipelinedRedisClient struct {
	*fakeRedisClient
	pipeliner *fakePipeliner
}

func (c *pipelinedRedisClient) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return c.pipeliner.Pipelined(ctx, fn)
}
