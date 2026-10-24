package copyengine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func awaitJob(t *testing.T, m *Manager, id string) *Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		m.mu.Lock()
		j := m.lookup(id)
		if j == nil {
			m.mu.Unlock()
			t.Fatal("task missing")
		}
		result := *j
		m.mu.Unlock()
		if result.State == "completed" {
			return &result
		}
		if result.State == "error" {
			t.Fatalf("job %s failed: %s", id, result.Error)
		}
	}
	t.Fatalf("job %s timed out", id)
	return nil
}

// Exercises the actual Go manager, real rclone and fnOS-format storage paths.
// No production shares or existing task records are touched.
func TestRealCombinedJobAndIncrementalRestart(t *testing.T) {
	bin := os.Getenv("RCLONE_TEST_BIN")
	if bin == "" {
		t.Skip("set RCLONE_TEST_BIN")
	}
	rootParent := "/vol99/1000"
	if _, err := os.Stat(rootParent); err != nil {
		t.Skip("isolated NAS test root not mounted")
	}
	root, err := os.MkdirTemp(rootParent, "single-combined-job-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	src := filepath.Join(root, "input")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	dad := filepath.Join(src, "爸爸相册")
	mom := filepath.Join(src, "妈妈相册")
	file := filepath.Join(src, "video.mp4")
	for _, p := range []string{filepath.Join(dad, "a.jpg"), filepath.Join(mom, "nested", "b.jpg"), file} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("example-content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := NewManager(filepath.Join(root, "private", "copies.json"), bin)
	if err != nil {
		t.Fatal(err)
	}
	for i, verify := range []string{"size", "size", "content"} {
		id := "combined-" + string(rune('0'+i))
		job := &Job{ID: id, Sources: []string{dad, mom, file}, Target: target, Transfers: 8, Verify: verify, State: "queued", Created: time.Now(), Updated: time.Now()}
		m.mu.Lock()
		m.jobs = append(m.jobs, job)
		if err := m.saveLocked(); err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
		m.mu.Unlock()
		m.kick()
		result := awaitJob(t, m, id)
		if result.DoneFiles != 3 || result.DoneBytes != int64(3*len("example-content")) {
			t.Fatalf("incorrect counters: %+v", result)
		}
		for _, name := range []string{"爸爸相册/a.jpg", "妈妈相册/nested/b.jpg", "video.mp4"} {
			b, err := os.ReadFile(filepath.Join(target, name))
			if err != nil || string(b) != "example-content" {
				t.Fatalf("%s: %q %v", name, b, err)
			}
		}
		reloaded, err := NewManager(m.stateFile, bin)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, j := range reloaded.jobs {
			if j.ID == id && j.State == "completed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("task %s not persisted", id)
		}
	}
}
