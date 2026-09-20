{{- define "garmin-mcp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "garmin-mcp.fullname" -}}
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

{{- define "garmin-mcp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "garmin-mcp.labels" -}}
helm.sh/chart: {{ include "garmin-mcp.chart" . }}
{{ include "garmin-mcp.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "garmin-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "garmin-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "garmin-mcp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "garmin-mcp.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The OAuth client registry as the JSON document GARMIN_MCP_OAUTH_CLIENTS takes.
Rendered from oauth.clients so an operator writes YAML, not JSON. It carries a
secret digest, so the caller must place it in a Secret and never a ConfigMap.
*/}}
{{- define "garmin-mcp.oauthClientsJSON" -}}
{{- $out := list }}
{{- range .Values.oauth.clients }}
{{- $client := dict "id" .id "name" (default .id .name) "redirect-uris" .redirectURIs "scopes" .scopes "resources" (default (list $.Values.config.publicURL) .resources) "public" (default false .public) }}
{{- if .secretHash }}
{{- $_ := set $client "secret-hash" .secretHash }}
{{- end }}
{{- $out = append $out $client }}
{{- end }}
{{- toJson $out }}
{{- end }}
