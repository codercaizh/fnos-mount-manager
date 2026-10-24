package copyengine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RCLONE_TEST_BIN opts in to actual rclone integration, independently of NAS mounts.
func TestCombinedSourceRealRclone(t *testing.T) {
	bin := os.Getenv("RCLONE_TEST_BIN")
	if bin == "" {
		t.Skip("set RCLONE_TEST_BIN to run the isolated end-to-end test")
	}
	for _, tc := range []struct {
		name           string
		folders, files bool
	}{
		{"mixed folders and files", true, true},
		{"directories only", true, false},
		{"selected files only", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "destination")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			var entries []Entry
			wanted := map[string]string{}
			makeFile := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.folders {
				one := filepath.Join(root, "source top", "爸爸 相册")
				two := filepath.Join(root, "source=2", "妈妈 \"相册\"")
				makeFile(filepath.Join(one, "nested", "A.jpg"), "dad-content")
				makeFile(filepath.Join(two, "B.jpg"), "mom-content")
				// A symlink within a selected directory must NOT be followed.
				if err := os.Symlink("/etc/hosts", filepath.Join(two, "skip-link")); err != nil {
					t.Fatal(err)
				}
				entries = append(entries, Entry{Source: one, Name: "爸爸 相册", Dir: true}, Entry{Source: two, Name: "妈妈 \"相册\"", Dir: true})
				wanted["爸爸 相册/nested/A.jpg"] = "dad-content"
				wanted["妈妈 \"相册\"/B.jpg"] = "mom-content"
			}
			if tc.files {
				first := filepath.Join(root, "separate", "已选 #1.txt")
				second := filepath.Join(root, "another parent", "file with spaces.txt")
				makeFile(first, "single-content")
				makeFile(second, "other-content")
				// An unselected file must never be copied.
				makeFile(filepath.Join(root, "separate", "do-not-copy"), "secret")
				entries = append(entries, Entry{Source: first, Name: "已选 #1.txt"}, Entry{Source: second, Name: "file with spaces.txt"})
				wanted["已选 #1.txt"] = "single-content"
				wanted["file with spaces.txt"] = "other-content"
			}
			source, err := makeCombinedSource(entries, filepath.Join(root, "copies.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer source.clean()
			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command(bin, args...)
				out, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("rclone %v: %v\n%s", args, e, out)
				}
			}
			run("copy", source.remote, target, "--config="+source.config,
				"--transfers=8", "--checkers=8", "--size-only", "--skip-links")
			for file, expected := range wanted {
				b, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(file)))
				if err != nil || string(b) != expected {
					t.Fatalf("%s: got %q, %v; want %q", file, b, err, expected)
				}
			}
			if tc.files {
				if _, err := os.Stat(filepath.Join(target, "do-not-copy")); !os.IsNotExist(err) {
					t.Fatal("copied an unselected file")
				}
			}
			if tc.folders {
				if _, err := os.Stat(filepath.Join(target, "妈妈 \"相册\"", "skip-link")); !os.IsNotExist(err) {
					t.Fatal("followed an unselected source symlink")
				}
			}
			// A repeated run must be incremental without deleting user files.
			unrelated := filepath.Join(target, "preserved.txt")
			makeFile(unrelated, "must remain")
			run("copy", source.remote, target, "--config="+source.config, "--transfers=8", "--size-only", "--skip-links")
			if b, err := os.ReadFile(unrelated); err != nil || !bytes.Equal(b, []byte("must remain")) {
				t.Fatal("unexpected cleanup")
			}
			// Download-based verification detects corruption (same size).
			run("check", source.remote, target, "--config="+source.config, "--download", "--one-way", "--skip-links")
			for file := range wanted {
				if !strings.HasSuffix(file, ".jpg") {
					continue
				}
				makeFile(filepath.Join(target, filepath.FromSlash(file)), strings.Repeat("x", len(wanted[file])))
				cmd := exec.Command(bin, "check", source.remote, target, "--config="+source.config, "--download", "--one-way", "--skip-links")
				if err := cmd.Run(); err == nil {
					t.Fatal("strict verification missed corruption")
				}
				break
			}
			config := source.config
			source.clean()
			if _, err := os.Stat(config); !os.IsNotExist(err) {
				t.Fatal("temporary rclone configuration was not cleaned")
			}
		})
	}
}

func TestCombinedUnsupportedDirectoryAliasIsRejected(t *testing.T) {
	root := t.TempDir()
	entry := Entry{Source: filepath.Join(root, "family=photos"), Name: "family=photos", Dir: true}
	if _, err := makeCombinedSource([]Entry{entry}, filepath.Join(root, "tasks.json")); err == nil {
		t.Fatal("rclone cannot safely split '=' inside an upstream alias")
	}
	leftovers, err := filepath.Glob(filepath.Join(root, "copy-source-*"))
	if err != nil || len(leftovers) > 0 {
		t.Fatalf("temporary staging leaked: %v %v", leftovers, err)
	}
}
