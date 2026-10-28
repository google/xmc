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
	"fmt"
	"strings"
)

// celStringEscaper escapes the characters that terminate or escape a single-quoted CEL string
// literal. Member emails are validated with validatePlainEmail before a query is built, which
// already excludes backslashes and leaves apostrophes as the only special character, so escaping
// is redundant with validation; it is kept as defense in depth.
var celStringEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// transitiveMembershipQuery returns the CEL query that checks transitive group membership of
// memberEmail, with memberEmail escaped as a single-quoted CEL string literal. Callers must
// validate memberEmail with validatePlainEmail first.
func transitiveMembershipQuery(memberEmail string) string {
	return fmt.Sprintf("member_key.id == '%s'", celStringEscaper.Replace(memberEmail))
}
