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
)

// Config holds the Redis connection configuration for the broadcast service.
type Config struct {
	Addr string
}

// Publisher defines the contract for broadcasting state update notifications.
type Publisher interface {
	Publish(ctx context.Context, message string) error
}

// Consumer defines the contract for subscribing to broadcast notifications.
type Consumer interface {
	Subscribe(ctx context.Context, startID string, handler func(msg string))
}
