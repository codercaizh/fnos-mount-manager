package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fnos-mount-manager/internal/copyengine"
)

func TestLegacyAndMergedCopyPauseRoutes(t *testing.T) {
	for _, route := range []string{"api/pause", "api/copy/pause"} {
		t.Run(route, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "copies.json")
			state := `{"jobs":[{"id":"pausetest","sources":["/vol1/1000/share/source"],"target":"/vol1/1000/share/backup","transfers":8,"verify":"size","state":"queued","created":"2026-10-24T22:00:00Z","updated":"2026-10-24T22:00:00Z"}]}`
			if e := os.WriteFile(file, []byte(state), 0600); e != nil {
				t.Fatal(e)
			}
			manager, e := copyengine.NewManager(file, "/tmp/not-used")
			if e != nil {
				t.Fatal(e)
			}
			store := &Store{copier: manager}
			request := httptest.NewRequest(http.MethodPost, base+route, strings.NewReader(`{"id":"pausetest"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Fnos-Quickcopy-UI", "1")
			request.Header.Set("Origin", "https://fnos.example.net:5667")
			w := httptest.NewRecorder()
			store.serve(w, request)
			if w.Code != 409 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var out map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatalf("expected JSON compatibility response, got %s: %v", w.Body, e)
			}
			if out["error"] != "任务当前不能暂停" {
				t.Fatalf("handler returned wrong error: %+v", out)
			}

		})
	}
}

func TestLegacyPauseNeedsCopyHeader(t *testing.T) {
	manager, e := copyengine.NewManager(filepath.Join(t.TempDir(), "missing.json"), "/tmp/not-used")
	if e != nil {
		t.Fatal(e)
	}
	s := &Store{copier: manager}
	req := httptest.NewRequest(http.MethodPost, base+"api/pause", strings.NewReader(`{"id":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.serve(w, req)
	if w.Code == 200 {
		t.Fatal("unmarked legacy route unexpectedly granted")
	}
}
