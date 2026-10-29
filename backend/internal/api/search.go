package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pinkhit/vault/backend/internal/documents"
)

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values["q"]) != 1 {
		problem(w, http.StatusBadRequest, "provide one search query using q")
		return
	}
	for key, entries := range values {
		if (key != "q" && key != "limit" && key != "offset") || len(entries) != 1 {
			problem(w, http.StatusBadRequest, "search accepts only q, limit and offset once each")
			return
		}
	}
	limit, offset := 20, 0
	if values.Has("limit") {
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil {
			problem(w, http.StatusBadRequest, documents.ErrSearchBounds.Error())
			return
		}
	}
	if values.Has("offset") {
		offset, err = strconv.Atoi(values.Get("offset"))
		if err != nil {
			problem(w, http.StatusBadRequest, documents.ErrSearchBounds.Error())
			return
		}
	}
	page, err := a.Documents.Search(r.Context(), sessionFrom(r).User, values.Get("q"), limit, offset)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, page)
	case errors.Is(err, documents.ErrSearchQuery), errors.Is(err, documents.ErrSearchBounds):
		problem(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		problem(w, http.StatusServiceUnavailable, "search interrupted; retry with a narrower query")
	default:
		// FTS errors can contain search terms. Keep them out of access/error logs.
		a.Log.Error("search failed", "request_id", w.Header().Get("X-Request-ID"))
		problem(w, http.StatusInternalServerError, "search failed; see server logs using the request ID")
	}
}
