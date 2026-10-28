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

package server

import (
	"fmt"
	"os"
	"strings"

	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/dbconfig"
)

// xidLookupFilter returns a ListXidEntries filter that matches exactly the given XIDs.
//
// The XID service runs on the same backend as this server (both read DB_BACKEND from the shared
// db-config ConfigMap). On Spanner the Xids table is keyed by BitReversedXid, which is the field
// its query generator can serve with an index, so the filter uses bit_reversed_xid. The
// PostgreSQL Xids table is keyed by Xid directly and exposes no bit_reversed_xid field, so the
// filter uses xid there.
func xidLookupFilter(xids []int64) string {
	backend, err := dbconfig.ParseBackend(os.Getenv("DB_BACKEND"))
	usePlainXid := err == nil && backend == dbconfig.BackendPostgres
	terms := make([]string, 0, len(xids))
	for _, xid := range xids {
		if usePlainXid {
			terms = append(terms, fmt.Sprintf("xid=%d", xid))
			continue
		}
		terms = append(terms, fmt.Sprintf("bit_reversed_xid=%d", data.ReverseBits(xid)))
	}
	return strings.Join(terms, " OR ")
}
