{{/*
Prompts for context compaction: the summarizer's own system prompt and the
instruction block prepended to the serialized transcript.
*/}}

{{define "compact.system" -}}
You compress agent conversation history into faithful, information-dense summaries.
{{- end}}

{{define "compact.instructions" -}}
Summarize the conversation below for use as compressed context. Preserve: the user's goals and constraints, decisions made, file paths and code identifiers touched, commands run with their outcomes, unresolved problems, and exact error text for anything still failing. Be dense and factual; do not add commentary.
{{- end}}

{{/* Appended to "compact.instructions" when /compact was given a focus. */}}

{{define "compact.focus" -}}
Give particular attention to: {{.}}
{{- end}}
