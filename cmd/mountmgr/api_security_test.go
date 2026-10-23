package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postJSON(s *Store, route, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://unix-proxy.local"+base+strings.TrimPrefix(route, "/"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fnos-Mount-UI", "1")
	req.Header.Set("Origin", "https://fnos.example.test:5667")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.serve(w, req)
	return w
}
func TestAPIRequestValidation(t *testing.T) {
	s := &Store{file: filepath.Join(t.TempDir(), "mounts.json"), Records: []*Record{}}
	tests := []struct {
		method, route, body string
		status              int
	}{
		{"POST", "api/create", `{"Source":"/etc","Parent":"/vol1/1000/share","Name":"demo","Mode":"ro"}`, 400},
		{"POST", "api/create", `{"Source":"/vol02/1000-0-abc","Parent":"/etc","Name":"demo","Mode":"ro"}`, 400},
		{"POST", "api/create", `{"Source":"/vol02/1000-0-abc","Parent":"/vol1/1000/share","Name":"a/b","Mode":"ro"}`, 400},
		{"POST", "api/create", `{"Source":"/vol02/1000-0-abc","Parent":"/vol1/1000/share","Name":"demo","Mode":"root"}`, 400},
		{"POST", "api/create", `{"Source":"/vol1/1000/share/photos","Parent":"/vol1/1000/share/photos","Name":"nested","Mode":"rw"}`, 400},
		{"POST", "api/create", `{`, 400},
		{"POST", "api/remove", `{"ID":"absent"}`, 404},
		{"POST", "api/retry", `{"ID":"absent"}`, 404},
		{"POST", "api/rename", `{"ID":"absent","Name":"ok"}`, 404},
		{"GET", "api/create", "", 404},
	}
	for _, test := range tests {
		var w *httptest.ResponseRecorder
		if test.method == "POST" {
			w = postJSON(s, test.route, test.body)
		} else {
			req := httptest.NewRequest("GET", base+test.route, nil)
			w = httptest.NewRecorder()
			s.serve(w, req)
		}
		if w.Code != test.status {
			t.Errorf("%s %s body=%s status=%d want=%d", test.method, test.route, test.body, w.Code, test.status)
		}
	}
}
func TestStaticAndStatusRoutes(t *testing.T) {
	s := &Store{file: filepath.Join(t.TempDir(), "mounts.json"), Records: []*Record{}}
	for _, tc := range []struct{ path, ctype string }{
		{base, "text/html"}, {base + "app.js", "text/javascript"}, {base + "style.css", "text/css"},
		{base + "fnos-sdk.js", "text/javascript"}, {base + "auth-callback.html", "text/html"},
		{base + "icon.png", "image/png"}, {base + "api/status", "application/json"},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		w := httptest.NewRecorder()
		s.serve(w, req)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.ctype) || w.Body.Len() == 0 {
			t.Errorf("%s status=%d ctype=%s len=%d", tc.path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s missing no-store", tc.path)
		}
	}
	req := httptest.NewRequest("GET", base+"api/status", nil)
	w := httptest.NewRecorder()
	s.serve(w, req)
	var data struct {
		Records []*Record `json:"records"`
		Root    bool      `json:"root"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.Records == nil {
		t.Errorf("status JSON=%s err=%v", w.Body.String(), err)
	}
}
func TestPathBoundaryChecks(t *testing.T) {
	cases := []struct {
		path    string
		allowed bool
	}{
		{"/vol02/1000-0-abc/folder", true}, {"/vol02", false}, {"/vol02/1234567", false},
		{"/vol2/1000/share/爸爸", true}, {"/vol1/1000/@appdata/secret", false},
		{"/vol1/1000/share/@secret", false}, {"/vol1/1000/share", true},
		{"/vol1/abc/share", false}, {"/vol03/1000-foo", false},
	}
	for _, v := range cases {
		if sourceAllowed(v.path) != v.allowed {
			t.Errorf("path %q expected allowed=%t", v.path, v.allowed)
		}
	}
	for _, name := range []string{"normal.txt", "数据 2026", "相册-备份"} {
		if e := strictName(name); e != nil {
			t.Errorf("name %q: %v", name, e)
		}
	}
	for _, name := range []string{".hidden", "@app", "..", "a/b", "a\\b", "a\nb", strings.Repeat("中", 60)} {
		if strictName(name) == nil {
			t.Errorf("unsafe name accepted %q", name)
		}
	}
}
func TestLoadRejectsInvalidPersistentRecords(t *testing.T) {
	testCases := []string{
		`{"records":[null]}`,
		`{"records":[{"id":"x","source":"/etc","target":"/vol1/1000/share/x","mode":"rw"}]}`,
		`{"records":[{"id":"x","source":"/vol02/1000-0-abc","target":"/etc/payload","mode":"rw"}]}`,
		`{"records":[{"id":"x","source":"/vol02/1000-0-abc","target":"/vol1/1000/share/x","mode":"bad"}]}`,
		`{"records":[{"id":"x","source":"/vol02/1000-0-abc","target":"/vol1/1000/share/x","mode":"rw"},{"id":"x","source":"/vol02/1000-0-def","target":"/vol1/1000/share/y","mode":"ro"}]}`,
	}
	for _, jsonText := range testCases {
		p := filepath.Join(t.TempDir(), "state.json")
		if e := os.WriteFile(p, []byte(jsonText), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := load(p); e == nil {
			t.Errorf("invalid record accepted: %.110s", jsonText)
		}
	}
}
