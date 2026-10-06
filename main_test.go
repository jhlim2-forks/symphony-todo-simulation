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
		before := call(h, "GET", "/api/todos", "").Body.String()
		w = call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, tc.title))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("%s: %d %s", tc.code, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("rejected %s title changed list: before=%s after=%s", tc.code, before, after)
		}
	}
	for _, tc := range []struct{ body, code string }{{`{"title":""}`, "title_blank"}, {`{"title":null}`, "invalid_json"}, {`{"title":7}`, "invalid_json"}} {
		before := call(h, "GET", "/api/todos", "").Body.String()
		w = call(h, "POST", "/api/todos", tc.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("invalid title body %s: %d %s", tc.body, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("rejected title changed list: before=%s after=%s", before, after)
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
	for _, tc := range []struct {
		done string
		want bool
	}{{"true", true}, {"true", true}, {"false", false}, {"true", true}} {
		w := call(h, "PATCH", "/api/todos/1", `{"done":`+tc.done+`}`)
		if w.Code != 200 {
			t.Fatalf("patch: %d %s", w.Code, w.Body)
		}
		var updated Todo
		if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil || updated.Done != tc.want {
			t.Fatalf("patch response for done=%s: %+v err=%v", tc.done, updated, err)
		}
		var listed struct {
			Todos []Todo `json:"todos"`
		}
		if err := json.Unmarshal(call(h, "GET", "/api/todos", "").Body.Bytes(), &listed); err != nil || listed.Todos[0].Done != tc.want {
			t.Fatalf("list state for done=%s: %+v err=%v", tc.done, listed, err)
		}
	}
	for _, body := range []string{`{"done":null}`, `{"done":"true"}`} {
		before := call(h, "GET", "/api/todos", "").Body.String()
		w := call(h, "PATCH", "/api/todos/1", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"done_required"`) {
			t.Fatalf("invalid done %s: %d %s", body, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("rejected update changed list: before=%s after=%s", before, after)
		}
	}
	w := call(h, "PATCH", "/api/todos/1", `{}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"nothing_to_update"`) {
		t.Fatalf("empty patch: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/api/todos", "")
	if !strings.Contains(w.Body.String(), `"title":"가","done":true`) || strings.Index(w.Body.String(), "가") > strings.Index(w.Body.String(), "나") {
		t.Fatalf("order/state: %s", w.Body)
	}
}

// REQ-11, REQ-12, REQ-13, REQ-14: Deadlines are optional, calendar-valid dates and independently editable.
func TestDeadlines(t *testing.T) {
	h := newHandler(NewStore())
	add := call(h, "POST", "/api/todos", `{"title":"작업","due":"2024-02-29"}`)
	if add.Code != 201 || !strings.Contains(add.Body.String(), `"due":"2024-02-29"`) {
		t.Fatalf("valid due: %d %s", add.Code, add.Body)
	}
	if w := call(h, "POST", "/api/todos", `{"title":"기본"}`); w.Code != 201 || !strings.Contains(w.Body.String(), `"due":null`) {
		t.Fatalf("missing due: %d %s", w.Code, w.Body)
	}
	for _, date := range []string{"2026-02-30", "2026-13-01", "2026-02-29", "2026-3-1", "03/01/2026", "2026-03-01T10:00:00Z"} {
		before := call(h, "GET", "/api/todos", "").Body.String()
		w := call(h, "POST", "/api/todos", fmt.Sprintf(`{"title":"bad","due":%q}`, date))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"due_invalid"`) {
			t.Fatalf("invalid due %s: %d %s", date, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("invalid due changed list")
		}
	}
	for _, body := range []string{`{"due":"2026-03-01"}`, `{"due":"2026-04-02"}`, `{"due":null}`} {
		w := call(h, "PATCH", "/api/todos/2", body)
		if w.Code != 200 {
			t.Fatalf("patch due: %d %s", w.Code, w.Body)
		}
	}
	list := call(h, "GET", "/api/todos", "").Body.String()
	if !strings.Contains(list, `"title":"작업","done":false,"due":"2024-02-29"`) || !strings.Contains(list, `"title":"기본","done":false,"due":null`) {
		t.Fatalf("due updates changed other fields: %s", list)
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
	w = call(h, "DELETE", "/api/todos/2", "")
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"not_found"`) {
		t.Fatalf("repeated delete: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/api/todos", "")
	if strings.Contains(w.Body.String(), "나") || !strings.Contains(w.Body.String(), `"title":"다","done":true`) {
		t.Fatalf("remaining: %s", w.Body)
	}
	for _, path := range []string{"/api/todos/999", "/api/todos/abc"} {
		before := call(h, "GET", "/api/todos", "").Body.String()
		w = call(h, "PATCH", path, `{"done":false}`)
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("missing ID request changed list: before=%s after=%s", before, after)
		}
		w = call(h, "DELETE", path, "")
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"not_found"`) {
			t.Fatalf("DELETE %s: %d %s", path, w.Code, w.Body)
		}
		if after := call(h, "GET", "/api/todos", "").Body.String(); after != before {
			t.Fatalf("missing ID delete changed list: before=%s after=%s", before, after)
		}
	}
}

// REQ-08: The page is Korean and its machine-readable error messages exactly match the API contract.
func TestPageAndErrorMessages(t *testing.T) {
	h := newHandler(NewStore())
	contract := map[string]string{
		"invalid_json": "요청 형식이 올바르지 않습니다.", "title_blank": "할 일 제목을 입력해 주세요.",
		"title_too_long": "할 일 제목은 200자까지 입력할 수 있습니다.", "done_required": "완료 여부(done)를 true 또는 false로 보내 주세요.",
		"due_invalid": "마감일은 2026-03-01처럼 실제로 있는 날짜로 입력해 주세요.", "nothing_to_update": "바꿀 내용을 보내 주세요. 완료 여부(done) 또는 마감일(due)이 필요합니다.",
		"not_found": "해당 할 일을 찾을 수 없습니다.", "method_not_allowed": "허용되지 않은 요청 방식입니다.",
	}
	if len(errorMessages) != len(contract) {
		t.Fatalf("server error map has unexpected codes: %+v", errorMessages)
	}
	for code, msg := range contract {
		if errorMessages[code] != msg {
			t.Errorf("server contract %s: %q", code, errorMessages[code])
		}
	}
	// Compare actual API responses with the documented contract too. Comparing
	// only the page and server maps would let both drift together unnoticed.
	for _, tc := range []struct {
		method, path, body string
		code               string
	}{
		{"POST", "/api/todos", "not json", "invalid_json"},
		{"POST", "/api/todos", `{"title":"   "}`, "title_blank"},
		{"POST", "/api/todos", fmt.Sprintf(`{"title":%q}`, strings.Repeat("가", 201)), "title_too_long"},
		{"PATCH", "/api/todos/1", `{"done":null}`, "done_required"},
		{"POST", "/api/todos", `{"title":"작업","due":"2026-02-30"}`, "due_invalid"},
		{"PATCH", "/api/todos/1", `{}`, "nothing_to_update"},
		{"DELETE", "/api/todos/999", "", "not_found"},
		{"PUT", "/api/todos", "", "method_not_allowed"},
	} {
		response := call(h, tc.method, tc.path, tc.body)
		var got struct {
			Error apiError `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s response: %v (%s)", tc.code, err, response.Body)
		}
		if got.Error.Code != tc.code || got.Error.Message != contract[tc.code] {
			t.Errorf("%s response: got code=%q message=%q, want message=%q", tc.code, got.Error.Code, got.Error.Message, contract[tc.code])
		}
	}
	w := call(h, "GET", "/", "")
	page := w.Body.String()
	if w.Code != 200 || !strings.Contains(page, `id="error"`) || !strings.Contains(page, `id="filter-slot"`) || !strings.Contains(page, `id="title"`) || !strings.Contains(page, `id="add-form"`) || !strings.Contains(page, `id="todos"`) || !strings.Contains(page, "할 일 목록") || !strings.Contains(page, "추가") || !strings.Contains(page, "삭제") {
		t.Fatalf("page: %d", w.Code)
	}
	for _, msg := range contract {
		if strings.Count(page, msg) != 1 {
			t.Fatalf("error message %q must appear only once in the page", msg)
		}
	}
	if !strings.Contains(page, `id="error" role="alert"></p>`) {
		t.Fatal("error area must initially be empty")
	}
	start := strings.Index(page, "<script type=\"application/json\" id=\"error-messages\">") + len("<script type=\"application/json\" id=\"error-messages\">")
	if start < len("<script type=\"application/json\" id=\"error-messages\">") {
		t.Fatal("missing machine-readable error table")
	}
	end := strings.Index(page[start:], "</script>") + start
	var msgs struct {
		Default string            `json:"default"`
		Codes   map[string]string `json:"codes"`
	}
	if err := json.Unmarshal([]byte(w.Body.String()[start:end]), &msgs); err != nil {
		t.Fatal(err)
	}
	if msgs.Default != "요청을 처리할 수 없습니다. 잠시 후 다시 시도해 주세요." || len(msgs.Codes) != len(contract) {
		t.Fatalf("message table: %+v", msgs)
	}
	for code, msg := range contract {
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
