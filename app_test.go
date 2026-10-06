package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// REQ-08: 화면의 오류 표와 API 오류는 정해진 한국어 문구를 사용한다.
func TestErrorMessagesAndPage(t *testing.T) {
	h := NewHandler()
	w := call(h, "POST", "/api/todos", "bad")
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte(messages["invalid_json"])) {
		t.Fatal(w.Body)
	}
	page := call(h, "GET", "/", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="error-messages"`) || !strings.Contains(page.Body.String(), "마감일 지우기") {
		t.Fatal(page.Code, page.Body)
	}
	for _, v := range messages {
		if !strings.Contains(page.Body.String(), v) {
			t.Fatalf("page lacks %q", v)
		}
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
