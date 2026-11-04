{{/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/}}

{{/* Labels selecting the Vizier server pods of this release. */}}
{{- define "vizier.selectorLabels" -}}
app.kubernetes.io/name: vizier
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end }}

{{/* Labels common to every resource of this release. */}}
{{- define "vizier.labels" -}}
{{ include "vizier.podLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}

{{/*
Labels of the Vizier server pod. They omit the chart version so that a chart-only bump does not
recreate the pod, which the Recreate strategy would turn into downtime.
*/}}
{{- define "vizier.podLabels" -}}
{{ include "vizier.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/* Validated database.type: "sqlite" or "postgres". */}}
{{- define "vizier.databaseType" -}}
{{- $type := .Values.database.type | default "sqlite" }}
{{- if not (has $type (list "sqlite" "postgres")) }}
{{- fail (printf `database.type must be "sqlite" or "postgres", got %q` $type) }}
{{- end }}
{{- $type }}
{{- end }}
