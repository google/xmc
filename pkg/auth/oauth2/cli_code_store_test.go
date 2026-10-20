// Copyright 2026 Google LLC
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

package oauth2

import (
	"crypto/sha256"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEnableCLILogin(t *testing.T) {
	t.Run("nil store is rejected", func(t *testing.T) {
		a := NewAuthenticator(baseLoginConfig())
		if err := a.EnableCLILogin(nil); err == nil {
			t.Fatal("EnableCLILogin(nil) error = nil, want non-nil")
		}
		if a.cliCodes != nil || a.cliLoginKey != nil {
			t.Error("EnableCLILogin(nil) enabled CLI login")
		}
	})
	t.Run("store enables CLI login", func(t *testing.T) {
		a := NewAuthenticator(baseLoginConfig())
		store := newFakeCLICodeStore()
		if err := a.EnableCLILogin(store); err != nil {
			t.Fatalf("EnableCLILogin() failed: %v", err)
		}
		wantKey, err := deriveCLILoginMACKey([]byte(loginTestSecret))
		if err != nil {
			t.Fatalf("deriveCLILoginMACKey() failed: %v", err)
		}
		if a.cliCodes != store || !cmp.Equal(a.cliLoginKey, wantKey) {
			t.Error("EnableCLILogin() did not install the store and MAC key")
		}
	})
	t.Run("second call is rejected", func(t *testing.T) {
		a := NewAuthenticator(baseLoginConfig())
		first := newFakeCLICodeStore()
		if err := a.EnableCLILogin(first); err != nil {
			t.Fatalf("EnableCLILogin() failed: %v", err)
		}
		if err := a.EnableCLILogin(newFakeCLICodeStore()); err == nil {
			t.Fatal("second EnableCLILogin() error = nil, want non-nil")
		}
		if a.cliCodes != first {
			t.Error("second EnableCLILogin() replaced the store")
		}
	})
}

func TestNewCLILoginCode(t *testing.T) {
	code, hash, err := newCLILoginCode()
	if err != nil {
		t.Fatalf("newCLILoginCode() failed: %v", err)
	}
	if !base64URL256BitPattern.MatchString(code) {
		t.Errorf("newCLILoginCode() code = %q, want 43 base64url characters", code)
	}
	wantHash := sha256.Sum256([]byte(code))
	if diff := cmp.Diff(wantHash[:], hash); diff != "" {
		t.Errorf("newCLILoginCode() hash mismatch (-want +got):\n%s", diff)
	}
	other, _, err := newCLILoginCode()
	if err != nil {
		t.Fatalf("newCLILoginCode() failed: %v", err)
	}
	if other == code {
		t.Error("newCLILoginCode() returned the same code twice")
	}
}

func TestNewCLILoginCodeRandomFailure(t *testing.T) {
	withFailingRandom(t)
	if _, _, err := newCLILoginCode(); err == nil {
		t.Error("newCLILoginCode() error = nil, want a random source error")
	}
}

func TestCodeHashPrefix(t *testing.T) {
	tests := []struct {
		name string
		hash []byte
		want string
	}{
		{name: "full hash", hash: hashCLILoginCode("code"), want: "5694d08a"},
		{name: "short hash", hash: []byte{0xab}, want: "ab"},
		{name: "empty hash", hash: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeHashPrefix(tt.hash); got != tt.want {
				t.Errorf("codeHashPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}
