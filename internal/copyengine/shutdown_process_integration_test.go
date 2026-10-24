package copyengine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Uses a fake long-running rclone to verify SIGTERM-like cancellation really
// waits for the OS child to disappear before the task becomes resumable.
func TestStopAndWaitKillsActiveRcloneBeforeSaving(t *testing.T) {
	rootParent := "/vol99/1000"
	if _, err := os.Stat(rootParent); err != nil {
		t.Skip("isolated test volume unavailable")
	}
	root, err := os.MkdirTemp(rootParent, "stop-protection-")
	if err != nil {
		t.Skipf("cannot create isolated test directory: %v", err)
	}
	defer os.RemoveAll(root)
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.jpg"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "child.pid")
	t.Setenv("STOP_PROTECTION_MARKER", marker)
	fake := filepath.Join(root, "rclone-fake.py")
	script := `#!/usr/bin/env python3
import os,time,signal,sys
marker=os.environ["STOP_PROTECTION_MARKER"]
def on_stop(sig,frame):
 with open(marker+".interrupted","w") as f:
  f.write("SIGINT")
 time.sleep(0.2)
 sys.exit(0)
signal.signal(signal.SIGINT,on_stop)
with open(marker,"w") as f:
 f.write(str(os.getpid()))
time.sleep(60)
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "private", "copies.json")
	m, err := NewManager(state, fake)
	if err != nil {
		t.Fatal(err)
	}
	job := &Job{ID: "long-active", Sources: []string{source}, Target: target, Transfers: 8, Verify: "size", State: "queued", Created: time.Now(), Updated: time.Now()}
	m.mu.Lock()
	m.jobs = append(m.jobs, job)
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.mu.Unlock()
	m.kick()
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(marker)
		if err == nil {
			pid, err = strconv.Atoi(string(b))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake rclone did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := m.StopAndWait(ctx); err != nil {
		t.Fatalf("safe stop failed: %v", err)
	}
	// rclone gets SIGINT first, allowing a clean close; cmd.Wait reaps it.
	if b, err := os.ReadFile(marker + ".interrupted"); err != nil || string(b) != "SIGINT" {
		t.Fatalf("child did not receive graceful SIGINT before exit: %q %v", b, err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("rclone pid %d survived safe shutdown", pid)
	}
	restored, err := NewManager(state, fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.jobs) != 1 || restored.jobs[0].State != "paused" {
		t.Fatalf("expected persisted paused task, got %#v", restored.jobs)
	}
}
