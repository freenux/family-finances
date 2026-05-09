package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"family-finances/internal/domain"
)

//go:embed template
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// StaticFS 用于 http.FileServer 挂载
func StaticFS() fs.FS {
	sub, _ := fs.Sub(staticFS, "static")
	return sub
}

// funcMap 所有模板共享的函数
var funcMap = template.FuncMap{
	// rawJSON 把已经 marshal 好的 JSON 字符串注入到 <script type="application/json"> 里，
	// 避免 html/template 的 JS 上下文把它再字符串化。
	"rawJSON": func(s string) template.JS { return template.JS(s) },
	"yuan": func(fen int64) string {
		sign := ""
		if fen < 0 {
			sign = "-"
			fen = -fen
		}
		yuan := fen / 100
		cents := fen % 100
		// 千位分隔
		s := fmt.Sprintf("%d", yuan)
		var b strings.Builder
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(c)
		}
		return fmt.Sprintf("%s%s.%02d", sign, b.String(), cents)
	},
	"pct": func(r float64) string {
		return fmt.Sprintf("%.1f%%", r*100)
	},
	"formatDate": func(t time.Time) string {
		return t.Format("2006-01-02")
	},
	"categoryName": func(id string, cats []domain.Category) string {
		if id == "" {
			return "—"
		}
		for _, c := range cats {
			if c.ID == id {
				return c.Name
			}
		}
		return id
	},
}

type Renderer struct {
	templates map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{templates: make(map[string]*template.Template)}

	partialPaths, err := fs.Glob(templatesFS, "template/partials/*.html")
	if err != nil {
		return nil, err
	}
	layoutPaths, err := fs.Glob(templatesFS, "template/layout/*.html")
	if err != nil {
		return nil, err
	}
	pagePaths, err := fs.Glob(templatesFS, "template/pages/*.html")
	if err != nil {
		return nil, err
	}

	for _, pagePath := range pagePaths {
		name := strings.TrimSuffix(path.Base(pagePath), ".html")
		files := append([]string{}, layoutPaths...)
		files = append(files, partialPaths...)
		files = append(files, pagePath)
		t, err := template.New(name).Funcs(funcMap).ParseFS(templatesFS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		r.templates[name] = t
	}

	// 局部模板：partials 单独构建，用于 HTMX 片段返回
	partialTpl, err := template.New("partials").Funcs(funcMap).ParseFS(templatesFS, append(partialPaths, layoutPaths...)...)
	if err != nil {
		return nil, err
	}
	r.templates["_partials"] = partialTpl
	return r, nil
}

// RenderPage 渲染整页（走 layout/base → page/content）
func (r *Renderer) RenderPage(w io.Writer, page string, data any) error {
	t, ok := r.templates[page]
	if !ok {
		return fmt.Errorf("template not found: %s", page)
	}
	return t.ExecuteTemplate(w, "page", data)
}

// RenderPartial 渲染一个 {{define "..."}} 片段（HTMX 局部更新用）
func (r *Renderer) RenderPartial(w io.Writer, name string, data any) error {
	return r.templates["_partials"].ExecuteTemplate(w, name, data)
}
