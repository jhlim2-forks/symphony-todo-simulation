package main

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
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
	mu     sync.Mutex
	todos  []Todo
	nextID int
}

func NewStore() *Store        { return &Store{todos: []Todo{}, nextID: 1} }
func (s *Store) list() []Todo { s.mu.Lock(); defer s.mu.Unlock(); return append([]Todo{}, s.todos...) }
func (s *Store) add(title string, due *string) Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Todo{ID: s.nextID, Title: title, Due: due}
	s.nextID++
	s.todos = append(s.todos, t)
	return t
}
func (s *Store) update(id int, done *bool, due **string) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.todos {
		if s.todos[i].ID == id {
			if done != nil {
				s.todos[i].Done = *done
			}
			if due != nil {
				s.todos[i].Due = *due
			}
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
	"due_invalid":       "마감일은 2026-03-01처럼 실제로 있는 날짜로 입력해 주세요.",
	"nothing_to_update": "바꿀 내용을 보내 주세요. 완료 여부(done) 또는 마감일(due)이 필요합니다.",
}

func validDue(value string) bool {
	d, err := time.Parse("2006-01-02", value)
	return err == nil && d.Format("2006-01-02") == value
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: errorMessages[code]}})
}

func decodeJSON(r *http.Request, dst any) error {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
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
					Due   json.RawMessage `json:"due"`
				}
				if err := decodeJSON(r, &body); err != nil {
					fail(w, 400, "invalid_json")
					return
				}
				var title string
				if len(body.Title) == 0 || string(body.Title) == "null" || json.Unmarshal(body.Title, &title) != nil {
					fail(w, 400, "invalid_json")
					return
				}
				title = strings.TrimSpace(title)
				var due *string
				if len(body.Due) > 0 && string(body.Due) != "null" {
					var value string
					if json.Unmarshal(body.Due, &value) != nil {
						fail(w, 400, "invalid_json")
						return
					}
					if !validDue(value) {
						fail(w, 400, "due_invalid")
						return
					}
					due = &value
				}
				if title == "" {
					fail(w, 400, "title_blank")
					return
				}
				if utf8.RuneCountInString(title) > 200 {
					fail(w, 400, "title_too_long")
					return
				}
				todo := store.add(title, due)
				writeJSON(w, 201, todo)
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
			if err := decodeJSON(r, &body); err != nil {
				fail(w, 400, "invalid_json")
				return
			}
			var done *bool
			if v, exists := body["done"]; exists {
				var value bool
				if string(v) == "null" || json.Unmarshal(v, &value) != nil {
					fail(w, 400, "done_required")
					return
				}
				done = &value
			}
			var due **string
			if v, exists := body["due"]; exists {
				var value *string
				if string(v) != "null" {
					var date string
					if json.Unmarshal(v, &date) != nil {
						fail(w, 400, "invalid_json")
						return
					}
					if !validDue(date) {
						fail(w, 400, "due_invalid")
						return
					}
					value = &date
				}
				due = &value
			}
			if done == nil && due == nil {
				fail(w, 400, "nothing_to_update")
				return
			}
			t, ok := store.update(id, done, due)
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
