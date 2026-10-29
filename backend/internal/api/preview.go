package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/pinkhit/vault/backend/internal/pdfutil"
	"github.com/pinkhit/vault/backend/internal/preview"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !storage.ValidID(id) {
		problem(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	parameters, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		problem(w, http.StatusBadRequest, "invalid preview parameters")
		return
	}
	page := 1
	if values, ok := parameters["page"]; ok {
		if len(values) != 1 {
			problem(w, http.StatusBadRequest, "provide one preview page")
			return
		}
		page, err = strconv.Atoi(values[0])
	}
	if err != nil || page < 1 || page > preview.MaxPages || len(parameters) > 1 || (len(parameters) == 1 && !parameters.Has("page")) {
		problem(w, http.StatusBadRequest, "invalid preview parameters")
		return
	}
	result, err := a.Documents.Preview(r.Context(), sessionFrom(r).User, id, page)
	switch {
	case errors.Is(err, preview.ErrUnsupported):
		problem(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, preview.ErrLimit), errors.Is(err, pdfutil.ErrLimit):
		problem(w, http.StatusRequestEntityTooLarge, preview.ErrLimit.Error())
	case errors.Is(err, preview.ErrMalformed), errors.Is(err, pdfutil.ErrMalformed):
		problem(w, http.StatusUnprocessableEntity, preview.ErrMalformed.Error())
	case errors.Is(err, preview.ErrPage):
		problem(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, preview.ErrBusy), errors.Is(err, pdfutil.ErrUnavailable):
		problem(w, http.StatusServiceUnavailable, "preview is temporarily unavailable; try again or download the original")
	case errors.Is(err, context.DeadlineExceeded):
		problem(w, http.StatusGatewayTimeout, "preview took too long; download the original")
	case errors.Is(err, os.ErrNotExist):
		problem(w, http.StatusConflict, "stored document is missing or corrupt")
	case err != nil:
		a.securityError(w, r, err)
	default:
		w.Header().Set("Content-Disposition", "inline")
		writeJSON(w, http.StatusOK, result)
	}
}
