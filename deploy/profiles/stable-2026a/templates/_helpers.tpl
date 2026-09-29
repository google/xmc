{{/*
Copyright 2026 Google LLC
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
xmc-profile.mergeInto deep-merges src into dst in place: (list dst src).
Keys in src always win, including explicit false, 0 and "" values, which sprig
merge/mergeOverwrite silently drop. Nested maps merge recursively; lists and
scalars are replaced. Maps copied from src are deep-copied so dst never aliases a
.Values map. Lists are assigned by reference and so remain shared with .Values;
this is harmless because component templates never mutate them.
*/}}
{{- define "xmc-profile.mergeInto" -}}
{{- $dst := index . 0 -}}
{{- range $key, $val := index . 1 -}}
{{- if and (kindIs "map" $val) (kindIs "map" (index $dst $key)) -}}
{{- include "xmc-profile.mergeInto" (list (index $dst $key) $val) -}}
{{- else if kindIs "map" $val -}}
{{- $_ := set $dst $key (deepCopy $val) -}}
{{- else -}}
{{- $_ := set $dst $key $val -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
xmc-profile.readFile returns the content of a chart file, which may be empty,
or fails when the file is missing (for example when .helmignore excludes it):
(list $ path). Helm 3 .Files has no Has method, so existence is checked with
Glob on the literal path.
*/}}
{{- define "xmc-profile.readFile" -}}
{{- $root := index . 0 -}}
{{- $path := index . 1 -}}
{{- if not ($root.Files.Glob $path) -}}
{{- fail (printf "xmc-profile: required file %q is missing from the profile chart" $path) -}}
{{- end -}}
{{- $root.Files.Get $path -}}
{{- end -}}

{{/*
xmc-profile.component renders the Component CR of one catalog component:
(list $ name). Values resolve like a Helm sub-chart:
  components/<name>/values.yaml <- {global: .Values.global} <- .Values.components.<name>

Invariant for components/<name>/templates/component.yaml authors: the template
is rendered through tpl with only .Values, .Chart (Name, Version, AppVersion),
.Release, .Capabilities and .Template in scope. It must not include sub-chart
named templates (_*.tpl) or use .Files, because those are not loaded here.
*/}}
{{- define "xmc-profile.component" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- $dir := printf "components/%s" $name -}}
{{- $meta := include "xmc-profile.readFile" (list $root (printf "%s/Chart.yaml" $dir)) | fromYaml -}}
{{- $values := include "xmc-profile.readFile" (list $root (printf "%s/values.yaml" $dir)) | fromYaml | default dict -}}
{{- $body := include "xmc-profile.readFile" (list $root (printf "%s/templates/component.yaml" $dir)) -}}
{{- include "xmc-profile.mergeInto" (list $values (dict "global" ($root.Values.global | default dict))) -}}
{{- include "xmc-profile.mergeInto" (list $values (index ($root.Values.components | default dict) $name | default dict)) -}}
{{- $chart := dict "Name" $meta.name "Version" $meta.version "AppVersion" ($meta.appVersion | default "") -}}
{{- $template := dict "Name" (printf "%s/%s/templates/component.yaml" $root.Chart.Name $dir) "BasePath" $root.Template.BasePath -}}
{{- tpl $body (dict "Values" $values "Chart" $chart "Release" $root.Release "Capabilities" $root.Capabilities "Template" $template) -}}
{{- end -}}
