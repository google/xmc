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

package bridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// allowAllProbeDials disables the dial guard so tests can reach loopback httptest servers.
func allowAllProbeDials(string, string, syscall.RawConn) error { return nil }

func TestIsBlockedProbeAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"IPv4 loopback", "127.0.0.1", true},
		{"IPv4 loopback range", "127.5.5.5", true},
		{"IPv6 loopback", "::1", true},
		{"IPv4-mapped loopback", "::ffff:127.0.0.1", true},
		{"GCE metadata IPv4", "169.254.169.254", true},
		{"IPv4-mapped GCE metadata", "::ffff:169.254.169.254", true},
		{"GCE metadata IPv6", "fd20:ce::254", true},
		{"zoned GCE metadata IPv6", "fd20:ce::254%eth0", true},
		{"AWS IMDS IPv6", "fd00:ec2::254", true},
		{"OCI IMDS IPv4", "192.0.0.192", true},
		{"IPv4 link-local", "169.254.1.1", true},
		{"IPv6 link-local", "fe80::1", true},
		{"IPv4 unspecified", "0.0.0.0", true},
		{"IPv4 this-network", "0.1.2.3", true},
		{"IPv6 unspecified", "::", true},
		{"IPv4 multicast", "224.0.0.1", true},
		{"IPv6 link-local multicast", "ff02::1", true},
		{"IPv6 interface-local multicast", "ff01::1", true},
		{"IPv6 global multicast", "ff0e::1", true},
		{"in-cluster pod IP", "10.0.0.5", false},
		{"in-cluster service IP", "10.96.0.10", false},
		{"private 172.16/12", "172.18.0.2", false},
		{"private 192.168/16", "192.168.1.1", false},
		{"public IPv4", "8.8.8.8", false},
		{"public IPv6", "2001:db8::1", false},
		{"other IPv6 ULA", "fd00::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockedProbeAddr(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Errorf("isBlockedProbeAddr(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
	t.Run("invalid address", func(t *testing.T) {
		if !isBlockedProbeAddr(netip.Addr{}) {
			t.Error("isBlockedProbeAddr(zero Addr) = false, want true")
		}
	})
}

func TestRejectBlockedProbeAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"metadata server", "169.254.169.254:80", true},
		{"zoned IPv6 link-local", "[fe80::1%eth0]:80", true},
		{"zoned GCE metadata IPv6", "[fd20:ce::254%eth0]:80", true},
		{"OCI IMDS IPv4", "192.0.0.192:80", true},
		{"IPv6 loopback", "[::1]:443", true},
		{"missing port", "10.0.0.1", true},
		{"unresolved hostname", "metadata.google.internal:80", true},
		{"in-cluster IPv4", "10.0.0.1:443", false},
		{"public IPv6", "[2001:db8::1]:443", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectBlockedProbeAddress("tcp", tt.address, nil)
			if gotErr := errors.Is(err, errBlockedProbeAddress); gotErr != tt.wantErr {
				t.Errorf("rejectBlockedProbeAddress(%q) error = %v, wantErr %v", tt.address, err, tt.wantErr)
			}
		})
	}
}

func TestGuardDialer_ChainsExistingHooks(t *testing.T) {
	errInner := errors.New("inner hook")
	tests := []struct {
		name    string
		dialer  *net.Dialer
		address string
		wantErr error
	}{
		{"blocked address skips inner hooks", &net.Dialer{ControlContext: func(context.Context, string, string, syscall.RawConn) error { return errInner }}, "127.0.0.1:80", errBlockedProbeAddress},
		{"allowed address runs ControlContext", &net.Dialer{ControlContext: func(context.Context, string, string, syscall.RawConn) error { return errInner }}, "10.0.0.1:80", errInner},
		{"allowed address runs legacy Control", &net.Dialer{Control: func(string, string, syscall.RawConn) error { return errInner }}, "10.0.0.1:80", errInner},
		{"allowed address without inner hooks", &net.Dialer{}, "10.0.0.1:80", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guarded := guardDialer(tt.dialer, rejectBlockedProbeAddress)
			if guarded.Control != nil {
				t.Error("guardDialer() left legacy Control set, which ControlContext would shadow")
			}
			if err := guarded.ControlContext(context.Background(), "tcp", tt.address, nil); !errors.Is(err, tt.wantErr) {
				t.Errorf("ControlContext(%q) error = %v, want %v", tt.address, err, tt.wantErr)
			}
		})
	}
}

func TestGuardDialer_BlocksResolvedLoopbackHostname(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("failed to split listener address: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := guardDialer(&net.Dialer{}, rejectBlockedProbeAddress).DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err == nil {
		_ = conn.Close()
	}
	if !errors.Is(err, errBlockedProbeAddress) {
		t.Errorf("DialContext(localhost) error = %v, want %v", err, errBlockedProbeAddress)
	}
}

func TestNetworkHealthProber_DoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirector.Close()

	for name, opts := range map[string][]HealthProberOption{
		"default client": {withProbeDialControl(allowAllProbeDials)},
		"custom client":  {withProbeDialControl(allowAllProbeDials), WithHealthHTTPClient(redirector.Client())},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := NewNetworkHealthProber(opts...).ProbeHTTP(context.Background(), redirector.URL, 2*time.Second)
			if err != nil {
				t.Fatalf("ProbeHTTP() unexpected error: %v", err)
			}
			want := &HealthProbeResult{Healthy: true, StatusCode: http.StatusFound, Message: "HTTP 302"}
			if diff := cmp.Diff(want, res, cmp.FilterPath(func(p cmp.Path) bool { return p.Last().String() == ".Latency" }, cmp.Ignore())); diff != "" {
				t.Errorf("ProbeHTTP() mismatch (-want +got):\n%s", diff)
			}
			if hits := targetHits.Load(); hits != 0 {
				t.Errorf("redirect target received %d requests, want 0", hits)
			}
		})
	}
}

func TestGuardHTTPClient_IgnoresCustomTLSDialers(t *testing.T) {
	var customDials atomic.Int32
	bypass := func(context.Context, string, string) (net.Conn, error) {
		customDials.Add(1)
		return nil, errors.New("custom TLS dialer used")
	}
	transport := &http.Transport{
		DialTLSContext: bypass,
		DialTLS:        func(network, addr string) (net.Conn, error) { return bypass(context.Background(), network, addr) },
	}
	client := guardHTTPClient(&http.Client{Transport: transport, Timeout: 5 * time.Second}, guardDialer(&net.Dialer{}, rejectBlockedProbeAddress))

	resp, err := client.Get("https://127.0.0.1:1/")
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, errBlockedProbeAddress) {
		t.Errorf("GET https://127.0.0.1:1/ error = %v, want errBlockedProbeAddress", err)
	}
	if got := customDials.Load(); got != 0 {
		t.Errorf("custom TLS dialer calls = %d, want 0 (HTTPS must dial through the guard)", got)
	}
}

func TestGuardHTTPClient_Transport(t *testing.T) {
	dialer := guardDialer(&net.Dialer{}, rejectBlockedProbeAddress)

	got := guardHTTPClient(nil, dialer)
	transport, ok := got.Transport.(*http.Transport)
	if !ok || transport == http.DefaultTransport {
		t.Fatalf("guardHTTPClient(nil) transport = %T (default shared: %v), want a cloned *http.Transport", got.Transport, transport == http.DefaultTransport)
	}
	if transport.Proxy != nil {
		t.Error("guardHTTPClient(nil) kept a proxy, which would bypass the dial guard")
	}
	if transport.DialContext == nil {
		t.Error("guardHTTPClient(nil) did not install the guarded DialContext")
	}

	rt := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	custom := guardHTTPClient(&http.Client{Transport: rt}, dialer)
	if _, ok := custom.Transport.(roundTripFunc); !ok || custom.CheckRedirect == nil {
		t.Errorf("guardHTTPClient(custom) = {Transport: %T, CheckRedirect set: %v}, want original RoundTripper with CheckRedirect", custom.Transport, custom.CheckRedirect != nil)
	}
}
