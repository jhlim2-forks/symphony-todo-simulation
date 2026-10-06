package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type Todo struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type Store struct {
	mu     sync.Mutex
	todos  []Todo
	nextID int
}

func NewStore() *Store        { return &Store{todos: []Todo{}, nextID: 1} }
func (s *Store) list() []Todo { s.mu.Lock(); defer s.mu.Unlock(); return append([]Todo{}, s.todos...) }
func (s *Store) add(title string) Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Todo{ID: s.nextID, Title: title}
	s.nextID++
	s.todos = append(s.todos, t)
	return t
}
func (s *Store) update(id int, done bool) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.todos {
		if s.todos[i].ID == id {
			s.todos[i].Done = done
			return s.todos[i], true
		}
	}
	return Todo{}, false
}
func (s *Store) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.todos {
		if t.ID == id {
			s.todos = append(s.todos[:i], s.todos[i+1:]...)
			return true
		}
	}
	return false
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var errorMessages = map[string]string{
	"invalid_json": "요청 형식이 올바르지 않습니다.", "title_blank": "할 일 제목을 입력해 주세요.",
	"title_too_long": "할 일 제목은 200자까지 입력할 수 있습니다.", "done_required": "완료 여부(done)를 true 또는 false로 보내 주세요.",
	"not_found": "해당 할 일을 찾을 수 없습니다.", "method_not_allowed": "허용되지 않은 요청 방식입니다.",
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: errorMessages[code]}})
}

func newHandler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if r.Method != "GET" {
				fail(w, 405, "method_not_allowed")
				return
			}
			http.ServeFile(w, r, "index.html")
			return
		}
		if r.URL.Path != "/api/todos" && !strings.HasPrefix(r.URL.Path, "/api/todos/") {
			fail(w, 404, "not_found")
			return
		}
		if r.URL.Path == "/api/todos" {
			switch r.Method {
			case "GET":
				writeJSON(w, 200, map[string][]Todo{"todos": store.list()})
			case "POST":
				var body struct {
					Title json.RawMessage `json:"title"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					fail(w, 400, "invalid_json")
					return
				}
				var title string
				if len(body.Title) == 0 || json.Unmarshal(body.Title, &title) != nil {
					fail(w, 400, "invalid_json")
					return
				}
				title = strings.TrimSpace(title)
				if title == "" {
					fail(w, 400, "title_blank")
					return
				}
				if utf8.RuneCountInString(title) > 200 {
					fail(w, 400, "title_too_long")
					return
				}
				writeJSON(w, 201, store.add(title))
			default:
				fail(w, 405, "method_not_allowed")
			}
			return
		}
		part := strings.TrimPrefix(r.URL.Path, "/api/todos/")
		if part == "" || strings.Contains(part, "/") {
			fail(w, 404, "not_found")
			return
		}
		id, err := strconv.Atoi(part)
		if err != nil || id < 1 {
			fail(w, 404, "not_found")
			return
		}
		switch r.Method {
		case "PATCH":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				fail(w, 400, "invalid_json")
				return
			}
			v, ok := body["done"]
			var done bool
			if !ok || json.Unmarshal(v, &done) != nil {
				fail(w, 400, "done_required")
				return
			}
			t, ok := store.update(id, done)
			if !ok {
				fail(w, 404, "not_found")
				return
			}
			writeJSON(w, 200, t)
		case "DELETE":
			if !store.delete(id) {
				fail(w, 404, "not_found")
				return
			}
			w.WriteHeader(204)
		default:
			fail(w, 405, "method_not_allowed")
		}
	})
}

func main() {
	addr := flag.String("listen", "127.0.0.1:8091", "listen address")
	flag.Parse()
	_ = http.ListenAndServe(*addr, newHandler(NewStore()))
}
