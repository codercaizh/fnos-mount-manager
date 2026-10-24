package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteSharesFromMounts(t *testing.T) {
	input := []MountInfo{
		{Point: "/vol02/1000-0-bbb", FSType: "cifs"},
		{Point: "/vol02/1000-0-aaa", FSType: "smb3"},
		{Point: "/vol02/1000-0-aaa", FSType: "smb3"},   // deduplicated
		{Point: "/vol02/1000-0-bbb/a", FSType: "cifs"}, // nested share folder
		{Point: "/vol02/1000-0-ccc", FSType: "ext4"},
		{Point: "/vol02/irrelevant", FSType: "cifs"},
		{Point: "/vol1/1000/share", FSType: "cifs"},
	}
	got := remoteSharesFromMounts(input)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != "/vol02/1000-0-aaa" || got[1].Path != "/vol02/1000-0-bbb" {
		t.Fatalf("wrong order/paths: %+v", got)
	}
	if got[0].Name != "1000-0-aaa" {
		t.Errorf("bad label %s", got[0].Name)
	}
}

func TestSMBRootSourceReady(t *testing.T) {
	root := "/vol02/1000-0-abcdef"
	ms := []MountInfo{{Point: root, FSType: "cifs", ID: "42"}}
	if !remotePath(root) || !sourceAllowed(root) {
		t.Fatal("SMB root should be allowed as source")
	}
	if !remoteReady(root, ms) {
		t.Fatal("mounted SMB root must be ready without subfolder")
	}
	if !remoteReady(root+"/Photos", ms) {
		t.Fatal("nested SMB source must remain supported")
	}
	if remoteReady("/vol02/1000-0-other", ms) {
		t.Fatal("unmounted SMB root should be offline")
	}
}

func TestLocalMkdirPathValidation(t *testing.T) {
	for _, in := range []struct{ parent, name, want string }{
		{"/vol1/1000/share", "Backup", "/vol1/1000/share/Backup"},
		{"/vol2/1000/share", "家人照片", "/vol2/1000/share/家人照片"},
	} {
		out, e := localMkdirPath(in.parent, in.name)
		if e != nil || out != in.want {
			t.Errorf("%+v -> %q, %v", in, out, e)
		}
	}
	for _, in := range []struct{ parent, name string }{
		{"/etc", "foo"},
		{"/vol02/1000-0-abc", "foo"},
		{"/vol1/1000", "foo"},
		{"/vol1/1000/share", ".."},
		{"/vol1/1000/share", "a/b"},
		{"/vol1/1000/share", "@app"},
		{"/vol1/1000/share", ""},
	} {
		if _, err := localMkdirPath(in.parent, in.name); err == nil {
			t.Errorf("dangerous folder accepted %+v", in)
		}
	}
}

func TestMkdirEndpointRejectsUnsupportedAndCrossSite(t *testing.T) {
	s := &Store{Records: []*Record{}}
	for _, raw := range []string{
		"{",
		`{"Parent":"/etc","Name":"test"}`,
		`{"Parent":"/vol02/1000-0-abc","Name":"test"}`,
		`{"Parent":"/vol1/1000/share","Name":"../evil"}`,
	} {
		rec := postJSON(s, "api/mkdir", raw)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("got %d body=%s for %s", rec.Code, rec.Body.String(), raw)
		}
	}
	req := httptest.NewRequest(http.MethodPost, base+"api/mkdir", strings.NewReader(`{"Parent":"/vol1/1000/share","Name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fnos-Mount-UI", "1")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	s.serve(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross site status %d", w.Code)
	}
}

func TestCreateLocalFolderOnlyWithinSafeParent(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "new folder")
	if err := createLocalFolder(parent, target, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	fi, err := os.Lstat(target)
	if err != nil || !fi.IsDir() {
		t.Fatalf("created folder invalid: %v", err)
	}
	if err = createLocalFolder(parent, target, nil); !errors.Is(err, os.ErrExist) {
		t.Errorf("expected ErrExist for duplicate, got %v", err)
	}
	another := filepath.Join(parent, "another")
	record := []*Record{{Target: parent}}
	if err = createLocalFolder(parent, another, record); err == nil {
		t.Fatal("create under a managed alias must be rejected")
	}
	if _, err = os.Lstat(another); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected directory created under alias: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	if err = createLocalFolder(link, filepath.Join(link, "bad"), nil); err == nil {
		t.Fatal("create under symlink should fail")
	}
}
