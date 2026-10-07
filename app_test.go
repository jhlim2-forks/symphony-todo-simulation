package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func decodeTodo(t *testing.T, b []byte) Todo {
	t.Helper()
	var v Todo
	if e := json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}

// REQ-01, REQ-09, REQ-11: 새 할 일은 순차 ID, 미완료 상태, nullable 마감일로 메모리에 추가된다.
func TestCreateAndList(t *testing.T) {
	h := NewHandler()
	w := call(h, "POST", "/api/todos", `{"title":"장보기","due":"2026-03-01"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	v := decodeTodo(t, w.Body.Bytes())
	if v.ID != 1 || v.Done || v.Due == nil || *v.Due != "2026-03-01" {
		t.Fatalf("%+v", v)
	}
	w = call(h, "POST", "/api/todos", `{"title":"청소"}`)
	v = decodeTodo(t, w.Body.Bytes())
	if v.ID != 2 || v.Due != nil {
		t.Fatalf("%+v", v)
	}
	w = call(h, "GET", "/api/todos", "")
	if !strings.Contains(w.Body.String(), `"due":null`) {
		t.Fatal(w.Body.String())
	}
	if got := call(NewHandler(), "GET", "/api/todos", "").Body.String(); got != "{\"todos\":[]}\n" {
		t.Fatal(got)
	}
}

// REQ-02, REQ-03: 제목은 공백 제거 후 비어 있지 않고 유니코드 문자 200자 이하여야 한다.
func TestTitleValidation(t *testing.T) {
	h := NewHandler()
	w := call(h, "POST", "/api/todos", `{"title":"  장보기  "}`)
	if decodeTodo(t, w.Body.Bytes()).Title != "장보기" {
		t.Fatal(w.Body)
	}
	for _, tc := range []struct{ body, code string }{{`{"title":"  "}`, "title_blank"}, {`{"title":"` + strings.Repeat("가", 201) + `"}`, "title_too_long"}} {
		w = call(h, "POST", "/api/todos", tc.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s: %d %s", tc.code, w.Code, w.Body)
		}
	}
	w = call(h, "POST", "/api/todos", `{"title":"`+strings.Repeat("🙂", 200)+`"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
}

// REQ-04, REQ-05, REQ-06, REQ-07: 완료 변경과 삭제는 상태·순서를 보존하고 없는 ID를 거절한다.
func TestStateOrderingDeleteAndMissing(t *testing.T) {
	h := NewHandler()
	for _, s := range []string{"가", "나", "다"} {
		call(h, "POST", "/api/todos", `{"title":"`+s+`"}`)
	}
	w := call(h, "PATCH", "/api/todos/1", `{"done":true}`)
	if decodeTodo(t, w.Body.Bytes()).Done != true {
		t.Fatal(w.Body)
	}
	w = call(h, "PATCH", "/api/todos/1", `{"done":false}`)
	if decodeTodo(t, w.Body.Bytes()).Done {
		t.Fatal(w.Body)
	}
	if w = call(h, "DELETE", "/api/todos/2", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = call(h, "GET", "/api/todos", "")
	if strings.Index(w.Body.String(), "\"title\":\"가\"") > strings.Index(w.Body.String(), "\"title\":\"다\"") {
		t.Fatal(w.Body)
	}
	if call(h, "DELETE", "/api/todos/2", "").Code != 404 || call(h, "PATCH", "/api/todos/abc", `{"done":true}`).Code != 404 {
		t.Fatal("missing ID accepted")
	}
}

// REQ-05 (#7): PATCH에서 null은 유효한 완료 여부가 아니며 기존 상태를 바꾸지 않는다.
func TestPatchDoneNullIsRejectedWithoutChangingState(t *testing.T) {
	h := NewHandler()
	call(h, "POST", "/api/todos", `{"title":"완료된 할 일"}`)
	if w := call(h, "PATCH", "/api/todos/1", `{"done":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := call(h, "PATCH", "/api/todos/1", `{"done":null}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"done_required"`) || !strings.Contains(w.Body.String(), messages["done_required"]) {
		t.Fatalf("expected done_required, got %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/api/todos", "")
	if !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatalf("null changed the completed state: %s", w.Body)
	}
}

// REQ-08 (#7): HTML의 오류 안내 표와 서버 응답은 API 계약의 여덟 문구 및 405 응답과 일치한다.
func TestErrorMessagesAndPage(t *testing.T) {
	h := NewHandler()
	page := call(h, "GET", "/", "")
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body)
	}
	pageHTML := page.Body.String()
	for _, element := range []string{
		`<input id="title"`,
		`<input id="new-due" type="date">`,
		`<button>추가</button>`,
		`<div id="todos"></div>`,
		`<p id="error" aria-live="polite"></p>`,
	} {
		if !strings.Contains(pageHTML, element) {
			t.Errorf("page is missing required screen element %q", element)
		}
	}

	contract := map[string]string{
		"invalid_json":       "요청 형식이 올바르지 않습니다.",
		"title_blank":        "할 일 제목을 입력해 주세요.",
		"title_too_long":     "할 일 제목은 200자까지 입력할 수 있습니다.",
		"done_required":      "완료 여부(done)를 true 또는 false로 보내 주세요.",
		"due_invalid":        "마감일은 2026-03-01처럼 실제로 있는 날짜로 입력해 주세요.",
		"nothing_to_update":  "바꿀 내용을 보내 주세요. 완료 여부(done) 또는 마감일(due)이 필요합니다.",
		"not_found":          "해당 할 일을 찾을 수 없습니다.",
		"method_not_allowed": "허용되지 않은 요청 방식입니다.",
	}
	const defaultMessage = "요청을 처리할 수 없습니다. 잠시 후 다시 시도해 주세요."
	matcher := regexp.MustCompile(`<script type="application/json" id="error-messages">([\s\S]*?)</script>`)
	matches := matcher.FindStringSubmatch(page.Body.String())
	if len(matches) != 2 {
		t.Fatal("error-messages JSON element missing")
	}
	var payload struct {
		Default string            `json:"default"`
		Codes   map[string]string `json:"codes"`
	}
	if err := json.Unmarshal([]byte(matches[1]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Default != defaultMessage || len(payload.Codes) != len(contract) {
		t.Fatalf("unexpected page error table: %+v", payload)
	}
	for code, message := range contract {
		if payload.Codes[code] != message {
			t.Errorf("page code %q: got %q, want %q", code, payload.Codes[code], message)
		}
	}
	if len(payload.Codes) != len(contract) {
		t.Fatalf("page has unexpected error codes: %v", payload.Codes)
	}
	if !strings.Contains(page.Body.String(), "마감일 없음") ||
		!strings.Contains(page.Body.String(), "마감일: '+t.due") ||
		!strings.Contains(page.Body.String(), "type:'date'") ||
		!strings.Contains(page.Body.String(), "마감일 지우기") {
		t.Fatal("page is missing a due date display, empty label, date input, or clear button")
	}

	// Compare every API response to the approved contract text, independently of
	// the server's message table so a wrong server message cannot validate itself.
	requests := []struct {
		method, path, body, code string
	}{
		{"POST", "/api/todos", "bad", "invalid_json"},
		{"POST", "/api/todos", `{"title":"  "}`, "title_blank"},
		{"POST", "/api/todos", `{"title":"` + strings.Repeat("가", 201) + `"}`, "title_too_long"},
		{"PATCH", "/api/todos/1", `{"done":null}`, "done_required"},
		{"POST", "/api/todos", `{"title":"x","due":"2026-02-30"}`, "due_invalid"},
		{"PATCH", "/api/todos/1", `{}`, "nothing_to_update"},
		{"PATCH", "/api/todos/999", `{"due":null}`, "not_found"},
	}
	for _, tc := range requests {
		w := call(h, tc.method, tc.path, tc.body)
		var response struct {
			Error struct {
				Code, Message string
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Code != tc.code || response.Error.Message != contract[tc.code] {
			t.Errorf("%s response = %+v, want %q: %q", tc.code, response.Error, tc.code, contract[tc.code])
		}
	}

	put := call(h, "PUT", "/api/todos", "")
	var api struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if put.Code != 405 {
		t.Fatalf("PUT status = %d, want 405", put.Code)
	}
	if err := json.Unmarshal(put.Body.Bytes(), &api); err != nil {
		t.Fatal(err)
	}
	if api.Error.Code != "method_not_allowed" || api.Error.Message != contract["method_not_allowed"] {
		t.Fatalf("unexpected 405 response: %+v", api)
	}
}

// REQ-10: 화면은 기본 전체 선택, 세 필터와 빈 결과별 안내를 제공하고 필터 선택은 화면 안에서 처리한다.
func TestTodoFilterUI(t *testing.T) {
	page := call(NewHandler(), "GET", "/", "")
	html := page.Body.String()
	for _, want := range []string{
		`name="filter" value="all" checked`,
		`name="filter" value="active"`,
		`name="filter" value="done"`,
		`id="filter-messages"`,
		`"all":"할 일이 없습니다."`,
		`"active":"진행 중인 할 일이 없습니다."`,
		`"done":"완료한 할 일이 없습니다."`,
		`selectedFilter='all'`,
		`allTodos.filter(t=>selectedFilter==='all'||(selectedFilter==='active'&&!t.done)||(selectedFilter==='done'&&t.done))`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("filter UI is missing %q", want)
		}
	}
	if got := call(NewHandler(), "GET", "/api/todos", "").Body.String(); got != "{\"todos\":[]}\n" {
		t.Fatalf("filter must not alter the server list: %s", got)
	}
}

// REQ-15, REQ-18 (#14): 완료 비우기는 필터 상태와 무관하게 전체 완료 건수를 확인하고 서버 응답 목록으로 화면을 갱신한다.
func TestClearCompletedUsesFullListAndResponse(t *testing.T) {
	page := call(NewHandler(), "GET", "/", "")
	html := page.Body.String()
	for _, want := range []string{
		"function render(todos=allTodos){allTodos=todos;",
		"const count=allTodos.filter(t=>t.done).length",
		"render(result.todos)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("clear completed UI is missing behavior %q", want)
		}
	}
	if strings.Contains(html, `querySelectorAll('#todos .todo')`) {
		t.Fatal("clear completed count must not depend on the visible filtered rows")
	}
}

// REQ-12: 날짜는 형식에 맞고 달력에 실제 존재할 때만 허용된다.
func TestDueDateValidation(t *testing.T) {
	h := NewHandler()
	for _, d := range []string{"2026-02-30", "2026-13-01", "2026-02-29", "2026-3-1", "03/01/2026"} {
		w := call(h, "POST", "/api/todos", `{"title":"x","due":"`+d+`"}`)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "due_invalid") {
			t.Fatalf("%s: %d %s", d, w.Code, w.Body)
		}
	}
	if w := call(h, "POST", "/api/todos", `{"title":"x","due":"2024-02-29"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	call(h, "POST", "/api/todos", `{"title":"기존 마감일","due":"2026-03-01"}`)
	if w := call(h, "PATCH", "/api/todos/2", `{"due":"2026-02-30"}`); w.Code != 400 {
		t.Fatalf("invalid PATCH due: %d %s", w.Code, w.Body)
	}
	var listed struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(call(h, "GET", "/api/todos", "").Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Todos) != 2 || listed.Todos[1].Due == nil || *listed.Todos[1].Due != "2026-03-01" {
		t.Fatalf("invalid due changed existing data: %+v", listed.Todos)
	}
}

// REQ-11: 마감일은 날짜 문자열 또는 null이어야 하며 다른 JSON 값은 거절하고 저장하지 않는다.
func TestDueRejectsNonStringValues(t *testing.T) {
	h := NewHandler()
	call(h, "POST", "/api/todos", `{"title":"기존 할 일","due":"2026-03-01"}`)
	for _, due := range []string{`1`, `true`, `{}`} {
		for _, tc := range []struct {
			method, path, body string
		}{
			{"POST", "/api/todos", `{"title":"잘못된 마감일","due":` + due + `}`},
			{"PATCH", "/api/todos/1", `{"due":` + due + `}`},
		} {
			w := call(h, tc.method, tc.path, tc.body)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_json"`) {
				t.Fatalf("%s due via %s: expected invalid_json, got %d %s", due, tc.method, w.Code, w.Body)
			}
		}
	}
	var listed struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(call(h, "GET", "/api/todos", "").Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Todos) != 1 || listed.Todos[0].Title != "기존 할 일" || listed.Todos[0].Due == nil || *listed.Todos[0].Due != "2026-03-01" {
		t.Fatalf("invalid due values changed stored todos: %+v", listed.Todos)
	}
}

// REQ-13, REQ-14: 마감일은 독립적으로 수정·삭제되고 목록 순서는 유지된다.
func TestDueUpdateAndDisplay(t *testing.T) {
	h := NewHandler()
	call(h, "POST", "/api/todos", `{"title":"가"}`)
	call(h, "POST", "/api/todos", `{"title":"나","due":"2026-03-01"}`)
	call(h, "PATCH", "/api/todos/1", `{"done":true}`)
	w := call(h, "PATCH", "/api/todos/1", `{"due":"2026-04-02"}`)
	v := decodeTodo(t, w.Body.Bytes())
	if v.Due == nil || *v.Due != "2026-04-02" || v.Title != "가" || !v.Done {
		t.Fatal(w.Body)
	}
	w = call(h, "PATCH", "/api/todos/1", `{"done":false}`)
	v = decodeTodo(t, w.Body.Bytes())
	if v.Due == nil || *v.Due != "2026-04-02" || v.Done {
		t.Fatalf("done-only PATCH changed due date: %+v", v)
	}
	call(h, "PATCH", "/api/todos/1", `{"done":true}`)
	if call(h, "PATCH", "/api/todos/1", `{"due":"2026-02-30"}`).Code != 400 {
		t.Fatal("invalid due accepted")
	}
	w = call(h, "GET", "/api/todos", "")
	var listed struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Todos) != 2 || listed.Todos[0].Title != "가" || !listed.Todos[0].Done || listed.Todos[0].Due == nil || *listed.Todos[0].Due != "2026-04-02" || listed.Todos[1].Title != "나" || listed.Todos[1].Due == nil || *listed.Todos[1].Due != "2026-03-01" {
		t.Fatalf("due update changed unrelated data: %+v", listed.Todos)
	}
	call(h, "PATCH", "/api/todos/1", `{"due":null}`)
	w = call(h, "GET", "/api/todos", "")
	if !strings.Contains(w.Body.String(), `"due":null`) || strings.Index(w.Body.String(), "가") > strings.Index(w.Body.String(), "나") {
		t.Fatal(w.Body)
	}
	if call(h, "PATCH", "/api/todos/1", `{}`).Code != 400 {
		t.Fatal("empty patch accepted")
	}
}

// REQ-15, REQ-16: 완료 비우기 버튼은 처음 잠겨 있고, 완료 개수를 보여 주는 취소 가능한 확인 문구를 둔다.
func TestClearCompletedConfirmationUI(t *testing.T) {
	page := call(NewHandler(), "GET", "/", "")
	html := page.Body.String()
	if page.Code != http.StatusOK || strings.Count(html, `id="clear-completed"`) != 1 || !strings.Contains(html, `id="clear-completed" disabled`) {
		t.Fatalf("missing initially disabled clear button: %d", page.Code)
	}
	matcher := regexp.MustCompile(`<script type="application/json" id="confirm-messages">([\s\S]*?)</script>`)
	matches := matcher.FindStringSubmatch(html)
	if len(matches) != 2 {
		t.Fatal("confirmation message JSON missing")
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(matches[1]), &payload); err != nil {
		t.Fatal(err)
	}
	want := "완료한 할 일 {count}개를 지웁니다. 지운 항목은 되살릴 수 없습니다. 지우시겠습니까?"
	errorMatcher := regexp.MustCompile(`<script type="application/json" id="error-messages">([\s\S]*?)</script>`)
	errorMatches := errorMatcher.FindStringSubmatch(html)
	var errorPayload struct {
		Codes map[string]string `json:"codes"`
	}
	if len(errorMatches) != 2 || json.Unmarshal([]byte(errorMatches[1]), &errorPayload) != nil {
		t.Fatal("error message JSON missing or invalid")
	}
	if payload["clear_completed"] != want || errorPayload.Codes["clear_completed"] != "" {
		t.Fatalf("unexpected confirmation message: %v", payload)
	}
	if !strings.Contains(html, "window.confirm(confirmMessages.clear_completed.replace('{count}',count))") || !strings.Contains(html, "if(!count||!") {
		t.Fatal("confirmation does not report the displayed completed count or stop on cancel/zero")
	}
}

// REQ-17, REQ-18 (#23): 일괄 삭제는 완료 항목만 제거하고 진행 중 항목의 내용·순서·마감일을 보존하며 개수와 남은 목록을 돌려준다.
func TestDeleteCompletedPreservesInProgressTodosAndIDs(t *testing.T) {
	h := NewHandler()
	for _, body := range []string{
		`{"title":"가","due":"2026-03-01"}`,
		`{"title":"나","due":"2026-04-02"}`,
		`{"title":"다"}`,
		`{"title":"라","due":"2026-05-03"}`,
	} {
		if w := call(h, "POST", "/api/todos", body); w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
	}
	for _, id := range []string{"1", "3"} {
		if w := call(h, "PATCH", "/api/todos/"+id, `{"done":true}`); w.Code != http.StatusOK {
			t.Fatalf("complete %s: %d", id, w.Code)
		}
	}
	response := call(h, "DELETE", "/api/todos/completed", "")
	if response.Code != http.StatusOK {
		t.Fatalf("bulk delete: %d %s", response.Code, response.Body)
	}
	var result struct {
		Deleted int    `json:"deleted"`
		Todos   []Todo `json:"todos"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 2 || len(result.Todos) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	want := []Todo{{ID: 2, Title: "나", Done: false, Due: ptr("2026-04-02")}, {ID: 4, Title: "라", Done: false, Due: ptr("2026-05-03")}}
	for i := range want {
		if result.Todos[i].ID != want[i].ID || result.Todos[i].Title != want[i].Title || result.Todos[i].Done != want[i].Done || result.Todos[i].Due == nil || *result.Todos[i].Due != *want[i].Due {
			t.Fatalf("in-progress todo changed: got %+v want %+v", result.Todos[i], want[i])
		}
	}
	listed := call(h, "GET", "/api/todos", "")
	if !strings.Contains(listed.Body.String(), `"id":2`) || !strings.Contains(listed.Body.String(), `"id":4`) || strings.Contains(listed.Body.String(), `"id":1`) || strings.Contains(listed.Body.String(), `"id":3`) {
		t.Fatalf("server list does not match bulk response: %s", listed.Body)
	}
	created := call(h, "POST", "/api/todos", `{"title":"마지막"}`)
	if v := decodeTodo(t, created.Body.Bytes()); v.ID != 5 {
		t.Fatalf("deleted ID reused: %+v", v)
	}
}

// REQ-18 (#23): 완료 항목이 없을 때 일괄 삭제는 오류 없이 0개와 그대로인 목록을 반환한다.
func TestDeleteCompletedWhenNothingIsCompleted(t *testing.T) {
	h := NewHandler()
	call(h, "POST", "/api/todos", `{"title":"진행 중"}`)
	for _, expected := range []int{0, 0} {
		w := call(h, "DELETE", "/api/todos/completed", "")
		var result struct {
			Deleted int    `json:"deleted"`
			Todos   []Todo `json:"todos"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Deleted != expected || len(result.Todos) != 1 || result.Todos[0].Title != "진행 중" {
			t.Fatalf("unexpected no-op response: %d %+v (%v)", w.Code, result, err)
		}
	}
	empty := call(NewHandler(), "DELETE", "/api/todos/completed", "")
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"deleted":0`) || !strings.Contains(empty.Body.String(), `"todos":[]`) {
		t.Fatalf("unexpected empty response: %d %s", empty.Code, empty.Body)
	}
	if w := call(h, "GET", "/api/todos/completed", ""); w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("unexpected method handling: %d %s", w.Code, w.Body)
	}
}

func ptr(s string) *string { return &s }
