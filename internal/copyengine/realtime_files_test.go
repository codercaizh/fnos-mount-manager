package copyengine

import (
	"encoding/json"
	"testing"
)

func TestCombinedFinishedCountClamped(t *testing.T) {
	cases := []struct {
		name          string
		stats         rcStats
		pending, want int64
	}{
		{"already synced", rcStats{Checks: 3, Transfers: 2}, 10, 5},
		{"capped", rcStats{Checks: 12, Transfers: 8}, 8, 8},
		{"empty", rcStats{}, 8, 0},
		{"negative", rcStats{Checks: -1}, 8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completedFileCount(tc.stats, tc.pending); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRcloneJSONStatsContainsFinishedCounts(t *testing.T) {
	sample := []byte(`{"level":"notice","stats":{"bytes":1114112,"checks":7,"transfers":3,"transferring":[{"name":"file-01.mp4","speedAvg":1}]}}`)
	var record logLine
	if err := json.Unmarshal(sample, &record); err != nil {
		t.Fatal(err)
	}
	if record.Stats == nil || completedFileCount(*record.Stats, 10) != 10 || len(record.Stats.Transferring) != 1 {
		t.Fatalf("parsed stats: %+v", record)
	}
}
