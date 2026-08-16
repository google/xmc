// Copyright 2025 Google LLC
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

package server

import (
	"errors"
	"testing"
)

func TestProdServer_Close(t *testing.T) {
	t.Parallel()

	t.Run("nil cleanup returns nil", func(t *testing.T) {
		t.Parallel()
		ps := &ProdServer{}
		if err := ps.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("cleanup called and returns error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("cleanup error")
		ps := &ProdServer{
			cleanup: func() error {
				return expectedErr
			},
		}
		if err := ps.Close(); !errors.Is(err, expectedErr) {
			t.Errorf("Close() = %v, want %v", err, expectedErr)
		}
	})
}
