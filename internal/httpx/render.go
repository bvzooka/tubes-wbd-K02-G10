package httpx

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"

	"dbplatform/internal/domain"
)

const (
	templatesDir = "templates"
	layoutFile   = "templates/layouts/base.html"
)

type PageData struct {
	Title     string
	Principal *domain.Principal
	CSRFToken string
	Flash     *Flash
	Data      any
}

type Renderer struct {
	pages map[string]*template.Template
	flash *Flasher
}

func NewRenderer(fsys fs.FS, flash *Flasher) (*Renderer, error) {
	partials, err := fs.Glob(fsys, templatesDir+"/partials/*.html")
	if err != nil {
		return nil, err
	}
	sort.Strings(partials)

	r := &Renderer{pages: map[string]*template.Template{}, flash: flash}
	err = fs.WalkDir(fsys, templatesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, templatesDir+"/")
		if d.IsDir() || !strings.HasSuffix(p, ".html") ||
			strings.HasPrefix(rel, "layouts/") || strings.HasPrefix(rel, "partials/") {
			return nil
		}
		files := append([]string{layoutFile}, partials...)
		files = append(files, p)
		t, err := template.New(path.Base(p)).Funcs(funcs).ParseFS(fsys, files...)
		if err != nil {
			return fmt.Errorf("httpx: parse template %s: %w", rel, err)
		}
		r.pages[rel] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Renderer) Page(w http.ResponseWriter, req *http.Request, status int, name string, data any) {
	t, ok := r.pages[name]
	if !ok {
		http.Error(w, "Terjadi kesalahan pada server.", http.StatusInternalServerError)
		logError(req, fmt.Errorf("template %q tidak ditemukan", name))
		return
	}

	pd := PageData{Data: data, CSRFToken: domain.CSRFTokenFrom(req.Context())}
	if p, ok := domain.PrincipalFrom(req.Context()); ok {
		pd.Principal = &p
	}
	if r.flash != nil {
		pd.Flash = r.flash.Take(w, req)
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", pd); err != nil {
		http.Error(w, "Terjadi kesalahan pada server.", http.StatusInternalServerError)
		logError(req, fmt.Errorf("render %s: %w", name, err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (r *Renderer) Has(name string) bool { _, ok := r.pages[name]; return ok }

var funcs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"truncate": func(n int, s string) string {
		rs := []rune(s)
		if len(rs) <= n {
			return s
		}
		return string(rs[:n]) + "…"
	},
}
