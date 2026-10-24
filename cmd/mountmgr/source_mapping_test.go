package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTwoAliasesSameParentMustHaveDifferentSources(t *testing.T) {
	dad := &Record{ID: "dad", Source: "/vol02/1000-0-dad", Target: "/vol1/1000/share/网盘备份/爸爸相册", Mode: "ro", Enabled: true}
	mom := &Record{ID: "mom", Source: "/vol02/1000-0-mom", Target: "/vol1/1000/share/网盘备份/妈妈相册", Mode: "ro", Enabled: true}
	records := []*Record{dad, mom}
	if duplicateSource(records, "/vol02/1000-0-mom", mom) != nil {
		t.Fatal("own source incorrectly flagged duplicate")
	}
	if duplicateSource(records, "/vol02/1000-0-dad", nil) != dad {
		t.Fatal("duplicate dad root was not blocked")
	}
	if duplicateSource(records, "/vol02/1000-0-mom", nil) != mom {
		t.Fatal("duplicate mom root was not blocked")
	}
	if duplicateSource(records, "/vol02/1000-0-new", nil) != nil {
		t.Fatal("unique root marked duplicate")
	}
	if dad.Target == mom.Target || dad.Source == mom.Source {
		t.Fatal("two aliases should keep distinct source and target")
	}
	if overlap(dad.Target, mom.Target) {
		t.Fatal("different siblings should not conflict")
	}
}

func TestDisabledSourceChangePreservesTargetAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "mounts.json")
	dad := &Record{ID: "dad", Source: "/vol02/1000-0-old", Target: "/vol1/1000/share/网盘备份/爸爸相册", MountID: "old-bind-mount-id", Mode: "ro", Enabled: false, Status: "disabled"}
	mom := &Record{ID: "mom", Source: "/vol02/1000-0-mom", Target: "/vol1/1000/share/网盘备份/妈妈相册", Mode: "ro", Enabled: true}
	store := &Store{file: path, Records: []*Record{dad, mom}}
	if err := saveDisabledSource(store, dad, "/vol02/1000-0-dad"); err != nil {
		t.Fatal(err)
	}
	if dad.Target != "/vol1/1000/share/网盘备份/爸爸相册" || dad.Source != "/vol02/1000-0-dad" {
		t.Fatalf("mapping was incorrectly modified: %+v", dad)
	}
	if dad.MountID != "" || dad.Enabled || dad.Status != "disabled" {
		t.Fatalf("unsafe state after edit: %+v", dad)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Records []*Record `json:"records"`
	}
	if err := json.Unmarshal(saved, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Records[0].Source != "/vol02/1000-0-dad" || parsed.Records[1].Source != "/vol02/1000-0-mom" {
		t.Fatalf("persisted sources crossed: %+v", parsed.Records)
	}
	// Persist failure must leave the live record unchanged.
	store.file = filepath.Join(path, "invalid-file-under-a-json-document")
	err = saveDisabledSource(store, dad, "/vol02/1000-0-other")
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if dad.Source != "/vol02/1000-0-dad" || dad.MountID != "" || dad.Target != "/vol1/1000/share/网盘备份/爸爸相册" {
		t.Fatalf("rollback failed: %+v", dad)
	}
}

func TestChangeSourceEndpointRejectsUnsafeOperations(t *testing.T) {
	records := []*Record{{ID: "dad", Source: "/vol02/1000-0-dad", Target: "/vol1/1000/share/dad", Mode: "ro", Enabled: false}}
	s := &Store{file: filepath.Join(t.TempDir(), "state.json"), Records: records}
	for _, tc := range []struct {
		description, request string
		code                 int
	}{
		{"system path", `{"ID":"dad","Source":"/etc"}`, 400},
		{"invalid path", `{"ID":"dad","Source":"../relative"}`, 400},
		{"missing remote mount", `{"ID":"dad","Source":"/vol02/1000-0-absent"}`, 400},
	} {
		r := postJSON(s, "api/change-source", tc.request)
		if r.Code != tc.code {
			t.Errorf("%s: code=%d want=%d body=%s", tc.description, r.Code, tc.code, r.Body.String())
		}
	}
	if !strings.Contains(records[0].Source, "0-dad") {
		t.Fatal("invalid request modified mapping")
	}
}

func TestMappingListDisplaysRealDistinctSMBSources(t *testing.T) {
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-dad", Name: "爸爸相册"},
		{Path: "/vol02/1000-0-mom", Name: "妈妈相册"},
	}
	for _, tc := range []struct{ source, want string }{
		{"/vol02/1000-0-dad", "爸爸相册"},
		{"/vol02/1000-0-mom", "妈妈相册"},
		{"/vol02/1000-0-dad/2026", "爸爸相册/2026"},
		{"/vol02/1000-0-unknown", ""},
	} {
		if got := sourceLabelForRecord(tc.source, shares); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.source, got, tc.want)
		}
	}
}

func TestBindMountReceivesIndependentSourceForEachAlbum(t *testing.T) {
	dad := &Record{Source: "/vol02/1000-0-dad", Target: "/vol1/1000/share/网盘备份/爸爸相册", Mode: "rw"}
	mom := &Record{Source: "/vol02/1000-0-mom", Target: "/vol1/1000/share/网盘备份/妈妈相册", Mode: "rw"}
	var got [][2]string
	withMockMount(t, func(source, target, fs string, flags uintptr, data string) error {
		got = append(got, [2]string{source, target})
		return nil
	}, func(string, int) error { return nil })
	if err := mountBind(dad.Source, dad.Target, dad.Mode); err != nil {
		t.Fatal(err)
	}
	if err := mountBind(mom.Source, mom.Target, mom.Mode); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two distinct bind calls: %+v", got)
	}
	if got[0] != [2]string{dad.Source, dad.Target} || got[1] != [2]string{mom.Source, mom.Target} {
		t.Fatalf("bind calls point to incorrect SMB roots: %+v", got)
	}
}

func TestSourceEditRequiresDisabledAndUnmountedAlias(t *testing.T) {
	record := &Record{ID: "dad", Source: "/vol02/1000-0-dad", Target: "/vol1/1000/share/爸爸相册", Enabled: true}
	if err := validateSourceEditState(record, nil); err == nil {
		t.Fatal("editing active mapping should be blocked")
	}
	record.Enabled = false
	ms := []MountInfo{{Point: record.Target, ID: "42", FSType: "cifs"}}
	if err := validateSourceEditState(record, ms); err == nil {
		t.Fatal("editing still-mounted alias should be blocked")
	}
	if err := validateSourceEditState(record, nil); err != nil {
		t.Fatalf("disabled and unmounted should allow edit: %v", err)
	}
}
