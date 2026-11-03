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
package auth

import (
	"strings"

	"google.golang.org/protobuf/proto"
	xpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/pkg/auth/principal"
)

// NormalizeACLs returns a copy of acls with every user, service account, group and domain
// principal in canonical form (see principal.Normalize). acls is not modified; nil returns nil.
func NormalizeACLs(acls *xpb.XidAcls) *xpb.XidAcls {
	if acls == nil {
		return nil
	}
	normalized := proto.Clone(acls).(*xpb.XidAcls)
	for _, roles := range [][]*xpb.XidAcls_Role{normalized.GetOwners(), normalized.GetWriters(), normalized.GetReaders()} {
		for _, role := range roles {
			normalizeRolePrincipal(role)
		}
	}
	return normalized
}

// normalizeRolePrincipal rewrites an email or domain principal of role in canonical form in place.
// Principals without an identity string (e.g. all authenticated users) are left unchanged.
func normalizeRolePrincipal(role *xpb.XidAcls_Role) {
	switch p := role.GetPrincipal().(type) {
	case *xpb.XidAcls_Role_User:
		p.User = principal.Normalize(p.User)
	case *xpb.XidAcls_Role_ServiceAccount:
		p.ServiceAccount = principal.Normalize(p.ServiceAccount)
	case *xpb.XidAcls_Role_Group:
		p.Group = principal.Normalize(p.Group)
	case *xpb.XidAcls_Role_Domain:
		p.Domain = principal.Normalize(p.Domain)
	}
}

// principalsMatch reports whether two principals are equal in canonical form. An empty principal
// never matches, so an unset ACL field cannot authorize anyone.
func principalsMatch(a, b string) bool {
	canonical := principal.Normalize(a)
	return canonical != "" && canonical == principal.Normalize(b)
}

// emailDomain returns the canonical domain of an email address, or "" if it has none.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return principal.Normalize(email[at+1:])
}
