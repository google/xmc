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
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"syscall"
)

// errBlockedProbeAddress reports a health probe dial to an address outside the allowed probe scope.
var errBlockedProbeAddress = errors.New("health probe destination address is blocked")

// probeDialControl matches the net.Dialer Control hook signature.
type probeDialControl func(network, address string, c syscall.RawConn) error

var (
	// metadataServerAddrs are cloud metadata server addresses that the link-local check does not
	// cover: the GCE IPv6 address fd20:ce::254 (IPv6-only VMs), and the AWS IMDS IPv6 address
	// fd00:ec2::254. 169.254.169.254 is listed for clarity, though link-local already covers it.
	// metadata.google.internal resolves to these addresses.
	metadataServerAddrs = []netip.Addr{
		netip.MustParseAddr("169.254.169.254"),
		netip.MustParseAddr("fd20:ce::254"),
		netip.MustParseAddr("fd00:ec2::254"),
	}
	// thisNetworkPrefix is 0.0.0.0/8, which Linux routes to the local host.
	thisNetworkPrefix = netip.MustParsePrefix("0.0.0.0/8")
)

// isBlockedProbeAddr reports whether health probes must not connect to addr: loopback, link-local
// (including 169.254.0.0/16 and fe80::/10), unspecified, multicast, and cloud metadata servers.
// Private ranges such as 10.0.0.0/8 stay allowed because probed 3P services run in-cluster.
func isBlockedProbeAddr(addr netip.Addr) bool {
	// Drop any IPv6 zone: netip.Addr equality includes it, and Linux ignores the scope ID for
	// non-link-local destinations, so "fd20:ce::254%eth0" still reaches the metadata server.
	addr = addr.Unmap().WithZone("")
	return !addr.IsValid() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		thisNetworkPrefix.Contains(addr) || slices.Contains(metadataServerAddrs, addr)
}

// rejectBlockedProbeAddress is a net.Dialer Control hook that runs after DNS resolution, so it
// checks the IP actually being connected to and defeats DNS rebinding.
func rejectBlockedProbeAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %w", errBlockedProbeAddress, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: %w", errBlockedProbeAddress, err)
	}
	if isBlockedProbeAddr(addr) {
		return fmt.Errorf("%w: %s", errBlockedProbeAddress, addr)
	}
	return nil
}

// guardDialer returns a copy of d that runs control before any Control or ControlContext hook of d.
func guardDialer(d *net.Dialer, control probeDialControl) *net.Dialer {
	guarded := *d
	inner, innerLegacy := d.ControlContext, d.Control
	guarded.Control = nil
	guarded.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
		if err := control(network, address, c); err != nil {
			return err
		}
		if inner != nil {
			return inner(ctx, network, address, c)
		}
		if innerLegacy != nil {
			return innerLegacy(network, address, c)
		}
		return nil
	}
	return &guarded
}

// guardHTTPClient returns a copy of c that never follows redirects and, when its transport is an
// *http.Transport (the default), dials through d without a proxy so the dial guard sees the target.
// It also clears the transport's Dial, DialTLS and DialTLSContext hooks, so HTTPS connections are
// dialed through d too.
func guardHTTPClient(c *http.Client, d *net.Dialer) *http.Client {
	guarded := http.Client{}
	if c != nil {
		guarded = *c
	}
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if guarded.Transport == nil {
		guarded.Transport = http.DefaultTransport
	}
	if t, ok := guarded.Transport.(*http.Transport); ok {
		t = t.Clone()
		t.Proxy = nil
		// Every connection, including the TCP connection under TLS, goes through the guarded dialer:
		// a custom DialTLSContext or DialTLS would otherwise dial HTTPS targets unguarded.
		t.DialContext = d.DialContext
		t.Dial = nil
		t.DialTLS = nil
		t.DialTLSContext = nil
		guarded.Transport = t
	}
	return &guarded
}
