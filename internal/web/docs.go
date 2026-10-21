package web

import (
	"html/template"
	"net/http"

	primusdocs "github.com/Horac3/Primus/docs"
)

func docsIndexHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, tmpl, "docs-index.html", primusdocs.List())
	}
}

type docsPageData struct {
	Doc  primusdocs.Doc
	Nav  []primusdocs.Doc
	Body template.HTML
}

func docsPageHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		body, err := primusdocs.Render(slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		title := slug
		for _, d := range primusdocs.List() {
			if d.Slug == slug {
				title = d.Title
				break
			}
		}
		renderTemplate(w, tmpl, "docs-page.html", docsPageData{
			Doc:  primusdocs.Doc{Slug: slug, Title: title},
			Nav:  primusdocs.List(),
			Body: body,
		})
	}
}
