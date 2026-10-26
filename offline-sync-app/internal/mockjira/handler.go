package mockjira

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /issues", s.rest(s.handleList))
	mux.HandleFunc("POST /issues", s.rest(s.handleCreate))
	mux.HandleFunc("GET /issues/{id}", s.rest(s.handleGet))
	mux.HandleFunc("PATCH /issues/{id}", s.rest(s.handlePatch))
	mux.HandleFunc("DELETE /issues/{id}", s.rest(s.handleDelete))
	mux.HandleFunc("POST /_admin/fail", s.handleAdminFail)
	mux.HandleFunc("POST /_admin/flush", s.handleAdminFlush)
	return mux
}

func (s *Server) rest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if f, ok := s.takeFault(); ok {
			if f.retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(f.retryAfter.Seconds())))
			}
			http.Error(w, "injected fault", f.status)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if raw := r.URL.Query().Get("updated_since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			http.Error(w, "invalid updated_since", http.StatusBadRequest)
			return
		}
		since = parsed
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	page, err := s.List(since, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Summary     string `json:"summary"`
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.Create(in.Summary, in.Status, in.Description))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	issue, err := s.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	var p Patch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	issue, err := s.patch(r.PathValue("id"), p, r.Header.Get("Idempotency-Key"))
	if errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

// patch replays the stored response for a repeated Idempotency-Key instead of
// applying the change (and bumping the version) a second time.
func (s *Server) patch(id string, p Patch, key string) (Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patchCalls++
	if key != "" {
		if prior, ok := s.idempotent[key]; ok {
			return prior, nil
		}
	}
	issue, err := s.update(id, p)
	if err != nil {
		return Issue{}, err
	}
	if key != "" {
		s.idempotent[key] = issue
	}
	return issue, nil
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	issue, err := s.Delete(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) handleAdminFail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, _ := strconv.Atoi(q.Get("n"))
	status, _ := strconv.Atoi(q.Get("status"))
	retryAfter, _ := strconv.Atoi(q.Get("retry_after"))
	if n <= 0 || status < 400 {
		http.Error(w, "n must be > 0 and status >= 400", http.StatusBadRequest)
		return
	}
	s.FailNext(n, status, time.Duration(retryAfter)*time.Second)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminFlush(w http.ResponseWriter, r *http.Request) {
	modes := map[string]FlushMode{
		"":          FlushInOrder,
		"in_order":  FlushInOrder,
		"duplicate": FlushDuplicated,
		"reverse":   FlushReversed,
		"drop":      FlushDropped,
	}
	mode, ok := modes[r.URL.Query().Get("mode")]
	if !ok {
		http.Error(w, "mode must be in_order, duplicate, reverse or drop", http.StatusBadRequest)
		return
	}
	posts, err := s.Flush(mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"posts": posts})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
