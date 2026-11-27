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

package essclient

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/testing/protocmp"
	spb "xmanager_cloud/api/protos/experiment"
)

type stubTokenSource struct {
	tok *oauth2.Token
	err error
}

func (s stubTokenSource) Token() (*oauth2.Token, error) { return s.tok, s.err }

type fakeESSServer struct {
	spb.UnimplementedExperimentStateServiceServer
	gotAuth string
	gotName string
	resp    *spb.Experiment
	err     error
}

func (f *fakeESSServer) GetExperiment(ctx context.Context, req *spb.GetExperimentRequest) (*spb.Experiment, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			f.gotAuth = vals[0]
		}
	}
	f.gotName = req.GetName()
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestIDTokenCredentials(t *testing.T) {
	t.Parallel()

	factoryErr := errors.New("metadata server unavailable")
	tokenErr := errors.New("token mint failed")

	t.Run("rejects empty audience or nil factory", func(t *testing.T) {
		t.Parallel()
		if _, err := newIDTokenCredentials(context.Background(), "  ", idtoken.NewTokenSource); err == nil {
			t.Fatal("newIDTokenCredentials(empty audience) error = nil, want error")
		}
		if _, err := newIDTokenCredentials(context.Background(), "aud", nil); err == nil {
			t.Fatal("newIDTokenCredentials(nil factory) error = nil, want error")
		}
	})

	t.Run("does not require transport security", func(t *testing.T) {
		t.Parallel()
		creds, err := newIDTokenCredentials(context.Background(), "aud", idtoken.NewTokenSource)
		if err != nil {
			t.Fatalf("newIDTokenCredentials() error = %v", err)
		}
		if creds.RequireTransportSecurity() {
			t.Error("RequireTransportSecurity() = true, want false for in-cluster h2c")
		}
	})

	t.Run("lazy init, caches token source, and attaches bearer header", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		var gotAud string
		creds, err := newIDTokenCredentials(context.Background(), " my-aud ", func(_ context.Context, aud string, _ ...idtoken.ClientOption) (oauth2.TokenSource, error) {
			calls.Add(1)
			gotAud = aud
			return stubTokenSource{tok: &oauth2.Token{AccessToken: "id-tok-123"}}, nil
		})
		if err != nil {
			t.Fatalf("newIDTokenCredentials() error = %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("factory calls before RPC = %d, want 0 (lazy init)", calls.Load())
		}
		want := map[string]string{"authorization": "Bearer id-tok-123"}
		for i := range 2 {
			got, err := creds.GetRequestMetadata(context.Background())
			if err != nil {
				t.Fatalf("GetRequestMetadata() call %d error = %v", i, err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GetRequestMetadata() call %d mismatch (-want +got):\n%s", i, diff)
			}
		}
		if calls.Load() != 1 || gotAud != "my-aud" {
			t.Errorf("factory calls=%d aud=%q, want 1, %q", calls.Load(), gotAud, "my-aud")
		}
	})

	t.Run("retries factory after transient failure and propagates token errors", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		creds, err := newIDTokenCredentials(context.Background(), "aud", func(context.Context, string, ...idtoken.ClientOption) (oauth2.TokenSource, error) {
			if calls.Add(1) == 1 {
				return nil, factoryErr
			}
			return stubTokenSource{err: tokenErr}, nil
		})
		if err != nil {
			t.Fatalf("newIDTokenCredentials() error = %v", err)
		}
		if _, err := creds.GetRequestMetadata(context.Background()); !errors.Is(err, factoryErr) {
			t.Fatalf("first GetRequestMetadata() error = %v, want %v", err, factoryErr)
		}
		if _, err := creds.GetRequestMetadata(context.Background()); !errors.Is(err, tokenErr) {
			t.Fatalf("second GetRequestMetadata() error = %v, want %v", err, tokenErr)
		}
		if calls.Load() != 2 {
			t.Errorf("factory calls = %d, want 2", calls.Load())
		}
	})

	t.Run("rejects nil token source or empty/nil access token", func(t *testing.T) {
		t.Parallel()
		for _, ts := range []oauth2.TokenSource{nil, stubTokenSource{tok: nil}, stubTokenSource{tok: &oauth2.Token{AccessToken: ""}}} {
			creds, err := newIDTokenCredentials(context.Background(), "aud", func(context.Context, string, ...idtoken.ClientOption) (oauth2.TokenSource, error) {
				return ts, nil
			})
			if err != nil {
				t.Fatalf("newIDTokenCredentials() error = %v", err)
			}
			if _, err := creds.GetRequestMetadata(context.Background()); err == nil {
				t.Fatalf("GetRequestMetadata() with ts=%#v error = nil, want error", ts)
			}
		}
	})
}

func TestClient_NewValidationAndGetExperiment(t *testing.T) {
	t.Parallel()

	if _, err := New(context.Background(), "", "aud"); err == nil {
		t.Fatal("New(empty addr) error = nil, want error")
	}
	if _, err := New(context.Background(), "127.0.0.1:50051", ""); err == nil {
		t.Fatal("New(empty audience) error = nil, want error")
	}
	if err := (*Client)(nil).Close(); err != nil {
		t.Fatalf("(*Client)(nil).Close() error = %v, want nil", err)
	}
	if _, err := (*Client)(nil).GetExperiment(context.Background(), "experiments/1"); err == nil {
		t.Fatal("(*Client)(nil).GetExperiment() error = nil, want error")
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	wantExp := &spb.Experiment{
		Name:           "experiments/42",
		ExperimentType: spb.Experiment_STUDY,
	}
	fakeSrv := &fakeESSServer{resp: wantExp}
	grpcSrv := grpc.NewServer()
	spb.RegisterExperimentStateServiceServer(grpcSrv, fakeSrv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	client, err := newWithFactory(context.Background(), lis.Addr().String(), "test-aud", func(context.Context, string, ...idtoken.ClientOption) (oauth2.TokenSource, error) {
		return stubTokenSource{tok: &oauth2.Token{AccessToken: "live-token"}}, nil
	})
	if err != nil {
		t.Fatalf("newWithFactory() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close() error = %v", err)
		}
	}()

	got, err := client.GetExperiment(context.Background(), "experiments/42")
	if err != nil {
		t.Fatalf("GetExperiment() error = %v", err)
	}
	if diff := cmp.Diff(wantExp, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetExperiment() mismatch (-want +got):\n%s", diff)
	}
	if fakeSrv.gotAuth != "Bearer live-token" || fakeSrv.gotName != "experiments/42" {
		t.Errorf("server received auth=%q name=%q, want %q, %q", fakeSrv.gotAuth, fakeSrv.gotName, "Bearer live-token", "experiments/42")
	}
}
