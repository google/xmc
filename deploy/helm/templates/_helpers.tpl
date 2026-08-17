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
{{- if .Values.auth.authUrl }}
- name: AUTH_URL
  value: {{ .Values.auth.authUrl | quote }}
{{- end }}
{{- if .Values.auth.tokenUrl }}
- name: TOKEN_URL
  value: {{ .Values.auth.tokenUrl | quote }}
{{- end }}
{{- if .Values.auth.googleApplicationCredentials }}
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: {{ .Values.auth.googleApplicationCredentials | quote }}
{{- end }}
{{- if .Values.auth.apiUrl }}
- name: API_URL
  value: {{ .Values.auth.apiUrl | quote }}
{{- end }}
{{- if hasKey .Values.auth "secureCookie" }}
- name: SECURE_COOKIE
  value: {{ .Values.auth.secureCookie | quote }}
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
  secret:
    secretName: backend-oauth-secrets
{{- end }}
{{- end -}}
