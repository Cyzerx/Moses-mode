package report

import (
	"html/template"
	"io"

	"github.com/Cyzerx/Artex-Moses-mode/db"
)

// The template contains no scripts, external resources or live evidence links.
var offlineReview = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Selected finding evidence</title>
<style>body{max-width:48rem;margin:auto;padding:2rem 1rem;color:#192936;font:1rem/1.5 system-ui,sans-serif}h1,h2{line-height:1.2}article{border-top:1px solid #bbc6ce;padding:1rem 0}dt{font-weight:600}dd{margin:0 0 .35rem}li{overflow-wrap:anywhere}</style>
</head><body><main><h1>Selected finding evidence</h1>
<p>Engagement #{{.TaskID}} · {{len .Findings}} selected finding(s)</p>
{{range .Findings}}<article><h2>Finding #{{.ID}}{{if .Title}} · {{.Title}}{{end}}</h2>
<dl><dt>Disposition</dt><dd>{{.Disposition}}</dd><dt>Machine verification</dt><dd>{{.Verification}}</dd>
<dt>Detailed report freshness</dt><dd>{{.Freshness}}</dd></dl>
<h3>Evidence references</h3>{{if .Bindings}}<ul>{{range .Bindings}}<li>Binding #{{.ID}} · {{.Role}}</li>{{end}}</ul>{{else}}<p>No bound evidence.</p>{{end}}</article>{{end}}
</main></body></html>`))

func WriteOfflineReview(w io.Writer, taskID int64, findings []db.ReviewFinding) error {
	return offlineReview.Execute(w, struct {
		TaskID   int64
		Findings []db.ReviewFinding
	}{taskID, findings})
}
