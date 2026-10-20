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
	"slices"
)

// sharedPublicClientIDs are Google OAuth client IDs shipped inside public tools. Any Google user
// can mint an ID token for them (e.g. `gcloud auth print-identity-token`), so accepting them as an
// audience would let the email allowlist / domain / IAM admission be the only gate between the
// deployment and every Google account. Deployers must register their own OAuth client instead.
var sharedPublicClientIDs = map[string]string{
	"32555940559.apps.googleusercontent.com":                                   "the Google Cloud SDK (gcloud) client",
	"764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com": "the gcloud application-default credentials client",
}

// parseAdditionalAudiences parses the comma/space separated ADDITIONAL_AUDIENCES value into a
// deduplicated list in first-seen order. It rejects shared public Google OAuth clients.
func parseAdditionalAudiences(val string) ([]string, error) {
	var audiences []string
	for _, aud := range parseCommaSpaceSeparated(val) {
		if tool, ok := sharedPublicClientIDs[aud]; ok {
			return nil, fmt.Errorf("`ADDITIONAL_AUDIENCES` must not contain %q (%s): ID tokens for shared public clients can be minted by any Google user, so register an OAuth client in your own project and list its client ID instead", aud, tool)
		}
		if !slices.Contains(audiences, aud) {
			audiences = append(audiences, aud)
		}
	}
	return audiences, nil
}

// AcceptedAudiences returns every ID token audience a service accepts: the service-specific base
// audiences (e.g. IAP audiences), then ClientID, then AdditionalAudiences. base and ClientID are
// passed through unchanged, including empty values, so authorizers keep failing fast at startup
// when a required audience is unset; callers that tolerate an unset audience must filter it.
// AdditionalAudiences entries already present are skipped, so the order is deterministic. It is
// the single source of the audience list for the HTTP middleware, gRPC authorizers, and JWKS
// validators.
func (c *Config) AcceptedAudiences(base ...string) []string {
	audiences := append(slices.Clone(base), c.ClientID)
	for _, aud := range c.AdditionalAudiences {
		if aud != "" && !slices.Contains(audiences, aud) {
			audiences = append(audiences, aud)
		}
	}
	return audiences
}
