package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMissingAppOwnedAliasAutoRecreated(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	parent := filepath.Join(root, "网盘备份")
	target := filepath.Join(parent, "爸爸相册")
	for _, p := range []string{src, parent} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	calls := 0
	withMockMount(t, func(a, b, fs string, flags uintptr, data string) error {
		calls++
		if a != src || b != target || flags != syscall.MS_BIND {
			t.Fatalf("unexpected bind %s -> %s (%d)", a, b, flags)
		}
		st, err := os.Stat(target)
		if err != nil || !st.IsDir() {
			t.Fatalf("missing target on bind: %v", err)
		}
		return nil
	}, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
	r := &Record{Source: src, Target: target, Mode: "rw", Enabled: true, Owned: true}
	reconcile(&Store{}, r, nil)
	if r.Status != "mounted" || calls != 1 {
		t.Fatalf("did not recover: %+v calls=%d", r, calls)
	}
	st, e := os.Stat(target)
	if e != nil || !st.IsDir() {
		t.Fatal("missing recreated directory", e)
	}
	files, e := os.ReadDir(target)
	if e != nil || len(files) != 0 {
		t.Fatal("recovered directory should be empty until mounted")
	}
}

func TestMissingAliasWithoutOwnershipNotRecreated(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	parent := filepath.Join(root, "share")
	target := filepath.Join(parent, "custom")
	for _, p := range []string{src, parent} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	withMockMount(t, func(string, string, string, uintptr, string) error { t.Fatal("must not mount"); return nil }, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
	r := &Record{Source: src, Target: target, Mode: "rw", Enabled: true, Owned: false}
	reconcile(&Store{}, r, nil)
	if r.Status != "error" || !strings.Contains(r.Error, "不是本应用创建") {
		t.Fatalf("expected safe refusal: %+v", r)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected creation: %v", err)
	}
}

func TestMissingAliasDoesNotCreateParentsOrFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if e := os.Mkdir(src, 0755); e != nil {
		t.Fatal(e)
	}
	withMockMount(t, func(string, string, string, uintptr, string) error { t.Fatal("must not mount"); return nil }, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
	t.Run("missing parent", func(t *testing.T) {
		parent := filepath.Join(root, "missing", "backup")
		r := &Record{Source: src, Target: filepath.Join(parent, "妈妈相册"), Mode: "rw", Enabled: true, Owned: true}
		reconcile(&Store{}, r, nil)
		if r.Status != "error" || !strings.Contains(r.Error, "根路径不可用") {
			t.Fatalf("unexpected %+v", r)
		}
		if _, e := os.Lstat(parent); !os.IsNotExist(e) {
			t.Fatal("parent must not be auto-created")
		}
	})
	t.Run("symlink parent", func(t *testing.T) {
		real := filepath.Join(root, "real")
		alias := filepath.Join(root, "link")
		if e := os.Mkdir(real, 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(real, alias); e != nil {
			t.Fatal(e)
		}
		r := &Record{Source: src, Target: filepath.Join(alias, "妈妈相册"), Mode: "rw", Enabled: true, Owned: true}
		reconcile(&Store{}, r, nil)
		if r.Status != "error" || !strings.Contains(r.Error, "符号链接") {
			t.Fatalf("symlink not rejected %+v", r)
		}
		if _, e := os.Lstat(filepath.Join(real, "妈妈相册")); !os.IsNotExist(e) {
			t.Fatal("must not write through symlink")
		}
	})
	t.Run("dangling target symlink", func(t *testing.T) {
		link := filepath.Join(root, "dangling")
		if e := os.Symlink(filepath.Join(root, "nonexistent"), link); e != nil {
			t.Fatal(e)
		}
		r := &Record{Source: src, Target: link, Mode: "rw", Enabled: true, Owned: true}
		reconcile(&Store{}, r, nil)
		if r.Status != "error" || !strings.Contains(r.Error, "符号链接") {
			t.Fatalf("target symlink not rejected %+v", r)
		}
	})
}

func TestExistingAliasContentNeverCovered(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	target := filepath.Join(root, "mapped")
	if e := os.Mkdir(src, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(target, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(target, "precious.jpg"), []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	withMockMount(t, func(string, string, string, uintptr, string) error {
		t.Fatal("must not hide existing files")
		return nil
	}, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
	r := &Record{Source: src, Target: target, Mode: "rw", Enabled: true, Owned: true}
	reconcile(&Store{}, r, nil)
	if r.Status != "conflict" {
		t.Fatalf("must refuse nonempty path: %+v", r)
	}
	b, e := os.ReadFile(filepath.Join(target, "precious.jpg"))
	if e != nil || string(b) != "safe" {
		t.Fatal("data was changed")
	}
}

// Nested aliases should survive loss of several parent directories, not just
// loss of the final bind target.
func TestOwnedAliasRebuildsEntireMissingTree(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "user-root")
	if err := os.Mkdir(trusted, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(trusted, "网盘备份", "父母", "爸爸相册")
	record := &Record{Target: target, Owned: true}
	if err := ensureAliasTargetUnderRoot(record, trusted); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(trusted, "网盘备份"),
		filepath.Join(trusted, "网盘备份", "父母"),
		target,
	} {
		fi, err := os.Lstat(want)
		if err != nil || !fi.IsDir() {
			t.Fatalf("missing directory %s: %v", want, err)
		}
	}
	// Running recovery multiple times must be idempotent.
	if err := ensureAliasTargetUnderRoot(record, trusted); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("intact"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAliasTargetUnderRoot(record, trusted); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(target, "existing.txt"))
	if err != nil || string(content) != "intact" {
		t.Fatal("existing data was changed")
	}
}

func TestDeepRestoreRefusesUnexpectedExistingComponents(t *testing.T) {
	root := t.TempDir()
	trusted := filepath.Join(root, "user-root")
	if err := os.Mkdir(trusted, 0755); err != nil {
		t.Fatal(err)
	}
	t.Run("file in parent", func(t *testing.T) {
		file := filepath.Join(trusted, "ordinary-file")
		if err := os.WriteFile(file, []byte("precious"), 0600); err != nil {
			t.Fatal(err)
		}
		record := &Record{Target: filepath.Join(file, "sub", "target"), Owned: true}
		err := ensureAliasTargetUnderRoot(record, trusted)
		if err == nil || !strings.Contains(err.Error(), "文件或符号链接") {
			t.Fatalf("should refuse, got %v", err)
		}
		b, err := os.ReadFile(file)
		if err != nil || string(b) != "precious" {
			t.Fatal("modified user file")
		}
	})
	t.Run("symlink to another folder", func(t *testing.T) {
		real := filepath.Join(root, "real")
		if err := os.Mkdir(real, 0755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(trusted, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		record := &Record{Target: filepath.Join(link, "sub", "target"), Owned: true}
		err := ensureAliasTargetUnderRoot(record, trusted)
		if err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatalf("must refuse symlink %v", err)
		}
		if _, err := os.Lstat(filepath.Join(real, "sub")); !os.IsNotExist(err) {
			t.Fatal("wrote through symlink")
		}
	})
	t.Run("not owned", func(t *testing.T) {
		target := filepath.Join(trusted, "unowned", "nested", "target")
		err := ensureAliasTargetUnderRoot(&Record{Target: target, Owned: false}, trusted)
		if err == nil || !strings.Contains(err.Error(), "不是本应用创建") {
			t.Fatalf("should refuse: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(trusted, "unowned")); !os.IsNotExist(err) {
			t.Fatal("created unowned path")
		}
	})
	t.Run("missing trusted root", func(t *testing.T) {
		trustedMissing := filepath.Join(root, "offline-storage")
		target := filepath.Join(trustedMissing, "backup", "mapped")
		err := ensureAliasTargetUnderRoot(&Record{Target: target, Owned: true}, trustedMissing)
		if err == nil || !strings.Contains(err.Error(), "根路径不可用") {
			t.Fatalf("unsafe rebuild accepted: %v", err)
		}
		if _, err := os.Lstat(trustedMissing); !os.IsNotExist(err) {
			t.Fatal("created missing user-storage root")
		}
	})
	t.Run("escape", func(t *testing.T) {
		target := filepath.Join(root, "outside", "mapped")
		err := ensureAliasTargetUnderRoot(&Record{Target: target, Owned: true}, trusted)
		if err == nil || !strings.Contains(err.Error(), "目录范围") {
			t.Fatalf("accepted outside target: %v", err)
		}
	})
}

func TestRecoveryUsesVolumeUserRoot(t *testing.T) {
	cases := map[string]string{
		"/vol1/1000/网盘备份/爸爸相册":        "/vol1/1000",
		"/vol2/1001/网盘备份/家庭/子目录/妈妈相册": "/vol2/1001",
	}
	for path, want := range cases {
		if got := aliasRecoveryRoot(path); got != want {
			t.Fatalf("root for %s = %s, want %s", path, got, want)
		}
	}
}
