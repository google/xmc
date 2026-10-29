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

import xpb "xmanager_cloud/api/protos/xid"

// userRole returns an ACL role granting access to a user principal.
func userRole(user string) *xpb.XidAcls_Role {
	return &xpb.XidAcls_Role{Principal: &xpb.XidAcls_Role_User{User: user}}
}

// saRole returns an ACL role granting access to a service account principal.
func saRole(serviceAccount string) *xpb.XidAcls_Role {
	return &xpb.XidAcls_Role{Principal: &xpb.XidAcls_Role_ServiceAccount{ServiceAccount: serviceAccount}}
}

// groupRole returns an ACL role granting access to a group principal.
func groupRole(group string) *xpb.XidAcls_Role {
	return &xpb.XidAcls_Role{Principal: &xpb.XidAcls_Role_Group{Group: group}}
}

// domainRole returns an ACL role granting access to a domain principal.
func domainRole(domain string) *xpb.XidAcls_Role {
	return &xpb.XidAcls_Role{Principal: &xpb.XidAcls_Role_Domain{Domain: domain}}
}
