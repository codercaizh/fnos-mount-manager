package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func withMockMount(t *testing.T, mount func(string, string, string, uintptr, string) error, unmount func(string, int) error) {
	t.Helper()
	previousMount, previousUnmount := mountCall, unmountCall
	mountCall = mount
	unmountCall = unmount
	t.Cleanup(func() { mountCall = previousMount; unmountCall = previousUnmount })
}

func TestMountBindModesAndRollback(t *testing.T) {
	t.Run("read-write single bind", func(t *testing.T) {
		var flags []uintptr
		withMockMount(t, func(src, dst, fs string, flag uintptr, data string) error {
			if src != "/source" || dst != "/target" {
				t.Errorf("wrong bind args: %s -> %s", src, dst)
			}
			flags = append(flags, flag)
			return nil
		}, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
		if err := mountBind("/source", "/target", "rw"); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(flags, []uintptr{syscall.MS_BIND}) {
			t.Fatalf("mount flags=%v", flags)
		}
	})
	t.Run("readonly must remount", func(t *testing.T) {
		var flags []uintptr
		withMockMount(t, func(_, _, _ string, flag uintptr, _ string) error { flags = append(flags, flag); return nil }, func(string, int) error { t.Fatal("unexpected unmount"); return nil })
		if err := mountBind("/source", "/target", "ro"); err != nil {
			t.Fatal(err)
		}
		want := []uintptr{syscall.MS_BIND, syscall.MS_BIND | syscall.MS_REMOUNT | syscall.MS_RDONLY}
		if !reflect.DeepEqual(flags, want) {
			t.Fatalf("mount flags=%v want=%v", flags, want)
		}
	})
	t.Run("readonly remount failure must rollback", func(t *testing.T) {
		var calls []string
		withMockMount(t, func(_, _, _ string, flags uintptr, _ string) error {
			if flags&syscall.MS_REMOUNT != 0 {
				calls = append(calls, "remount")
				return syscall.EPERM
			}
			calls = append(calls, "bind")
			return nil
		}, func(p string, flags int) error {
			calls = append(calls, "rollback")
			if p != "/target" || flags != 0 {
				t.Errorf("unexpected rollback args")
			}
			return nil
		})
		if err := mountBind("/source", "/target", "ro"); !errors.Is(err, syscall.EPERM) {
			t.Fatalf("err=%v", err)
		}
		if !reflect.DeepEqual(calls, []string{"bind", "remount", "rollback"}) {
			t.Fatalf("calls=%v", calls)
		}
	})
	t.Run("initial bind error never remounts", func(t *testing.T) {
		count := 0
		withMockMount(t, func(_, _, _ string, _ uintptr, _ string) error { count++; return syscall.EINVAL },
			func(string, int) error { t.Fatal("unexpected unmount"); return nil })
		if err := mountBind("/source", "/target", "ro"); !errors.Is(err, syscall.EINVAL) || count != 1 {
			t.Fatalf("err=%v count=%d", err, count)
		}
	})
}

func TestReconcileScenarios(t *testing.T) {
	t.Run("create valid mount", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		dst := filepath.Join(dir, "target")
		for _, p := range []string{src, dst} {
			if e := os.Mkdir(p, 0700); e != nil {
				t.Fatal(e)
			}
		}
		n := 0
		withMockMount(t, func(a, b, c string, f uintptr, data string) error {
			n++
			if a != src || b != dst || f != syscall.MS_BIND {
				t.Errorf("args %s %s %#x", a, b, f)
			}
			return nil
		},
			func(string, int) error { t.Fatal("unexpected unmount"); return nil })
		r := &Record{Source: src, Target: dst, Mode: "rw", Enabled: true}
		if !reconcile(&Store{}, r, nil) || r.Status != "mounted" || n != 1 {
			t.Fatalf("result=%+v count=%d", r, n)
		}
	})
	t.Run("occupied nonempty destination", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		dst := filepath.Join(dir, "target")
		for _, p := range []string{src, dst} {
			if e := os.Mkdir(p, 0700); e != nil {
				t.Fatal(e)
			}
		}
		if e := os.WriteFile(filepath.Join(dst, "important"), []byte("keep"), 0600); e != nil {
			t.Fatal(e)
		}
		withMockMount(t, func(string, string, string, uintptr, string) error { t.Fatal("unsafe bind"); return nil }, func(string, int) error { return nil })
		r := &Record{Source: src, Target: dst, Mode: "rw", Enabled: true}
		reconcile(&Store{}, r, nil)
		if r.Status != "conflict" || !strings.Contains(r.Error, "非空") {
			t.Fatalf("result=%+v", r)
		}
		if _, e := os.ReadFile(filepath.Join(dst, "important")); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("foreign mount cannot be adopted", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		dst := filepath.Join(dir, "target")
		for _, p := range []string{src, dst} {
			if e := os.Mkdir(p, 0700); e != nil {
				t.Fatal(e)
			}
		}
		withMockMount(t, func(string, string, string, uintptr, string) error { t.Fatal("unsafe bind"); return nil },
			func(string, int) error { t.Fatal("unsafe unmount"); return nil })
		r := &Record{Source: src, Target: dst, Mode: "rw", Enabled: true, MountID: "owned-id"}
		reconcile(&Store{}, r, []MountInfo{{Point: dst, ID: "unrelated-id"}})
		if r.Status != "conflict" {
			t.Fatalf("result=%+v", r)
		}
	})
	t.Run("offline remote unmount only owned id", func(t *testing.T) {
		n := 0
		withMockMount(t, func(string, string, string, uintptr, string) error { t.Fatal("unexpected bind"); return nil },
			func(p string, flags int) error {
				n++
				if p != "/vol1/1000/share/test" {
					t.Errorf("wrong target %s", p)
				}
				return nil
			})
		r := &Record{Source: "/vol02/1000-0-dead", Target: "/vol1/1000/share/test", Mode: "ro", Enabled: true, MountID: "1234"}
		reconcile(&Store{}, r, []MountInfo{{Point: r.Target, ID: "other"}})
		if n != 0 || r.Status != "source_offline" {
			t.Fatalf("foreign alias changed %+v, n=%d", r, n)
		}
		reconcile(&Store{}, r, []MountInfo{{Point: r.Target, ID: "1234"}})
		if n != 1 || r.Status != "source_offline" {
			t.Fatalf("owned alias not removed %+v, n=%d", r, n)
		}
	})
	t.Run("disabled owned mount must release", func(t *testing.T) {
		n := 0
		withMockMount(t, func(string, string, string, uintptr, string) error { return nil },
			func(string, int) error { n++; return nil })
		r := &Record{Source: "/vol02/1000-0-offline", Target: "/vol1/1000/share/test", Mode: "ro", Enabled: false, MountID: "abc"}
		reconcile(&Store{}, r, []MountInfo{{Point: r.Target, ID: "abc"}})
		if r.Status != "disabled" || n != 1 {
			t.Fatalf("result=%+v count=%d", r, n)
		}
	})
	t.Run("offline unmount failure reported", func(t *testing.T) {
		withMockMount(t, func(string, string, string, uintptr, string) error { return nil }, func(string, int) error { return syscall.EBUSY })
		r := &Record{Source: "/vol02/1000-0-offline", Target: "/vol1/1000/share/test", Enabled: true, MountID: "owned"}
		reconcile(&Store{}, r, []MountInfo{{Point: r.Target, ID: "owned"}})
		if r.Status != "source_offline" || !strings.Contains(r.Error, "卸载失败") {
			t.Fatalf("%+v", r)
		}
	})
}
