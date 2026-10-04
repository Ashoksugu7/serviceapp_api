package mail

import (
	"bytes"
	"html/template"
	"strings"
)

// Content is a short transactional email: a heading, paragraphs, optional
// labelled details (e.g. username) and an optional button link.
type Content struct {
	Heading    string
	Paragraphs []string
	Details    []Detail
	Action     *Action
	Footer     string
}

type Detail struct{ Label, Value string }

type Action struct{ Label, URL string }

var htmlLayout = template.Must(template.New("email").Parse(`<!doctype html>
<html><body style="margin:0;background:#f8fafc;font-family:Inter,Segoe UI,Arial,sans-serif;color:#0f172a">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="padding:24px 12px"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border:1px solid #e2e8f0;border-radius:12px">
<tr><td style="padding:24px 28px 8px"><span style="display:inline-block;background:#0d9488;color:#fff;font-weight:700;font-size:13px;border-radius:8px;padding:6px 8px">S3</span>
<span style="font-weight:600;font-size:14px;margin-left:8px">ServiceOps360</span></td></tr>
<tr><td style="padding:8px 28px 24px">
<h1 style="font-size:20px;margin:8px 0 12px">{{.Heading}}</h1>
{{range .Paragraphs}}<p style="font-size:14px;line-height:1.6;color:#334155;margin:0 0 12px">{{.}}</p>{{end}}
{{if .Details}}<table role="presentation" cellpadding="0" cellspacing="0" style="width:100%;background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;margin:8px 0 16px">
{{range .Details}}<tr><td style="padding:8px 12px;font-size:12px;color:#64748b;width:40%">{{.Label}}</td><td style="padding:8px 12px;font-size:14px;font-family:Menlo,Consolas,monospace">{{.Value}}</td></tr>{{end}}
</table>{{end}}
{{with .Action}}<p style="margin:16px 0"><a href="{{.URL}}" style="display:inline-block;background:#0d9488;color:#fff;text-decoration:none;font-size:14px;font-weight:600;border-radius:8px;padding:10px 18px">{{.Label}}</a></p>
<p style="font-size:12px;color:#64748b;word-break:break-all">{{.URL}}</p>{{end}}
{{if .Footer}}<p style="font-size:12px;color:#94a3b8;margin-top:20px">{{.Footer}}</p>{{end}}
</td></tr></table></td></tr></table></body></html>`))

// Render returns the plain-text and HTML bodies. HTML values are escaped.
func (c Content) Render() (text, html string, err error) {
	var t strings.Builder
	t.WriteString(c.Heading + "\n\n")
	for _, p := range c.Paragraphs {
		t.WriteString(p + "\n\n")
	}
	for _, d := range c.Details {
		t.WriteString(d.Label + ": " + d.Value + "\n")
	}
	if len(c.Details) > 0 {
		t.WriteString("\n")
	}
	if c.Action != nil {
		t.WriteString(c.Action.Label + ": " + c.Action.URL + "\n\n")
	}
	if c.Footer != "" {
		t.WriteString(c.Footer + "\n")
	}
	var h bytes.Buffer
	if err := htmlLayout.Execute(&h, c); err != nil {
		return "", "", err
	}
	return t.String(), h.String(), nil
}

// Build renders content into a message for one recipient.
func Build(to, subject string, content Content) (Message, error) {
	text, html, err := content.Render()
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: subject, Text: text, HTML: html}, nil
}
