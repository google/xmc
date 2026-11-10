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

package workunit

import (
	"context"

	"xmanager_cloud/internal/storage"
)

// FetchActiveWorkUnitResources returns no rows until active work units are queried.
// TODO(#1597): query the creator, executable and state of active work units in queueNames.
func (r *Repository) FetchActiveWorkUnitResources(ctx context.Context, queueNames []string) ([]storage.ActiveWorkUnitResources, error) {
	return []storage.ActiveWorkUnitResources{}, nil
}
