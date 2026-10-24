package copyengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEightIndependentFilesAreCopiedConcurrently(t *testing.T) {
	if os.Getenv("RUN_FNOS_PARALLEL_INTEGRATION") != "1" {
		t.Skip("only in isolated n2 test environment")
	}
	if _, err := os.Stat("/vol99/1000"); err != nil {
		t.Skipf("test shared root unavailable: %v", err)
	}
	root, err := os.MkdirTemp("/vol99/1000", "quickcopy-parallel-e2e-")
	if err != nil {
		t.Skipf("isolated storage unavailable: %v", err)
	}
	defer os.RemoveAll(root)
	src := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, p := range []string{src, target} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	var paths []string
	for i := 0; i < 8; i++ {
		path := filepath.Join(src, fmt.Sprintf("test-%d.mp4", i))
		if e := os.WriteFile(path, []byte("sample"), 0644); e != nil {
			t.Fatal(e)
		}
		paths = append(paths, path)
	}
	trace := filepath.Join(root, "trace.json")
	t.Setenv("QUICKCOPY_TEST_TRACE", trace)
	fake := filepath.Join(root, "fake-rclone")
	script := `#!/usr/bin/env python3
import fcntl,json,os,time,sys
from pathlib import Path
trace=Path(os.environ['QUICKCOPY_TEST_TRACE'])
with open(trace,'a+') as f:
 fcntl.flock(f,fcntl.LOCK_EX)
 f.seek(0)
 try: s=json.load(f)
 except: s={'active':0,'max':0,'calls':[]}
 s['active']+=1
 s['max']=max(s['max'],s['active'])
 s['calls'].append(sys.argv[1:])
 f.seek(0); f.truncate(); json.dump(s,f); f.flush()
 fcntl.flock(f,fcntl.LOCK_UN)
sys.stderr.write(json.dumps({'level':'notice','stats':{'bytes':3,'speed':1048576,'transferring':[{'name':Path(sys.argv[2]).name,'speedAvg':1048576}]}})+'\n')
sys.stderr.flush()
time.sleep(1.5)
with open(trace,'r+') as f:
 fcntl.flock(f,fcntl.LOCK_EX)
 s=json.load(f)
 s['active']-=1
 f.seek(0);f.truncate();json.dump(s,f);f.flush()
 fcntl.flock(f,fcntl.LOCK_UN)
`
	if e := os.WriteFile(fake, []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	m, err := NewManager(filepath.Join(root, "copies.json"), fake)
	if err != nil {
		t.Fatal(err)
	}
	job := &Job{ID: "8-sources", Sources: paths, Target: target, Transfers: 8, Verify: "size", State: "queued", Created: time.Now(), Updated: time.Now()}
	m.mu.Lock()
	m.jobs = append(m.jobs, job)
	_ = m.saveLocked()
	m.mu.Unlock()
	m.kick()
	seen := 0
	until := time.Now().Add(7 * time.Second)
	for time.Now().Before(until) {
		time.Sleep(45 * time.Millisecond)
		m.mu.Lock()
		j := m.lookup(job.ID)
		if j.ActiveTransfers > seen {
			seen = j.ActiveTransfers
		}
		state := j.State
		m.mu.Unlock()
		if state == "completed" || state == "error" {
			break
		}
	}
	m.mu.Lock()
	state, errText, done, items := job.State, job.Error, job.DoneFiles, append([]Entry(nil), job.Items...)
	m.mu.Unlock()
	if state != "completed" {
		t.Fatalf("concurrent task didn't finish: %s %s", state, errText)
	}
	if done != 8 {
		t.Fatalf("expected eight completed files got %d", done)
	}
	for _, it := range items {
		if !it.Completed {
			t.Fatalf("missing completed checkpoint for %s", it.Name)
		}
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Max   int        `json:"max"`
		Calls [][]string `json:"calls"`
	}
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if record.Max != 8 {
		t.Errorf("maximum simultaneous subprocesses=%d, want 8", record.Max)
	}
	if seen != 8 {
		t.Errorf("UI active-transfer metrics only observed %d, want 8", seen)
	}
	if len(record.Calls) != 8 {
		t.Fatalf("rclone processes=%d, want 8", len(record.Calls))
	}
	for _, args := range record.Calls {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--transfers=1") {
			t.Errorf("worker parallel budget violated: %s", joined)
		}
	}
	reloaded, err := NewManager(m.stateFile, fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.jobs) != 1 || reloaded.jobs[0].DoneFiles != 8 {
		t.Fatal("checkpoint persistence lost")
	}
}
