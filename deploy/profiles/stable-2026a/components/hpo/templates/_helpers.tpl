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
Resolve the xmc-study-controller container image:
1. .Values.hpoDeployment.image, the image xmc-bridge resolves for the xmc-cmp-hpo release
   (appending the tag only if it is untagged and not a digest)
2. us-docker.pkg.dev/xmc-prod/xmc/study-controller with (.Values.global.imageTag | .Values.imageTag | "latest")
*/}}
{{- define "hpo.image" -}}
{{- $hpo := .Values.hpoDeployment | default dict -}}
{{- $global := .Values.global | default dict -}}
{{- $tag := $global.imageTag | default .Values.imageTag | default "latest" -}}
{{- if $hpo.image -}}
  {{- $img := $hpo.image | toString -}}
  {{- if or (contains "@sha256:" $img) (regexMatch ":[^:/]+$" $img) -}}
    {{- $img -}}
  {{- else -}}
    {{- printf "%s:%s" $img $tag -}}
  {{- end -}}
{{- else -}}
  {{- printf "us-docker.pkg.dev/xmc-prod/xmc/study-controller:%s" $tag -}}
{{- end -}}
{{- end -}}
