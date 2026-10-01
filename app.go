package main

import (
	_ "embed"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed index.html
var page string

type todo struct { ID int `json:"id"`; Text string `json:"text"`; Completed bool `json:"completed"` }
type store struct { mu sync.Mutex; items map[int]todo; nextID int }

func (s *store) list() []todo { s.mu.Lock(); defer s.mu.Unlock(); out := make([]todo, 0, len(s.items)); for _, t := range s.items { out = append(out, t) }; sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID }); return out }
func (s *store) create(text string) todo { s.mu.Lock(); defer s.mu.Unlock(); t := todo{ID: s.nextID, Text: text}; s.items[t.ID] = t; s.nextID++; return t }
func (s *store) exists(id int) bool { s.mu.Lock(); defer s.mu.Unlock(); _, ok := s.items[id]; return ok }
func (s *store) setCompleted(id int, completed bool) bool { s.mu.Lock(); defer s.mu.Unlock(); t, ok := s.items[id]; if !ok { return false }; t.Completed = completed; s.items[id] = t; return true }
func (s *store) delete(id int) bool { s.mu.Lock(); defer s.mu.Unlock(); if _, ok := s.items[id]; !ok { return false }; delete(s.items, id); return true }

// NewHandler owns its own independent, concurrency-safe in-memory TODO store.
func NewHandler() http.Handler {
	s := &store{items: make(map[int]todo), nextID: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("GET /api/todos", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.list()) })
	mux.HandleFunc("POST /api/todos", func(w http.ResponseWriter, r *http.Request) {
		var req struct { Text string `json:"text"` }
		if !decodeJSONObject(r, &req) { http.Error(w, "invalid request", http.StatusBadRequest); return }
		text := strings.TrimSpace(req.Text)
		if n := utf8.RuneCountInString(text); n < 1 || n > 200 { http.Error(w, "invalid text", http.StatusBadRequest); return }
		writeJSON(w, http.StatusCreated, s.create(text))
	})
	mux.HandleFunc("PATCH /api/todos/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := todoID(r); if !ok || !s.exists(id) { http.NotFound(w, r); return }
		var req struct { Completed *bool `json:"completed"` }
		if !decodeJSONObject(r, &req) || req.Completed == nil { http.Error(w, "invalid request", http.StatusBadRequest); return }
		if !s.setCompleted(id, *req.Completed) { http.NotFound(w, r); return }
		writeJSON(w, http.StatusOK, s.list())
	})
	mux.HandleFunc("DELETE /api/todos/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := todoID(r); if !ok || !s.delete(id) { http.NotFound(w, r); return }; w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func todoID(r *http.Request) (int, bool) { id, err := strconv.Atoi(r.PathValue("id")); return id, err == nil && id > 0 }

// decodeJSONObject accepts exactly one top-level JSON object and no extra data.
func decodeJSONObject(r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body); var raw json.RawMessage
	if dec.Decode(&raw) != nil { return false }
	trimmed := bytes.TrimSpace(raw); if len(trimmed) == 0 || trimmed[0] != '{' { return false }
	var extra json.RawMessage; if dec.Decode(&extra) != io.EOF { return false }
	return json.Unmarshal(raw, dst) == nil
}
func writeJSON(w http.ResponseWriter, status int, value any) { w.Header().Set("Content-Type", "application/json; charset=utf-8"); w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value) }
