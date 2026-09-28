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

// Package stable2026a provides embedded 1P component Helm charts for the stable-2026a profile.
package stable2026a

import "embed"

// EmbeddedCharts embeds the 1P native component Helm sub-charts under components/.
//
//go:embed components/ingestion-gateway/Chart.yaml components/ingestion-gateway/values.yaml components/ingestion-gateway/templates/*
var EmbeddedCharts embed.FS
