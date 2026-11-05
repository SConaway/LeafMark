// Package web serves LeafMark's confirm endpoint and minimal WebUI.
// Server-rendered html/template only — no JS framework or build step,
// matching the spec's "no need for that given the surface area" call.
package web

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"

	"leafmark/internal/goodreads"
	"leafmark/internal/ntfy"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Server holds everything the confirm endpoint and WebUI need.
type Server struct {
	db        *sql.DB
	goodreads goodreads.Goodreads
	notifier  ntfy.Publisher
	baseURL   string
	tmpl      *template.Template
	mux       *http.ServeMux
}

// NewServer builds the HTTP handler for the confirm endpoint and WebUI.
func NewServer(db *sql.DB, gr goodreads.Goodreads, notifier ntfy.Publisher, baseURL string) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		db:        db,
		goodreads: gr,
		notifier:  notifier,
		baseURL:   baseURL,
		tmpl:      tmpl,
		mux:       http.NewServeMux(),
	}

	s.mux.HandleFunc("POST /confirm", s.handleConfirm)
	s.mux.HandleFunc("GET /pending", s.handlePendingList)
	s.mux.HandleFunc("GET /pending/{id}", s.handlePendingDetail)
	s.mux.HandleFunc("POST /pending/{id}/resend", s.handleResend)
	s.mux.HandleFunc("POST /pending/resend-all", s.handleResendAll)
	s.mux.HandleFunc("POST /pending/{id}/ignore", s.handleIgnore)
	s.mux.HandleFunc("POST /ignored/{hash}/restore", s.handleRestore)

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}
