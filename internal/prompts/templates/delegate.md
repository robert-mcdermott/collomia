{{/*
Preambles prepended to a sub-agent's instructions when a delegated task
carries a named agent profile or a declared write scope.
*/}}

{{/* "." is the profile's configured role instructions. */}}

{{define "delegate.role" -}}
Agent role: {{.}}
{{- end}}

{{/* "." is the comma-joined list of allowed repository-relative scopes. */}}

{{define "delegate.write_contract" -}}
Delegated write contract: change only these repository-relative scopes: {{.}}. If the task requires another file, stop and report the mismatch instead of editing outside this scope.
{{- end}}
