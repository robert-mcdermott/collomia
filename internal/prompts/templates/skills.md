{{/*
The skills catalog preamble, substituted into "system" as .SkillsSummary
above the discovered skill list. The per-skill line format stays in
skills.Catalog.Summary; only the prose lives here.
*/}}

{{define "skills.header" -}}
Available skills (use load_skill only when a description matches the task; loaded skills may bundle scripts and reference files):
{{- end}}

{{define "skills.empty" -}}
No skills discovered.
{{- end}}
