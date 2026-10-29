package api

import "net/http"

func (a *API) storageAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		problem(w, http.StatusBadRequest, "storage analytics does not accept parameters")
		return
	}
	report, err := a.Storage.Overview(r.Context(), sessionFrom(r).User)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
