package auth

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
)

// Layout minimal yang meniru kontrak layouts/base.html (define "title" dan "content").
const stubLayout = `{{define "base"}}<title>{{template "title" .}}</title>{{template "content" .}}{{end}}{{define "csrf"}}<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">{{end}}`

type stubPageData struct {
	CSRFToken string
	Data      any
}

func TestLoginTemplate_RendersAndEscapes(t *testing.T) {
	tmpl := template.Must(template.New("stub").Parse(stubLayout))
	tmpl = template.Must(tmpl.ParseFiles(filepath.Join("..", "..", "web", "templates", "auth", "login.html")))
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "base", stubPageData{Data: LoginPage{
		CSRFToken: "tok123", Username: `<script>x</script>`, Next: "/databases", Error: msgInvalidCredentials,
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`name="csrf_token" value="tok123"`, msgInvalidCredentials, `type="password"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output tidak memuat %q", want)
		}
	}
	if strings.Contains(out, "<script>x</script>") {
		t.Error("username harus di-escape")
	}
}
