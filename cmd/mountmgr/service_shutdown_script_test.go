package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLifecycleStopWaitsForCheckpoint(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "normal"
		if deleted {
			name = "upgraded-executable-deleted"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			app := filepath.Join(root, "app")
			priv := filepath.Join(root, "var")
			if err := os.MkdirAll(filepath.Join(app, "server"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(priv, 0700); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(app, "server", "mountmgr")
			bash, err := exec.LookPath("bash")
			if err != nil {
				t.Fatal(err)
			}
			// The actual executable filename is mountmgr; for the test it
			// executes Bash as a tiny SIGTERM-aware mock daemon.
			data, err := os.ReadFile(bash)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, data, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "saved.state")
			ready := filepath.Join(root, "ready")
			mock := filepath.Join(root, "mock-service.sh")
			script := `#!/usr/bin/env bash
trap 'sleep 0.2; printf "paused" > "$STOP_MARKER"; exit 0' TERM
printf "ready" > "$READY_MARKER"
while true; do sleep 0.1; done
`
			if err := os.WriteFile(mock, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			child := exec.Command(bin, mock)
			child.Env = append(os.Environ(), "STOP_MARKER="+marker, "READY_MARKER="+ready)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, err := os.Stat(ready); err != nil {
				t.Fatalf("mock daemon not ready: %v", err)
			}
			if deleted {
				// This is what happens when fnOS replaces the old executable:
				// procfs exposes a path suffixed with " (deleted)".
				if err := os.Remove(bin); err != nil {
					t.Fatal(err)
				}
				link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", child.Process.Pid))
				if err != nil {
					t.Fatal(err)
				}
				if len(link) < 10 || link[len(link)-10:] != " (deleted)" {
					t.Fatalf("expected deleted executable suffix, got %q", link)
				}
			}
			if err := os.WriteFile(filepath.Join(priv, "service.pid"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join("..", "..", "packaging", "fpk", "cmd", "main")
			stop := exec.Command("bash", wrapper, "stop")
			stop.Env = append(os.Environ(), "TRIM_APPDEST="+app, "TRIM_PKGVAR="+priv)
			start := time.Now()
			output, err := stop.CombinedOutput()
			if err != nil {
				t.Fatalf("stop failed: %v %s", err, output)
			}
			if elapsed := time.Since(start); elapsed < 180*time.Millisecond {
				t.Fatalf("stop did not wait for the service checkpoint: %v", elapsed)
			}
			if err := child.Wait(); err != nil {
				t.Fatal(err)
			}
			waited = true
			raw, err := os.ReadFile(marker)
			if err != nil || string(raw) != "paused" {
				t.Fatalf("checkpoint missing before stop returned: %q %v", raw, err)
			}
			if _, err := os.Stat(filepath.Join(priv, "service.pid")); !os.IsNotExist(err) {
				t.Fatalf("PID file not cleaned after safe exit: %v", err)
			}
		})
	}
}
