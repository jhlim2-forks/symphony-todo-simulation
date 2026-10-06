package main

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Todo struct {
	ID    int     `json:"id"`
	Title string  `json:"title"`
	Done  bool    `json:"done"`
	Due   *string `json:"due"`
}
type Store struct {
	mu    sync.Mutex
	todos []Todo
	next  int
}

func NewStore() *Store { return &Store{todos: []Todo{}, next: 1} }

type App struct {
	store *Store
	html  []byte
}

var messages = map[string]string{
	"invalid_json": "요청 형식이 올바르지 않습니다.", "title_blank": "할 일 제목을 입력해 주세요.",
	"title_too_long": "할 일 제목은 200자까지 입력할 수 있습니다.", "done_required": "완료 여부(done)를 true 또는 false로 보내 주세요.",
	"due_invalid":       "마감일은 2026-03-01처럼 실제로 있는 날짜로 입력해 주세요.",
	"nothing_to_update": "바꿀 내용을 보내 주세요. 완료 여부(done) 또는 마감일(due)이 필요합니다.",
	"not_found":         "해당 할 일을 찾을 수 없습니다.", "method_not_allowed": "허용되지 않은 요청 방식입니다.",
}

func pageBytes() ([]byte, error) { return os.ReadFile("index.html") }
func NewHandler() http.Handler   { html, _ := pageBytes(); return &App{NewStore(), html} }
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		if r.Method != "GET" {
			a.err(w, 405, "method_not_allowed")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(a.html)
		return
	}
	if r.URL.Path == "/api/todos" {
		switch r.Method {
		case "GET":
			a.list(w)
		case "POST":
			a.create(w, r)
		default:
			a.err(w, 405, "method_not_allowed")
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/todos/") {
		id, e := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/todos/"))
		if e != nil || id < 1 {
			a.err(w, 404, "not_found")
			return
		}
		switch r.Method {
		case "PATCH":
			a.update(w, r, id)
		case "DELETE":
			a.delete(w, id)
		default:
			a.err(w, 405, "method_not_allowed")
		}
		return
	}
	a.err(w, 404, "not_found")
}
func (a *App) err(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": messages[code]}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) bool {
	b, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if e != nil {
		return false
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	if d.Decode(v) != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}
func (a *App) list(w http.ResponseWriter) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	writeJSON(w, 200, map[string]any{"todos": a.store.todos})
}
func validDue(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	t, e := time.Parse("2006-01-02", s)
	return e == nil && t.Format("2006-01-02") == s
}
func (a *App) create(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if !decode(r, &raw) {
		a.err(w, 400, "invalid_json")
		return
	}
	var title string
	b, ok := raw["title"]
	if !ok || json.Unmarshal(b, &title) != nil {
		a.err(w, 400, "invalid_json")
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		a.err(w, 400, "title_blank")
		return
	}
	if utf8.RuneCountInString(title) > 200 {
		a.err(w, 400, "title_too_long")
		return
	}
	var due *string
	if b, ok = raw["due"]; ok && string(b) != "null" {
		var s string
		if json.Unmarshal(b, &s) != nil {
			a.err(w, 400, "invalid_json")
			return
		}
		if !validDue(s) {
			a.err(w, 400, "due_invalid")
			return
		}
		due = &s
	}
	a.store.mu.Lock()
	t := Todo{a.store.next, title, false, due}
	a.store.next++
	a.store.todos = append(a.store.todos, t)
	a.store.mu.Unlock()
	writeJSON(w, 201, t)
}
func (a *App) update(w http.ResponseWriter, r *http.Request, id int) {
	var raw map[string]json.RawMessage
	if !decode(r, &raw) {
		a.err(w, 400, "invalid_json")
		return
	}
	_, hasDone := raw["done"]
	dueRaw, hasDue := raw["due"]
	if !hasDone && !hasDue {
		a.err(w, 400, "nothing_to_update")
		return
	}
	var done bool
	if hasDone && json.Unmarshal(raw["done"], &done) != nil {
		a.err(w, 400, "done_required")
		return
	}
	var due *string
	if hasDue && string(dueRaw) != "null" {
		var s string
		if json.Unmarshal(dueRaw, &s) != nil {
			a.err(w, 400, "invalid_json")
			return
		}
		if !validDue(s) {
			a.err(w, 400, "due_invalid")
			return
		}
		due = &s
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for i := range a.store.todos {
		if a.store.todos[i].ID == id {
			if hasDone {
				a.store.todos[i].Done = done
			}
			if hasDue {
				a.store.todos[i].Due = due
			}
			writeJSON(w, 200, a.store.todos[i])
			return
		}
	}
	a.err(w, 404, "not_found")
}
func (a *App) delete(w http.ResponseWriter, id int) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for i, t := range a.store.todos {
		if t.ID == id {
			a.store.todos = append(a.store.todos[:i], a.store.todos[i+1:]...)
			w.WriteHeader(204)
			return
		}
	}
	a.err(w, 404, "not_found")
}
func main() {
	addr := flag.String("listen", "127.0.0.1:8091", "listen address")
	flag.Parse()
	_ = http.ListenAndServe(*addr, NewHandler())
}
