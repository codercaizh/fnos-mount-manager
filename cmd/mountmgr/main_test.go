package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPolicy(t *testing.T) {
	good := []string{"/vol02/1000-0-cef8fa37", "/vol02/1000-0-cef8fa37/folder", "/vol2/1000/share/爸爸相册"}
	for _, p := range good {
		if !sourceAllowed(p) {
			t.Errorf("unexpectedly denied %s", p)
		}
	}
	bad := []string{"/etc", "/tmp", "/vol2/@appcenter", "/vol02", "/vol2/1000/@appdata/private", "/vol2/1000"}
	for _, p := range bad {
		if sourceAllowed(p) {
			t.Errorf("source allowed %s", p)
		}
	}
	for _, p := range []string{"/vol02/1000-0-cifs", "/vol2/@appcenter", "/vol2/1000"} {
		if localPath(p, true) {
			t.Errorf("parent allowed %s", p)
		}
	}
	if !localPath("/vol2/1000/share/相册", true) {
		t.Fatal("should allow share parent")
	}
}
func TestName(t *testing.T) {
	for _, v := range []string{"", "..", " ", "foo/bar", "abc\\def", " trailing ", "@private"} {
		if strictName(v) == nil {
			t.Errorf("accepted dangerous name %q", v)
		}
	}
	if strictName("爸爸相册") != nil {
		t.Fatal("Chinese name rejected")
	}
}
func TestMountParsing(t *testing.T) {
	s := "42 35 0:31 / /vol02/1000-0-abc rw,relatime - cifs //server/path rw,vers=3\n42 35 0:33 / /vol2/1000/share/My\\040Photos ro,relatime - cifs //server/path ro\n"
	ms := parseMounts(s)
	if len(ms) != 2 || ms[1].Point != "/vol2/1000/share/My Photos" {
		t.Fatalf("%+v", ms)
	}
	if !remoteReady("/vol02/1000-0-abc/a", ms) {
		t.Fatal("mounted source must be ready")
	}
	if remoteReady("/vol02/1000-0-def", ms) {
		t.Fatal("missing mount must be offline")
	}
}
func TestConfigAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	s := &Store{file: filepath.Join(dir, "private", "mounts.json"), Records: []*Record{{ID: "a", Source: "/vol02/1000-0-test", Target: "/vol2/1000/share/test", Mode: "ro", Enabled: true}}}
	if err := persisted(s); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(s.file)
	if err != nil || len(loaded.Records) != 1 {
		t.Fatal(err, loaded)
	}
	if fi, err := os.Stat(s.file); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
	if err := os.WriteFile(s.file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(s.file); err == nil {
		t.Fatal("corrupt config silently accepted")
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	if err := checkNoSymlink(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if !overlap("/vol2/1000/share/a", "/vol2/1000/share/a/b") {
		t.Fatal("overlap missed")
	}
	if overlap("/vol2/1000/share/a", "/vol2/1000/share/ab") {
		t.Fatal("false overlap")
	}
}
