{{/*
Copyright 2026 GENYEE
SPDX-License-Identifier: MIT

GENYEE Helm Chart Template Helpers

Best Practices References:
- https://helm.sh/docs/chart_best_practices/templates/
- https://github.com/argoproj/argo-helm/blob/main/charts/argo-cd/templates/_helpers.tpl
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "genyee.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "genyee.fullname" -}}
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
Resolve a component Service name while preserving names used by existing releases.
Usage: include "genyee.serviceName" (dict "component" "postgresql" "context" .)
*/}}
{{- define "genyee.serviceName" -}}
{{- $service := (index .context.Values .component).service | default dict -}}
{{- $legacyName := .component -}}
{{- if eq .component "postgresql" -}}
{{- $legacyName = "postgres" -}}
{{- end -}}
{{- default $legacyName $service.name -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
Ref: https://helm.sh/docs/chart_best_practices/labels/
*/}}
{{- define "genyee.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels following Kubernetes recommended labels.
Ref: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
*/}}
{{- define "genyee.labels" -}}
helm.sh/chart: {{ include "genyee.chart" . }}
{{ include "genyee.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: genyee
{{- end }}

{{/*
Selector labels
*/}}
{{- define "genyee.selectorLabels" -}}
app.kubernetes.io/name: {{ include "genyee.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Component labels - use for individual components
Usage: {{ include "genyee.componentLabels" (dict "component" "app" "context" .) }}
*/}}
{{- define "genyee.componentLabels" -}}
{{ include "genyee.labels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Component selector labels
Usage: {{ include "genyee.componentSelectorLabels" (dict "component" "app" "context" .) }}
*/}}
{{- define "genyee.componentSelectorLabels" -}}
{{ include "genyee.selectorLabels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Create the name of the service account to use.
Ref: https://helm.sh/docs/chart_best_practices/rbac/
*/}}
{{- define "genyee.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "genyee.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name - supports existing secret
*/}}
{{- define "genyee.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- include "genyee.fullname" . }}-secrets
{{- end }}
{{- end }}

{{/*
Format an image reference with optional global.imageRegistry prefix.
Usage: include "genyee.formatImage" (dict "repository" .Values.app.image.repository "tag" $tag "global" .Values.global)
*/}}
{{- define "genyee.formatImage" -}}
{{- $registry := "" -}}
{{- if and .global .global.imageRegistry -}}
{{- $registry = trimSuffix "/" (toString .global.imageRegistry) -}}
{{- end -}}
{{- $rawRepo := toString .repository -}}
{{- $rawTag := toString .tag -}}
{{- if or (regexMatch "[[:space:]\"']" $registry) (regexMatch "[[:space:]\"']" $rawRepo) (regexMatch "[[:space:]\"']" $rawTag) -}}
{{- fail "invalid image reference: registry, repository, and tag must not contain whitespace or quotes" -}}
{{- end -}}
{{- if $registry -}}
{{- $repository := $rawRepo -}}
{{- if .selfBuilt -}}
{{- $repository = base $repository -}}
{{- end -}}
{{- printf "%s/%s:%s" $registry $repository $rawTag -}}
{{- else -}}
{{- printf "%s:%s" $rawRepo $rawTag -}}
{{- end -}}
{{- end }}

{{/*
Return the app image with tag.
Defaults to Chart.appVersion if tag is not specified.
*/}}
{{- define "genyee.app.image" -}}
{{- $tag := default .Chart.AppVersion .Values.app.image.tag }}
{{- include "genyee.formatImage" (dict "repository" .Values.app.image.repository "tag" $tag "global" .Values.global "selfBuilt" true) }}
{{- end }}

{{/*
Return the frontend image with tag.
Defaults to Chart.appVersion if tag is not specified.
*/}}
{{- define "genyee.frontend.image" -}}
{{- $tag := default .Chart.AppVersion .Values.frontend.image.tag }}
{{- include "genyee.formatImage" (dict "repository" .Values.frontend.image.repository "tag" $tag "global" .Values.global "selfBuilt" true) }}
{{- end }}

{{/*
Return the docreader image with tag.
Defaults to Chart.appVersion if tag is not specified.
*/}}
{{- define "genyee.docreader.image" -}}
{{- $tag := default .Chart.AppVersion .Values.docreader.image.tag }}
{{- include "genyee.formatImage" (dict "repository" .Values.docreader.image.repository "tag" $tag "global" .Values.global "selfBuilt" true) }}
{{- end }}

{{/*
Return the PostgreSQL image with tag.
*/}}
{{- define "genyee.postgresql.image" -}}
{{- include "genyee.formatImage" (dict "repository" .Values.postgresql.image.repository "tag" .Values.postgresql.image.tag "global" .Values.global) }}
{{- end }}

{{/*
Return the Redis image with tag.
*/}}
{{- define "genyee.redis.image" -}}
{{- include "genyee.formatImage" (dict "repository" .Values.redis.image.repository "tag" .Values.redis.image.tag "global" .Values.global) }}
{{- end }}

{{/*
Return the Neo4j image with tag.
*/}}
{{- define "genyee.neo4j.image" -}}
{{- include "genyee.formatImage" (dict "repository" .Values.neo4j.image.repository "tag" .Values.neo4j.image.tag "global" .Values.global) }}
{{- end }}

{{/*
Create image pull secrets list.
*/}}
{{- define "genyee.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Return the storage class name.
*/}}
{{- define "genyee.storageClass" -}}
{{- if .Values.global.storageClass }}
{{- if eq .Values.global.storageClass "-" }}
storageClassName: ""
{{- else }}
storageClassName: {{ .Values.global.storageClass | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Pod security context.
Merges global defaults with component-specific overrides.
*/}}
{{- define "genyee.podSecurityContext" -}}
{{- $global := .Values.global.podSecurityContext | default dict }}
{{- $component := .componentSecurityContext | default dict }}
{{- $merged := merge $component $global }}
{{- if $merged }}
securityContext:
{{- toYaml $merged | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Container security context.
*/}}
{{- define "genyee.containerSecurityContext" -}}
{{- if . }}
securityContext:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Validate and return docreader.tls.secretName when TLS is enabled.
*/}}
{{- define "genyee.docreader.tlsSecretName" -}}
{{- $secretName := "" -}}
{{- if and .Values.docreader.tls .Values.docreader.tls.secretName -}}
{{- $secretName = trim (toString .Values.docreader.tls.secretName) -}}
{{- end -}}
{{- required "docreader.tls.secretName is required when docreader.tls.enabled is true" $secretName -}}
{{- end }}

{{/*
Validate and return the CA certificate key inside docreader.tls.secretName.
*/}}
{{- define "genyee.docreader.tlsCAKey" -}}
{{- $caKey := "ca.crt" -}}
{{- if and .Values.docreader.tls .Values.docreader.tls.caKey -}}
{{- $trimmed := trim (toString .Values.docreader.tls.caKey) -}}
{{- if $trimmed -}}
{{- $caKey = $trimmed -}}
{{- end -}}
{{- end -}}
{{- if or (contains "/" $caKey) (eq $caKey ".") (eq $caKey "..") -}}
{{- fail "docreader.tls.caKey must be a valid Secret key name without path separators" -}}
{{- end -}}
{{- $caKey -}}
{{- end }}
