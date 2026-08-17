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

	"xmanager_cloud/xid_service/config"
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

	t.Run("nil ProdServer returns nil", func(t *testing.T) {
		t.Parallel()
		var ps *ProdServer
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

func TestNewProd_Validation(t *testing.T) {
	t.Parallel()

	t.Run("nil config", func(t *testing.T) {
		t.Parallel()
		ps, err := NewProd(t.Context(), nil, nil)
		if ps != nil || err == nil {
			t.Errorf("NewProd(nil, nil) = (%v, %v), want (nil, error)", ps, err)
		}
	})

	t.Run("nil oauth config", func(t *testing.T) {
		t.Parallel()
		ps, err := NewProd(t.Context(), &config.Config{}, nil)
		if ps != nil || err == nil {
			t.Errorf("NewProd(cfg, nil) = (%v, %v), want (nil, error)", ps, err)
		}
	})
}

func TestProdServer_HealthCheckers(t *testing.T) {
	t.Parallel()

	t.Run("nil ProdServer returns nil checkers", func(t *testing.T) {
		t.Parallel()
		var ps *ProdServer
		sp := ps.HealthCheckers()
		if sp != nil {
			t.Errorf("HealthCheckers() on nil ProdServer = %v, want nil", sp)
		}
	})
}
