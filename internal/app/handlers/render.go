package handlers

import (
	"net/http"
	"text/template"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
)

// NotFound returns a handler that renders the 404 page.
func NotFound(tmpl *template.Template, config *models.Configuration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)

		data := struct {
			Config *models.Configuration
		}{
			Config: config,
		}

		if err := tmpl.ExecuteTemplate(w, "notfound", data); err != nil {
			logging.LogError("failed to execute template", logging.Error(err))
		}
	}
}

// Index returns a handler that renders the index page.
func Index(tmpl *template.Template, config *models.Configuration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logging.LogDebug("indexHandler", logging.String("path", r.URL.Path))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		data := struct {
			Config *models.Configuration
		}{
			Config: config,
		}

		if err := tmpl.ExecuteTemplate(w, "index", data); err != nil {
			logging.LogError("failed to execute template", logging.Error(err))
		}
	}
}
