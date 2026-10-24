package copyengine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyHistoryImport(t *testing.T) {
	dir := t.TempDir()
	prior := filepath.Join(dir, "legacy.json")
	jobs := []*Job{
		{ID: "old1", State: "completed", Updated: time.Now().Add(-3 * time.Hour), Sources: []string{"/vol1/1000/photos/photo.jpg"}, Target: "/vol1/1000/backup", TotalBytes: 12},
		{ID: "old2", State: "completed", Updated: time.Now().Add(-2 * time.Hour), Sources: []string{"/vol1/1000/photos/a.jpg"}, Target: "/vol1/1000/backup", TotalBytes: 22},
		{ID: "old3", State: "completed", Updated: time.Now().Add(-time.Hour), Sources: []string{"/vol1/1000/photos/b.jpg"}, Target: "/vol1/1000/backup", TotalBytes: 32},
		{ID: "old4", State: "completed", Updated: time.Now(), Sources: []string{"/vol1/1000/photos/c.jpg"}, Target: "/vol1/1000/backup", TotalBytes: 42},
		{ID: "running", State: "running", Updated: time.Now()},
	}
	data, e := json.Marshal(map[string]any{"jobs": jobs})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(prior, data, 0600); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "mountapp", "private", "copies.json")
	engine, e := NewManager(file, "/usr/bin/rclone")
	if e != nil {
		t.Fatal(e)
	}
	added, e := engine.ImportCompletedHistory(prior)
	if e != nil {
		t.Fatal(e)
	}
	if added != 3 {
		t.Fatalf("import count %d", added)
	}
	if len(engine.jobs) != 3 {
		t.Fatalf("retention failed: %d", len(engine.jobs))
	}
	for _, j := range engine.jobs {
		if j.ID == "running" || j.ID == "old1" {
			t.Fatalf("wrong imported job %s", j.ID)
		}
	}
	added, e = engine.ImportCompletedHistory(prior)
	if e != nil {
		t.Fatal(e)
	}
	if added != 0 {
		t.Fatalf("legacy import should be idempotent, got %d", added)
	}
	if len(engine.jobs) != 3 {
		t.Fatal("history exceeded retention")
	}
	loaded, e := NewManager(file, "/usr/bin/rclone")
	if e != nil {
		t.Fatal(e)
	}
	if len(loaded.jobs) != 3 {
		t.Fatal("persisted history missing")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/detail?id=old4", nil)
	w := httptest.NewRecorder()
	loaded.Serve(w, req)
	if w.Code != 200 {
		t.Fatalf("detail status %d: %s", w.Code, w.Body)
	}
	var detail Job
	if e = json.Unmarshal(w.Body.Bytes(), &detail); e != nil {
		t.Fatal(e)
	}
	if detail.Target != "/vol1/1000/backup" || len(detail.Sources) != 1 {
		t.Fatalf("detail mismatch %+v", detail)
	}
}
