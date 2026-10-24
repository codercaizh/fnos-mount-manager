package copyengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// combinedSource presents distinct selected source folders and files under a
// single virtual root. No user data is staged: selected files are represented
// by symlinks inside a private, temporary directory.
//
// rclone's combine backend maps each selected directory to its destination
// basename. The local backend with copy_links=true dereferences only the
// private symlinks for explicitly selected files; the directory backends keep
// the default symlink-skipping behavior. Union merges these namespaces.
// Exactly one rclone copy process owns all --transfers slots.
type combinedSource struct {
	remote string
	config string
	clean  func()
}

func makeCombinedSource(entries []Entry, stateFile string) (combinedSource, error) {
	if len(entries) == 0 {
		return combinedSource{}, errors.New("没有待复制的来源")
	}
	dir, err := os.MkdirTemp(filepath.Dir(stateFile), "copy-source-")
	if err != nil {
		return combinedSource{}, err
	}
	fail := func(e error) (combinedSource, error) {
		_ = os.RemoveAll(dir)
		return combinedSource{}, e
	}
	stage := filepath.Join(dir, "selected-files")
	var upstreams []string
	var hasFiles bool
	for _, entry := range entries {
		// Names are destination basenames, never arbitrary paths.
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." ||
			entry.Name != filepath.Base(entry.Name) || entry.Name != filepath.Base(entry.Source) {
			return fail(fmt.Errorf("来源名称无效：%q", entry.Name))
		}
		if entry.Dir {
			// rclone's combine upstream parser reserves '=' as the alias delimiter.
			// Refuse unsupported names rather than risking a wrong copy target.
			if strings.Contains(entry.Name, "=") {
				return fail(fmt.Errorf("来源目录名包含不支持的字符 '='：%s", entry.Name))
			}
			// SpaceSepList is CSV-style (space delimiter), not Go string quoting.
			// Embedded quotes must be doubled, including quotes in source paths.
			if strings.ContainsAny(entry.Name+entry.Source, "\r\n") {
				return fail(fmt.Errorf("来源目录名或路径包含不支持的换行符：%q", entry.Name))
			}
			value := strings.ReplaceAll(entry.Name+"="+entry.Source, "\"", "\"\"")
			upstreams = append(upstreams, "\""+value+"\"")
		} else {
			if !hasFiles {
				if err := os.Mkdir(stage, 0700); err != nil {
					return fail(err)
				}
			}
			hasFiles = true
			if err := os.Symlink(entry.Source, filepath.Join(stage, entry.Name)); err != nil {
				return fail(err)
			}
		}
	}
	var conf strings.Builder
	var remote string
	if len(upstreams) > 0 {
		conf.WriteString("[selected_dirs]\ntype = combine\nupstreams = ")
		conf.WriteString(strings.Join(upstreams, " "))
		conf.WriteString("\n")
		remote = "selected_dirs:"
	}
	if hasFiles {
		// Only this specifically named local backend dereferences symbolic links.
		conf.WriteString("[selected_files]\ntype = local\ncopy_links = true\n")
		if len(upstreams) == 0 {
			remote = "selected_files:" + stage
		} else {
			conf.WriteString("[selected_all]\ntype = union\nupstreams = selected_dirs: selected_files:")
			conf.WriteString(stage)
			conf.WriteString("\n")
			remote = "selected_all:"
		}
	}
	configPath := filepath.Join(dir, "rclone.conf")
	if err := os.WriteFile(configPath, []byte(conf.String()), 0600); err != nil {
		return fail(err)
	}
	return combinedSource{
		remote: remote,
		config: configPath,
		clean:  func() { _ = os.RemoveAll(dir) },
	}, nil
}

// Clamp rclone's cumulative skipped + finished count to the pending file set.
func completedFileCount(stat rcStats, pending int64) int64 {
	done := stat.Checks + stat.Transfers
	if done < 0 {
		return 0
	}
	if done > pending {
		return pending
	}
	return done
}
