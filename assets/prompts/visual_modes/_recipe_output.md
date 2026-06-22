{{define "recipe_output"}}
**Source Recipe**
- Title: {{.Title}}
- Mood: {{.Mood}}{{if .Tempo}}
- Tempo: {{.Tempo}} BPM{{end}}{{if .Key}}
- Key: {{.Key}}{{end}}{{if .Instruments}}
- Instruments: {{join .Instruments ", "}}{{end}}{{if .Hook}}
- Hook: "{{.Hook}}"{{end}}{{if .Keywords}}
- Keywords: {{join .Keywords ", "}}{{end}}{{if .Narrative}}
- Narrative: {{.Narrative}}{{end}}{{if .Sections}}
- Sections:{{range .Sections}}
  - {{.Name}} ({{.Duration}}s): {{.Prompt}}{{end}}{{end}}
{{end}}
