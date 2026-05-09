package server

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"mpnotepad/internal/config"
	"mpnotepad/internal/store"
	webassets "mpnotepad/web"
)

// Server is the HTTP/WebSocket server.
type Server struct {
	cfg       *config.Config
	log       *slog.Logger
	store     *store.Store
	mux       chi.Router
	srv       *http.Server
	templates *template.Template
	staticFS  fs.FS

	hubParentCtx context.Context
	hubCancel    context.CancelFunc
	hubs         sync.Map // docID string -> *hubRunner
	hubWG        sync.WaitGroup
}

type hubRunner struct {
	hub    *Hub
	cancel context.CancelFunc
}

// New constructs a Server.
func New(cfg *config.Config, log *slog.Logger, st *store.Store) (*Server, error) {
	tmpl, err := template.ParseFS(webassets.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	staticRoot, err := fs.Sub(webassets.Static, "static")
	if err != nil {
		return nil, err
	}

	hctx, hcancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:          cfg,
		log:          log,
		store:        st,
		templates:    tmpl,
		staticFS:     staticRoot,
		hubParentCtx: hctx,
		hubCancel:    hcancel,
	}
	s.mux = s.routes()
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.accessLog())
	r.Get("/", s.handleHome)
	r.Post("/new", s.handleNewDoc)

	r.Get("/d/{id}", s.handleDocView)
	r.Post("/d/{id}/auth", s.handleDocAuth)
	r.Get("/d/{id}/settings.json", s.handleSettingsGet)
	r.Post("/d/{id}/settings", s.handleSettingsPost)
	r.Get("/d/{id}/ws", s.handleWS)

	fileSrv := http.FileServer(http.FS(s.staticFS))
	r.Handle("/static/*", http.StripPrefix("/static/", fileSrv))

	return r
}

// accessLog logs one line per HTTP request (method, path, status, size, duration).
func (s *Server) accessLog() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			s.log.Info("http",
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"status", status,
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start),
				"remote", r.RemoteAddr,
			)
		})
	}
}

func (s *Server) acquireHub(docID string, seed []byte) *Hub {
	if v, ok := s.hubs.Load(docID); ok {
		return v.(*hubRunner).hub
	}

	ctx, cancel := context.WithCancel(s.hubParentCtx)
	onEmpty := func() {
		s.hubs.Delete(docID)
		cancel()
	}
	h := newHub(s.log, s.store, docID, seed, onEmpty)
	hr := &hubRunner{hub: h, cancel: cancel}

	if actual, loaded := s.hubs.LoadOrStore(docID, hr); loaded {
		cancel()
		return actual.(*hubRunner).hub
	}

	s.hubWG.Add(1)
	go func() {
		defer s.hubWG.Done()
		h.run(ctx)
	}()
	return h
}

// Run listens and serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("listening", "addr", s.cfg.Addr)
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shCtx)
		s.hubCancel()
		s.hubWG.Wait()
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// FlushAllHubs persists any in-memory Yjs state (call before closing store if needed).
func (s *Server) FlushAllHubs(ctx context.Context) {
	s.hubs.Range(func(key, value any) bool {
		hr := value.(*hubRunner)
		_ = hr.hub.Flush(ctx)
		return true
	})
}
