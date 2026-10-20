# Copyright 2025 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
{{/*
Expand the name of the chart.
*/}}
{{- define "xmanager-cloud.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "xmanager-cloud.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "xmanager-cloud.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "xmanager-cloud.labels" -}}
helm.sh/chart: {{ include "xmanager-cloud.chart" . }}
{{ include "xmanager-cloud.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "xmanager-cloud.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xmanager-cloud.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "xmanager-cloud.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "xmanager-cloud.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Get the UI hostname.
*/}}
{{- define "xmanager-cloud.uiHostname" -}}
{{- if .Values.xmcUrl -}}
{{- .Values.xmcUrl -}}
{{- else -}}
{{- .Values.gateway.uiHostname -}}
{{- end -}}
{{- end -}}

{{/*
Get the API hostname.
*/}}
{{- define "xmanager-cloud.apiHostname" -}}
{{- if .Values.xmcUrl -}}
{{- printf "api.%s" .Values.xmcUrl -}}
{{- else -}}
{{- .Values.gateway.apiHostname -}}
{{- end -}}
{{- end -}}

{{/*
Get the gRPC hostname.
*/}}
{{- define "xmanager-cloud.grpcHostname" -}}
{{- if .Values.xmcUrl -}}
{{- printf "grpc.%s" .Values.xmcUrl -}}
{{- else -}}
{{- .Values.gateway.grpcHostname -}}
{{- end -}}
{{- end -}}

{{/*
Get the UI API URL.
*/}}
{{- define "xmanager-cloud.uiApiUrl" -}}
{{- if .Values.xmcUrl -}}
{{- printf "https://%s/v1" .Values.xmcUrl -}}
{{- else -}}
{{- .Values.ui.apiUrl -}}
{{- end -}}
{{- end -}}

{{/*
Common OAuth environment variables for XMC services.
*/}}
{{- define "xmanager-cloud.oauthEnv" -}}
- name: SESSION_TTL
  value: {{ .Values.auth.sessionTtl | quote }}
- name: CLI_SESSION_TTL
  value: {{ .Values.auth.cliSessionTtl | quote }}
- name: MOCK_AUTH
  value: {{ .Values.auth.mockAuth | quote }}
- name: ALLOWED_ORIGINS
  value: {{ .Values.auth.allowedOrigins | quote }}
- name: ALLOWED_DOMAINS
  value: {{ .Values.auth.allowedDomains | quote }}
- name: ALLOWED_EMAILS
  value: {{ .Values.auth.allowedEmails | quote }}
- name: DISABLE_AUTHENTICATION
  value: {{ .Values.auth.disableAuthentication | default "false" | quote }}
- name: TRUST_X_FORWARDED_PROTO
  value: {{ .Values.auth.trustXForwardedProto | quote }}
- name: CLIENT_ID
  value: {{ .Values.auth.iap.clientId | quote }}
- name: ADDITIONAL_AUDIENCES
  value: {{ .Values.auth.additionalAudiences | default (list) | join "," | quote }}
- name: IAP_EXPECTED_ISSUER
  value: {{ .Values.auth.iap.iapIssuer | quote }}
- name: GCP_PROJECT_ID
  value: {{ .Values.projectId | quote }}
- name: AUTHORIZED_IAM_ROLE
  value: {{ .Values.auth.authorizedIamRole | quote }}
- name: ADMIN_IAM_ROLE
  value: {{ .Values.auth.adminIamRole | default "roles/owner" | quote }}
- name: JWT_SECRET_FILE
  value: "/etc/gcp-secrets/jwt-secret"
- name: CLIENT_SECRET_FILE
  value: "/etc/gcp-secrets/client-secret"
- name: REDIRECT_URL
  value: {{ .Values.auth.redirectUrl | default (printf "https://%s/api/callback" (include "xmanager-cloud.apiHostname" .)) | quote }}
- name: FRONTEND_URL
  value: {{ .Values.auth.frontendUrl | default (printf "https://%s" (include "xmanager-cloud.uiHostname" .)) | quote }}
{{ if .Values.auth.authUrl }}
- name: AUTH_URL
  value: {{ .Values.auth.authUrl | quote }}
{{ end }}
{{ if .Values.auth.tokenUrl }}
- name: TOKEN_URL
  value: {{ .Values.auth.tokenUrl | quote }}
{{ end }}
{{ if .Values.auth.apiUrl }}
- name: API_URL
  value: {{ .Values.auth.apiUrl | quote }}
{{ end }}
{{ if hasKey .Values.auth "secureCookie" }}
- name: SECURE_COOKIE
  value: {{ .Values.auth.secureCookie | quote }}
{{ end }}
{{ if .Values.isDevEnvironment }}
{{ if .Values.auth.username }}
- name: OIDC_USERNAME
  value: {{ .Values.auth.username | quote }}
{{ end }}
{{ if .Values.auth.password }}
- name: OIDC_PASSWORD
  value: {{ .Values.auth.password | quote }}
{{ end }}
{{ if .Values.auth.issuerUrl }}
- name: OIDC_ISSUER_URL
  value: {{ .Values.auth.issuerUrl | quote }}
{{ end }}
{{ end }}
{{ if or .Values.auth.googleApplicationCredentials .Values.googleApplicationCredentials }}
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: {{ default .Values.googleApplicationCredentials .Values.auth.googleApplicationCredentials | quote }}
{{ end }}
{{- end -}}

{{/*
ClickHouse connection environment variables for XMC services backed by the metrics pipeline.
Consumed by pkg/clickhouse.GetOptions. The password follows the same two paths as the JWT and
OAuth client secrets above: with the CSI driver enabled it is read from the file Secret Manager
mounts, and otherwise from the clickhouse-secret Secret rendered by
templates/metrics/clickhouse-secret.yaml out of a Helm value.
*/}}
{{- define "xmanager-cloud.clickhouseEnv" -}}
{{- $clickhouse := (default (dict) .Values.metrics.clickhouse) -}}
- name: CLICKHOUSE_HOST
  value: {{ $clickhouse.host | default "clickhouse.xmc-metrics.svc.cluster.local" | quote }}
- name: CLICKHOUSE_PORT
  value: {{ $clickhouse.port | default "9000" | quote }}
- name: CLICKHOUSE_DATABASE
  value: {{ $clickhouse.database | default "default" | quote }}
- name: CLICKHOUSE_USER
  value: {{ $clickhouse.user | default "default" | quote }}
{{- if .Values.secrets.csi.enabled }}
- name: CLICKHOUSE_PASSWORD_FILE
  value: "/etc/gcp-secrets/clickhouse-password"
{{- else }}
- name: CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: clickhouse-secret
      key: clickhouse-password
{{- end }}
{{- end -}}

{{/*
Common GCP Secrets Volume definition.
*/}}
{{- define "xmanager-cloud.gcpSecretsVolume" -}}
{{- if .Values.secrets.csi.enabled }}
- name: gcp-secrets-volume
  csi:
    driver: secrets-store-gke.csi.k8s.io
    readOnly: true
    volumeAttributes:
      secretProviderClass: "gcp-secrets-provider"
{{- else }}
- name: gcp-secrets-volume
  projected:
    sources:
    - secret:
        name: backend-oauth-secrets
    {{- $auth := default (dict) .Values.auth }}
    {{- $gac := default .Values.googleApplicationCredentials $auth.googleApplicationCredentials }}
    {{- if $gac }}
    - secret:
        name: gcp-sa-key
        items:
        - key: key.json
          path: key.json
    {{- end }}
{{- end }}
{{- end -}}
