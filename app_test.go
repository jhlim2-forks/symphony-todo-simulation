package main

import (
	"bytes"
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
	w := call(h, "POST", "/api/todos", "bad")
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte(messages["invalid_json"])) {
		t.Fatal(w.Body)
	}
	page := call(h, "GET", "/", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "마감일 지우기") {
		t.Fatal(page.Code, page.Body)
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
}

// REQ-13, REQ-14: 마감일은 독립적으로 수정·삭제되고 목록 순서는 유지된다.
func TestDueUpdateAndDisplay(t *testing.T) {
	h := NewHandler()
	call(h, "POST", "/api/todos", `{"title":"가"}`)
	call(h, "POST", "/api/todos", `{"title":"나","due":"2026-03-01"}`)
	w := call(h, "PATCH", "/api/todos/1", `{"due":"2026-04-02"}`)
	v := decodeTodo(t, w.Body.Bytes())
	if v.Due == nil || *v.Due != "2026-04-02" {
		t.Fatal(w.Body)
	}
	if call(h, "PATCH", "/api/todos/1", `{"due":"2026-02-30"}`).Code != 400 {
		t.Fatal("invalid due accepted")
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
