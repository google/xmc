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

package experimenttest

import "testing"

func TestStudyConfig_ReturnsIndependentCopies(t *testing.T) {
	first := StudyConfig()
	first.Parameters[0].ParameterId = "mutated"
	if got := StudyConfig().GetParameters()[0].GetParameterId(); got == "mutated" {
		t.Errorf("StudyConfig() shares state between calls: parameters[0].parameter_id = %q", got)
	}
}
