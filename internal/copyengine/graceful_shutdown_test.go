package copyengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStopAndWaitPersistsPausedStateBeforeExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "copies.json")
	running := &Job{ID: "running", State: "running", Created: time.Now(), Updated: time.Now(), TotalFiles: 10}
	queued := &Job{ID: "queued", State: "queued", Created: time.Now(), Updated: time.Now()}
	cancelled := make(chan struct{})
	manager := &Manager{
		stateFile: path, jobs: []*Job{running, queued},
		active: running.ID, activeDone: make(chan struct{}),
		cancel: func() { close(cancelled) },
	}
	// Model the active rclone worker: cancellation may take time, and the
	// shutdown must not finish before the worker's persisted final state.
	go func() {
		<-cancelled
		time.Sleep(110 * time.Millisecond)
		manager.end(running.ID, "paused", nil)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := manager.StopAndWait(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("shutdown returned before the active worker was done: %v", d)
	}
	if !manager.stopping || manager.active != "" || manager.activeDone != nil {
		t.Fatal("active worker was not drained")
	}
	for _, job := range manager.jobs {
		if job.State != "paused" {
			t.Fatalf("%s was not paused: %s", job.ID, job.State)
		}
	}
	manager.kick()
	if manager.active != "" {
		t.Fatal("shutdown restarted queued work")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Jobs []*Job `json:"jobs"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Jobs) != 2 || state.Jobs[0].State != "paused" || state.Jobs[1].State != "paused" {
		t.Fatalf("incorrect persisted tasks: %+v", state.Jobs)
	}
	reloaded, err := NewManager(path, "rclone")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.jobs) != 2 || reloaded.jobs[0].State != "paused" || reloaded.jobs[1].State != "paused" {
		t.Fatal("tasks were not resumable after restart")
	}
}

func TestStopAndWaitTimeoutDoesNotClaimSuccess(t *testing.T) {
	manager := &Manager{
		stateFile: filepath.Join(t.TempDir(), "copies.json"),
		active:    "copy", activeDone: make(chan struct{}),
		jobs:   []*Job{{ID: "copy", State: "running"}},
		cancel: func() {},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()
	if err := manager.StopAndWait(ctx); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("wanted explicit timeout, got %v", err)
	}
	if manager.active == "" {
		t.Fatal("worker disappeared without exiting")
	}
	manager.end("copy", "paused", nil)
	if err := manager.StopAndWait(context.Background()); err != nil {
		t.Fatalf("retry after worker exit: %v", err)
	}
	if manager.jobs[0].State != "paused" {
		t.Fatal("running state was not checkpointed")
	}
}

func TestShuttingDownRejectsResume(t *testing.T) {
	manager := &Manager{
		stateFile: filepath.Join(t.TempDir(), "copies.json"),
		stopping:  true, jobs: []*Job{{ID: "paused", State: "paused"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/resume", strings.NewReader(`{"id":"paused"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fnos-Quickcopy-UI", "1")
	resp := httptest.NewRecorder()
	manager.Serve(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during shutdown; got %d %s", resp.Code, resp.Body.String())
	}
	if manager.jobs[0].State != "paused" {
		t.Fatal("resume occurred during shutdown")
	}
}

func TestShutdownWithoutActiveWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copies.json")
	manager := &Manager{stateFile: path, jobs: []*Job{{ID: "q", State: "queued"}, {ID: "done", State: "completed"}}}
	if err := manager.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.jobs[0].State != "paused" || manager.jobs[1].State != "completed" {
		t.Fatal("only pending tasks should change to paused")
	}
	manager.kick()
	if manager.active != "" {
		t.Fatal("kick started work during shutdown")
	}
}

func TestShutdownRejectsUnwritableStatePath(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parent, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{stateFile: filepath.Join(parent, "copies.json"), jobs: []*Job{{ID: "pending", State: "queued"}}}
	if err := manager.StopAndWait(context.Background()); err == nil {
		t.Fatal("shutdown must fail when no checkpoint can be stored")
	}
	if manager.active != "" {
		t.Fatal("unexpected worker")
	}
}
