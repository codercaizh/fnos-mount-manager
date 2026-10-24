package copyengine

import (
	"encoding/json"
	"testing"
)

func TestCompletedAndSkippedFilesAppearInRealtime(t *testing.T) {
	j := &Job{ID: "live", TotalFiles: 10, DoneFiles: 0}
	m := &Manager{jobs: []*Job{j}}
	parts := []Entry{{Name: "第一批", Files: 5}, {Name: "第二批", Files: 5}}
	progress := newTransferProgress(parts)
	progress.record(m, j.ID, 0, &rcStats{Checks: 1, Transfers: 2, Bytes: 3000})
	if j.CurrentDoneFiles != 3 || j.DoneFiles != 0 {
		t.Fatalf("expected 3 of 10 while running, got current=%d durable=%d", j.CurrentDoneFiles, j.DoneFiles)
	}
	progress.record(m, j.ID, 1, &rcStats{Checks: 2, Transfers: 1, Bytes: 2000})
	if got := j.DoneFiles + j.CurrentDoneFiles; got != 6 {
		t.Fatalf("want 6/10 during 2 simultaneous sources, got %d", got)
	}
	// Once a complete source has been durably checkpointed, remove its
	// provisional live statistics so checks / transfers are never double-counted.
	j.DoneFiles = 5
	progress.record(m, j.ID, 0, nil)
	if got := j.DoneFiles + j.CurrentDoneFiles; got != 8 {
		t.Fatalf("5 durable + 3 still live should be 8, got %d", got)
	}
	// Repeated stat samples are cumulative, not increments.
	progress.record(m, j.ID, 1, &rcStats{Checks: 2, Transfers: 1, Bytes: 2000})
	if got := j.DoneFiles + j.CurrentDoneFiles; got != 8 {
		t.Fatalf("identical repeated stats were counted again: %d", got)
	}
	progress.record(m, j.ID, 1, &rcStats{Checks: 2, Transfers: 100, Bytes: 99999})
	if got := j.DoneFiles + j.CurrentDoneFiles; got != 10 {
		t.Fatalf("must not count beyond known source files: %d", got)
	}
	progress.record(m, j.ID, 1, nil)
	j.DoneFiles = 10
	if j.CurrentDoneFiles != 0 || j.DoneFiles != 10 {
		t.Fatalf("task completion counters inconsistent")
	}
}

func TestRcloneJSONStatsContainsFinishedCounts(t *testing.T) {
	// This matches the concrete rclone v1.75.2 JSON log schema.
	sample := []byte(`{"level":"notice","stats":{"bytes":1114112,"checks":7,"totalChecks":7,"transfers":3,"totalTransfers":3,"transferring":[{"name":"file-01.mp4","speedAvg":1}]}}`)
	var record logLine
	if err := json.Unmarshal(sample, &record); err != nil {
		t.Fatal(err)
	}
	if record.Stats == nil || record.Stats.Checks != 7 || record.Stats.Transfers != 3 {
		t.Fatalf("parsed stats: %+v", record)
	}
}
