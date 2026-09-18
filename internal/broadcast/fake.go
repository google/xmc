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

package broadcast

import (
	"context"
	"errors"
	"sync"
)

var (
	_ Publisher = (*FakeClient)(nil)
	_ Consumer  = (*FakeClient)(nil)
)

// FakeClient is an in-memory broadcast test double implementing Publisher and Consumer.
type FakeClient struct {
	streamName string
	mu         sync.RWMutex
	messages   []string
	subs       []chan string
	publishErr error
}

// NewFakeClient creates a new FakeClient test double for a broadcast stream.
func NewFakeClient(streamName string) *FakeClient {
	return &FakeClient{
		streamName: streamName,
		messages:   make([]string, 0),
		subs:       make([]chan string, 0),
	}
}

// SetPublishError configures an error to be returned by future Publish calls.
func (f *FakeClient) SetPublishError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishErr = err
}

// Publish records the message and broadcasts it to all active in-memory subscribers.
func (f *FakeClient) Publish(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	if f.publishErr != nil {
		err := f.publishErr
		f.mu.Unlock()
		return err
	}
	f.messages = append(f.messages, message)
	subs := make([]chan string, len(f.subs))
	copy(subs, f.subs)
	f.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- message:
		default:
		}
	}
	return nil
}

// LatestOffset returns "0" as the starting stream offset for the in-memory fake.
func (f *FakeClient) LatestOffset(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "0", nil
}

// Subscribe listens for in-memory broadcast messages and invokes handler for each message.
func (f *FakeClient) Subscribe(ctx context.Context, startID string, handler func(msg string)) error {
	if handler == nil {
		return nil
	}
	ch := f.addSubscriber()
	defer f.removeSubscriber(ch)

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case msg := <-ch:
			handler(msg)
		}
	}
}

func (f *FakeClient) addSubscriber() chan string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan string, 100)
	f.subs = append(f.subs, ch)
	return ch
}

func (f *FakeClient) removeSubscriber(ch chan string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, sub := range f.subs {
		if sub == ch {
			f.subs = append(f.subs[:i], f.subs[i+1:]...)
			break
		}
	}
}

// Messages returns a copy of all published messages for test assertions.
func (f *FakeClient) Messages() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	res := make([]string, len(f.messages))
	copy(res, f.messages)
	return res
}
