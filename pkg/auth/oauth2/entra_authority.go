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
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// entraHosts are the Microsoft Entra ID sign-in and token issuer hosts of the public and
// sovereign clouds, including the legacy aliases of login.microsoftonline.com and the v1 token
// issuer host (sts.windows.net).
var entraHosts = []string{
	"login.microsoftonline.com",
	"login.microsoft.com",
	"login.windows.net",
	"sts.windows.net",
	"login.microsoftonline.us",
	"login.partner.microsoftonline.cn",
	"login.chinacloudapi.cn",
}

// entraMultiTenantAuthorities are the Entra ID tenant segments that accept accounts from any
// directory instead of a single tenant.
var entraMultiTenantAuthorities = []string{"common", "organizations", "consumers"}

// entraTenant returns the lowercased tenant segment of a Microsoft Entra ID URL such as
// https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize. ok is false
// when rawURL is not an Entra ID URL.
func entraTenant(rawURL string) (tenant string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !slices.Contains(entraHosts, strings.ToLower(u.Hostname())) {
		return "", false
	}
	tenant, _, _ = strings.Cut(strings.TrimLeft(u.Path, "/"), "/")
	return strings.ToLower(tenant), true
}

// validateEntraAuthority refuses multi-tenant Microsoft Entra ID authorities: they sign in
// accounts of any directory, whose addresses are controlled by that directory's administrators.
func validateEntraAuthority(rawURL string) error {
	tenant, ok := entraTenant(rawURL)
	if !ok || !slices.Contains(entraMultiTenantAuthorities, tenant) {
		return nil
	}
	return fmt.Errorf("multi-tenant Microsoft Entra ID authority %q is not supported; use your tenant's authority", tenant)
}
