package storyapi

import (
	"net/http"
	"strconv"
)

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "usage is read with GET")
		return
	}
	var before int64
	if values, ok := r.URL.Query()["before_id"]; ok {
		var err error
		if len(values) != 1 {
			writeError(w, 400, "invalid_request", "invalid cursor")
			return
		}
		before, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || before <= 0 {
			writeError(w, 400, "invalid_request", "invalid cursor")
			return
		}
	}
	page, err := s.app.ReadUsage(r.Context(), r.URL.Query().Get("world_id"), before)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
