package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/pinkhit/vault/backend/internal/analytics"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/storage"
)

var ErrMultipart = errors.New("send exactly one file using multipart field 'file'")
var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type API struct {
	Documents    *documents.Service
	Log          *slog.Logger
	Auth         *auth.Service
	Storage      *analytics.Service
	PublicOrigin string
}

func (a *API) Handler(staticDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/auth/session", a.protect(a.currentSession))
	mux.HandleFunc("POST /api/auth/logout", a.protect(a.logout))
	mux.HandleFunc("POST /api/auth/revoke-sessions", a.protect(a.revokeSessions))
	mux.HandleFunc("POST /api/auth/password", a.protect(a.changePassword))
	mux.HandleFunc("GET /api/admin/users", a.admin(a.users))
	mux.HandleFunc("POST /api/admin/users", a.admin(a.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", a.admin(a.updateUser))
	mux.HandleFunc("GET /api/admin/audit", a.admin(a.auditEvents))
	mux.HandleFunc("GET /api/admin/storage", a.admin(a.storageAnalytics))
	mux.HandleFunc("GET /api/documents", a.protect(a.list))
	mux.HandleFunc("GET /api/search", a.protect(a.search))
	mux.HandleFunc("POST /api/documents", a.protect(a.upload))
	mux.HandleFunc("GET /api/documents/{id}/download", a.protect(a.download))
	mux.HandleFunc("GET /api/documents/{id}/preview", a.protect(a.preview))
	mux.HandleFunc("GET /api/documents/{id}/indexing", a.protect(a.indexingStatus))
	mux.HandleFunc("PATCH /api/documents/{id}", a.protect(a.rename))
	mux.HandleFunc("DELETE /api/documents/{id}", a.protect(a.deleteDocument))
	mux.HandleFunc("GET /api/documents/{id}/permissions", a.protect(a.grants))
	mux.HandleFunc("PUT /api/documents/{id}/permissions/{userID}", a.protect(a.setGrant))
	mux.HandleFunc("DELETE /api/documents/{id}/permissions/{userID}", a.protect(a.setGrant))
	for path, allowed := range map[string]string{
		"/api/health":                  "GET, HEAD",
		"/api/documents":               "GET, HEAD, POST",
		"/api/documents/{id}/download": "GET, HEAD",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allowed)
			problem(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		problem(w, http.StatusNotFound, "endpoint not found")
	})
	if staticDir != "" {
		// http.Dir and ServeFile follow out-of-root symlinks. Open a confined root
		// per request so its descriptor is closed after synchronous serving ends.
		serveStatic := func(w http.ResponseWriter, r *http.Request) {
			root, err := os.OpenRoot(staticDir)
			if err != nil {
				a.internal(w, err)
				return
			}
			defer root.Close()
			if r.URL.Path == "/" {
				http.ServeFileFS(w, r, root.FS(), "index.html")
				return
			}
			http.FileServerFS(root.FS()).ServeHTTP(w, r)
		}
		mux.HandleFunc("GET /assets/", serveStatic)
		mux.HandleFunc("GET /{$}", serveStatic)
	}
	return a.middleware(mux)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Documents.DB.PingContext(r.Context()); err != nil {
		problem(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	mode := "local-development"
	if a.PublicOrigin != "" {
		mode = "private-https"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"mode":             mode,
		"max_upload_bytes": a.Documents.MaxBytes,
	})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 100 {
		problem(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
	}
	if err != nil || offset < 0 || offset > 1000000 {
		problem(w, http.StatusBadRequest, "offset must be between 0 and 1000000")
		return
	}
	// Fetch one extra item to determine whether another page exists.
	docs, err := a.Documents.List(r.Context(), sessionFrom(r).User, limit+1, offset)
	if err != nil {
		a.internal(w, err)
		return
	}
	more := len(docs) > limit
	if more {
		docs = docs[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs, "has_more": more})
}

func (a *API) indexingStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !storage.ValidID(id) {
		problem(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	progress, err := a.Documents.Indexing(r.Context(), sessionFrom(r).User, id)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	// Bound multipart overhead as well as the file itself.
	r.Body = http.MaxBytesReader(w, r.Body, a.Documents.MaxBytes+(64<<10))
	defer r.Body.Close()
	expected := r.Header.Get("X-Checksum-SHA256")
	if expected != "" && !checksumPattern.MatchString(expected) {
		problem(w, http.StatusBadRequest, "X-Checksum-SHA256 must be a lowercase SHA-256 hex digest")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		problem(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	part, err := mr.NextRawPart()
	if err != nil {
		a.uploadError(w, r, errors.Join(ErrMultipart, err))
		return
	}
	defer part.Close()
	// Parse the raw filename; Part.FileName() applies a basename operation that would hide traversal attempts.
	kind, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil || kind != "form-data" || params["name"] != "file" || params["filename"] == "" {
		problem(w, http.StatusBadRequest, ErrMultipart.Error())
		return
	}
	if part.Header.Get("Content-Transfer-Encoding") != "" {
		problem(w, http.StatusBadRequest, ErrMultipart.Error())
		return
	}
	reader := &singleFileReader{part: part, multipart: mr, body: r.Body}
	d, err := a.Documents.Upload(r.Context(), sessionFrom(r).User, params["filename"], reader, expected)
	if err != nil {
		a.uploadError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/documents/"+d.ID+"/download")
	writeJSON(w, http.StatusCreated, d)
}

// The reader validates the closing multipart boundary before storage can publish bytes.
type singleFileReader struct {
	part      *multipart.Part
	multipart *multipart.Reader
	body      io.Reader
	finished  bool
}

func (s *singleFileReader) Read(p []byte) (int, error) {
	if s.finished {
		return 0, io.EOF
	}
	n, err := s.part.Read(p)
	if errors.Is(err, io.EOF) {
		s.finished = true
		next, nextErr := s.multipart.NextRawPart()
		if next != nil {
			next.Close()
			return n, ErrMultipart
		}
		if !errors.Is(nextErr, io.EOF) {
			return n, errors.Join(ErrMultipart, nextErr)
		}
		// MaxBytesReader also bounds any epilogue after the closing boundary.
		if _, drainErr := io.Copy(io.Discard, s.body); drainErr != nil {
			return n, drainErr
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, errors.Join(ErrMultipart, err)
	}
	return n, err
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !storage.ValidID(id) {
		problem(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	d, f, err := a.Documents.Download(r.Context(), sessionFrom(r).User, id)
	if errors.Is(err, sql.ErrNoRows) {
		a.securityError(w, r, err)
		return
	}
	if errors.Is(err, storage.ErrIntegrity) || errors.Is(err, os.ErrNotExist) {
		problem(w, http.StatusConflict, "stored document is missing or corrupt")
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Name}))
	w.Header().Set("X-Checksum-SHA256", d.SHA256)
	w.Header().Set("ETag", `"`+d.SHA256+`"`)
	http.ServeContent(w, r, d.Name, d.CreatedAt, f)
}

func (a *API) uploadError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.Is(err, storage.ErrTooLarge) || errors.As(err, &maxErr) {
		problem(w, http.StatusRequestEntityTooLarge, "upload exceeds the configured size limit")
		return
	}

	for _, validationError := range []error{
		documents.ErrName,
		documents.ErrChecksum,
		storage.ErrEmpty,
		ErrMultipart,
	} {
		if errors.Is(err, validationError) {
			problem(w, http.StatusBadRequest, validationError.Error())
			return
		}
	}
	a.internal(w, err)
}

func (a *API) internal(w http.ResponseWriter, err error) {
	// Internal errors may contain configured local paths; kept server-side only.
	a.Log.Error("request failed", "request_id", w.Header().Get("X-Request-ID"), "error", err)
	problem(w, http.StatusInternalServerError, "request failed; see server logs using the request ID")
}

func problem(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
