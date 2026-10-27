# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

{{/*
Resolve the container image for ingestion-gateway:
1. Explicit .Values.ingestionGatewayDeployment.image or .Values.parameters.image (appending tag only if untagged and not a digest)
2. (.Values.ingestionGatewayDeployment.imageRepository | .Values.parameters.imageRepository | .Values.image.repository) with (.Values.global.imageTag | .Values.imageTag | .Values.image.tag | "latest")
*/}}
{{- define "ingestion-gateway.image" -}}
{{- $ig := .Values.ingestionGatewayDeployment | default dict -}}
{{- $params := .Values.parameters | default dict -}}
{{- $imgCfg := .Values.image | default dict -}}
{{- $global := .Values.global | default dict -}}
{{- $tag := $global.imageTag | default .Values.imageTag | default $imgCfg.tag | default "latest" -}}
{{- $repo := $ig.imageRepository | default $params.imageRepository | default $imgCfg.repository | default "us-docker.pkg.dev/xmc-prod/xmc/ingestion-gateway" -}}
{{- $explicitImg := $ig.image | default $params.image -}}
{{- if $explicitImg -}}
  {{- $igImg := $explicitImg | toString -}}
  {{- if or (contains "@sha256:" $igImg) (regexMatch ":[^:/]+$" $igImg) -}}
    {{- $igImg -}}
  {{- else -}}
    {{- printf "%s:%s" $igImg $tag -}}
  {{- end -}}
{{- else -}}
  {{- printf "%s:%s" $repo $tag -}}
{{- end -}}
{{- end -}}
