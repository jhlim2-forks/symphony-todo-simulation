package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// REQ-01, REQ-09: New todos receive increasing IDs, start incomplete, and a new store is empty.
func TestCreateAndFreshStore(t *testing.T) {
	h := newHandler(NewStore())
	for i, title := range []string{"장보기", "청소"} {
		w := call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, title))
		if w.Code != 201 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		var got Todo
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != i+1 || got.Title != title || got.Done {
			t.Fatalf("unexpected todo: %+v", got)
		}
	}
	w := call(newHandler(NewStore()), "GET", "/api/todos", "")
	if w.Body.String() != `{"todos":[]}`+"\n" {
		t.Fatalf("empty list: %s", w.Body)
	}
}

// REQ-02, REQ-03: Titles are trimmed, blank or overlong titles rejected, and Unicode code points counted.
func TestTitleValidation(t *testing.T) {
	h := newHandler(NewStore())
	w := call(h, "POST", "/api/todos", `{"title":"  장보기  "}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"title":"장보기"`) {
		t.Fatalf("trim: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct{ title, code string }{{"   ", "title_blank"}, {strings.Repeat("가", 201), "title_too_long"}} {
		w = call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, tc.title))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("%s: %d %s", tc.code, w.Code, w.Body)
		}
	}
	for _, title := range []string{strings.Repeat("가", 200), strings.Repeat("🙂", 200)} {
		w = call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, title))
		if w.Code != 201 {
			t.Fatalf("200 chars rejected: %d %s", w.Code, w.Body)
		}
		var got Todo
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if utf8.RuneCountInString(got.Title) != 200 {
			t.Fatalf("count=%d", utf8.RuneCountInString(got.Title))
		}
	}
}

// REQ-04, REQ-05: Listing preserves insertion order across completion changes and allows idempotent toggles.
func TestOrderAndCompletion(t *testing.T) {
	h := newHandler(NewStore())
	for _, s := range []string{"가", "나", "다"} {
		call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, s))
	}
	for _, done := range []string{"true", "false", "true"} {
		w := call(h, "PATCH", "/api/todos/1", `{"done":`+done+`}`)
		if w.Code != 200 {
			t.Fatalf("patch: %d %s", w.Code, w.Body)
		}
	}
	w := call(h, "GET", "/api/todos", "")
	if !strings.Contains(w.Body.String(), `"title":"가","done":true`) || strings.Index(w.Body.String(), "가") > strings.Index(w.Body.String(), "나") {
		t.Fatalf("order/state: %s", w.Body)
	}
}

// REQ-06, REQ-07: Deleting one item preserves others, while missing or malformed IDs return not found.
func TestDeleteAndMissingIDs(t *testing.T) {
	h := newHandler(NewStore())
	for _, s := range []string{"가", "나", "다"} {
		call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, s))
	}
	call(h, "PATCH", "/api/todos/3", `{"done":true}`)
	w := call(h, "DELETE", "/api/todos/2", "")
	if w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	w = call(h, "GET", "/api/todos", "")
	if strings.Contains(w.Body.String(), "나") || !strings.Contains(w.Body.String(), `"title":"다","done":true`) {
		t.Fatalf("remaining: %s", w.Body)
	}
	for _, path := range []string{"/api/todos/999", "/api/todos/abc"} {
		w = call(h, "PATCH", path, `{"done":false}`)
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

// REQ-08: The page is Korean and its machine-readable error messages exactly match the API contract.
func TestPageAndErrorMessages(t *testing.T) {
	h := newHandler(NewStore())
	w := call(h, "GET", "/", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="error"`) || !strings.Contains(w.Body.String(), `id="filter-slot"`) {
		t.Fatalf("page: %d", w.Code)
	}
	start := strings.Index(w.Body.String(), "<script type=\"application/json\" id=\"error-messages\">") + len("<script type=\"application/json\" id=\"error-messages\">")
	end := strings.Index(w.Body.String()[start:], "</script>") + start
	var msgs struct {
		Default string            `json:"default"`
		Codes   map[string]string `json:"codes"`
	}
	if err := json.Unmarshal([]byte(w.Body.String()[start:end]), &msgs); err != nil {
		t.Fatal(err)
	}
	if msgs.Default != "요청을 처리할 수 없습니다. 잠시 후 다시 시도해 주세요." || len(msgs.Codes) != len(errorMessages) {
		t.Fatalf("message table: %+v", msgs)
	}
	for code, msg := range errorMessages {
		if msgs.Codes[code] != msg {
			t.Errorf("%s: %q != %q", code, msgs.Codes[code], msg)
		}
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		code               string
	}{{"POST", "/api/todos", "no json", 400, "invalid_json"}, {"PUT", "/api/todos", "", 405, "method_not_allowed"}, {"GET", "/api/missing", "", 404, "not_found"}} {
		w = call(h, tc.method, tc.path, tc.body)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
}
