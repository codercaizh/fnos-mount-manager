package copyengine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var volRE = regexp.MustCompile(`^vol[0-9]+$`)
var uidRE = regexp.MustCompile(`^[0-9]+$`)

type Entry struct {
	Source    string `json:"source"`
	Dest      string `json:"dest"`
	Name      string `json:"name"`
	Dir       bool   `json:"dir"`
	Bytes     int64  `json:"bytes"`
	Files     int64  `json:"files"`
	Completed bool   `json:"completed,omitempty"`
}
type Job struct {
	ID               string    `json:"id"`
	Sources          []string  `json:"sources"`
	Target           string    `json:"target"`
	Cores            int       `json:"cores,omitempty"`
	Transfers        int       `json:"transfers"`
	Verify           string    `json:"verify"`
	State            string    `json:"state"`
	Error            string    `json:"error,omitempty"`
	Created          time.Time `json:"created"`
	Updated          time.Time `json:"updated"`
	TotalBytes       int64     `json:"totalBytes"`
	TotalFiles       int64     `json:"totalFiles"`
	DoneBytes        int64     `json:"doneBytes"`
	DoneFiles        int64     `json:"doneFiles"`
	CurrentDoneFiles int64     `json:"currentDoneFiles"`
	CurrentBytes     int64     `json:"currentBytes"`
	Speed            float64   `json:"speed"`
	ETA              int64     `json:"eta"`
	CurrentFile      string    `json:"currentFile,omitempty"`
	CurrentFiles     []string  `json:"currentFiles,omitempty"`
	ActiveTransfers  int       `json:"activeTransfers"`
	Step             int       `json:"step"`
	Items            []Entry   `json:"items,omitempty"`
}
type Manager struct {
	mu        sync.Mutex
	jobs      []*Job
	stateFile string
	rclone    string
	active    string
	cancel    context.CancelFunc
}

func cleanPath(p string) (string, error) {
	if p == "" || !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", errors.New("请选择有效的绝对路径")
	}
	p = filepath.Clean(p)
	if p == "/" {
		return "", errors.New("不允许操作根目录")
	}
	return p, nil
}
func allowedPath(p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 || !volRE.MatchString(parts[0]) {
		return false
	}
	if parts[0] == "vol02" && strings.Contains(parts[1], "-") {
		return len(parts[1]) > 2
	}
	if len(parts) < 3 || !uidRE.MatchString(parts[1]) || parts[2] == "" || strings.HasPrefix(parts[2], "@") || strings.HasPrefix(parts[2], ".") {
		return false
	}
	for _, x := range parts[2:] {
		if strings.HasPrefix(x, "@") {
			return false
		}
	}
	return true
}
func allowedTarget(p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 || !volRE.MatchString(parts[0]) || !uidRE.MatchString(parts[1]) || strings.HasPrefix(parts[2], "@") || strings.HasPrefix(parts[2], ".") || parts[2] == "" {
		return false
	}
	for _, x := range parts[2:] {
		if strings.HasPrefix(x, "@") {
			return false
		}
	}
	return true
}
func noLinkAncestors(p string) error {
	cur := "/"
	for _, part := range strings.Split(strings.Trim(p, "/"), "/") {
		cur = filepath.Join(cur, part)
		info, e := os.Lstat(cur)
		if errors.Is(e, os.ErrNotExist) {
			break
		}
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("路径包含符号链接：%s", cur)
		}
	}
	return nil
}
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func validateInputs(src []string, target string, cores int, verify string) ([]Entry, error) {
	if len(src) < 1 || len(src) > 256 {
		return nil, errors.New("一次请选择 1～256 个来源")
	}
	if cores < 1 || cores > 256 {
		return nil, errors.New("并行数需为 1～256")
	}
	if verify != "size" && verify != "content" {
		return nil, errors.New("校验方式无效")
	}
	dest, e := cleanPath(target)
	if e != nil || !allowedTarget(dest) {
		return nil, errors.New("目标必须是飞牛用户共享目录")
	}
	if e = noLinkAncestors(dest); e != nil {
		return nil, e
	}
	info, e := os.Stat(dest)
	if e != nil || !info.IsDir() {
		return nil, errors.New("目标目录不存在或不是目录")
	}
	items := make([]Entry, 0, len(src))
	paths := make(map[string]bool)
	names := make(map[string]bool)
	for _, raw := range src {
		p, err := cleanPath(raw)
		if err != nil || !allowedPath(p) {
			return nil, fmt.Errorf("来源路径不属于允许的飞牛共享或 SMB 挂载：%q", raw)
		}
		if e = noLinkAncestors(p); e != nil {
			return nil, e
		}
		info, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("无法读取来源 %s：%w", p, err)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("仅支持普通文件和目录：%s", p)
		}
		if overlaps(p, dest) {
			return nil, fmt.Errorf("禁止来源与目标互相包含：%s", p)
		}
		for existed := range paths {
			if overlaps(existed, p) {
				return nil, errors.New("不能同时选择父目录和它的子路径")
			}
		}
		paths[p] = true
		name := filepath.Base(p)
		if names[name] {
			return nil, fmt.Errorf("不同来源同名：%s，请分开创建任务", name)
		}
		names[name] = true
		to := filepath.Join(dest, name)
		if err = noLinkAncestors(to); err != nil {
			return nil, err
		}
		if st, err := os.Lstat(to); err == nil && (st.IsDir() != info.IsDir() || st.Mode()&os.ModeSymlink != 0) {
			return nil, fmt.Errorf("目标存在同名但类型不同的对象：%s", to)
		}
		items = append(items, Entry{Source: p, Dest: to, Name: name, Dir: info.IsDir()})
	}
	return items, nil
}

func idgen() string { var b [8]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

// Keep at most three latest completed jobs; pending, paused and failed jobs remain recoverable.
// Caller must hold m.mu.
func (m *Manager) pruneCompletedLocked() {
	var completed []*Job
	for _, j := range m.jobs {
		if j.State == "completed" {
			completed = append(completed, j)
		}
	}
	if len(completed) <= 3 {
		return
	}
	sort.SliceStable(completed, func(i, j int) bool {
		return completed[i].Updated.After(completed[j].Updated)
	})
	keep := make(map[*Job]bool, 3)
	for _, j := range completed[:3] {
		keep[j] = true
	}
	filtered := m.jobs[:0]
	for _, j := range m.jobs {
		if j.State != "completed" || keep[j] {
			filtered = append(filtered, j)
		}
	}
	m.jobs = filtered
}
func (m *Manager) saveLocked() error {
	m.pruneCompletedLocked()
	raw, e := json.MarshalIndent(struct {
		Jobs []*Job `json:"jobs"`
	}{m.jobs}, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(m.stateFile), 0700); e != nil {
		return e
	}
	tmp := m.stateFile + ".tmp"
	if e = os.WriteFile(tmp, raw, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, m.stateFile)
}
func NewManager(file, bin string) (*Manager, error) {
	m := &Manager{stateFile: file, rclone: bin}
	data, e := os.ReadFile(file)
	if errors.Is(e, os.ErrNotExist) {
		return m, nil
	}
	if e != nil {
		return nil, e
	}
	var s struct {
		Jobs []*Job `json:"jobs"`
	}
	if e = json.Unmarshal(data, &s); e != nil {
		return nil, e
	}
	m.jobs = s.Jobs
	for _, j := range m.jobs {
		if j.State == "running" || j.State == "scanning" || j.State == "verifying" || j.State == "queued" {
			j.State = "paused"
			j.Error = "服务重启后已暂停，点击继续以跳过已完成文件"
		}
		j.Speed = 0
		j.ETA = -1
		j.CurrentBytes = 0
		j.CurrentDoneFiles = 0
		j.CurrentFiles = nil
		j.ActiveTransfers = 0
	}
	before := len(m.jobs)
	m.pruneCompletedLocked()
	if len(m.jobs) != before {
		if err := m.saveLocked(); err != nil {
			return nil, err
		}
	}
	return m, nil
}
func (m *Manager) lookup(id string) *Job {
	for _, j := range m.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}
func (m *Manager) kick() {
	m.mu.Lock()
	if m.active != "" {
		m.mu.Unlock()
		return
	}
	var selected *Job
	for _, j := range m.jobs {
		if j.State == "queued" {
			selected = j
			break
		}
	}
	if selected == nil {
		m.mu.Unlock()
		return
	}
	m.active = selected.ID
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	selected.State = "scanning"
	selected.CurrentDoneFiles = 0
	selected.Error = ""
	selected.Updated = time.Now()
	_ = m.saveLocked()
	m.mu.Unlock()
	go m.run(ctx, selected.ID)
}
func (m *Manager) mutate(id string, fn func(*Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.lookup(id); j != nil {
		fn(j)
		j.Updated = time.Now()
	}
}
func (m *Manager) end(id, state string, err error) {
	m.mu.Lock()
	j := m.lookup(id)
	if j != nil {
		if j.State != "paused" {
			j.State = state
			if err != nil {
				j.Error = err.Error()
			} else {
				j.Error = ""
			}
		}
		if j.State != "paused" {
			j.CurrentDoneFiles = 0
		}
		j.CurrentBytes = 0
		j.Speed = 0
		j.ETA = -1
		j.CurrentFile = ""
		j.CurrentFiles = nil
		j.ActiveTransfers = 0
		j.Updated = time.Now()
		_ = m.saveLocked()
	}
	if m.active == id {
		m.active = ""
		m.cancel = nil
	}
	m.mu.Unlock()
	m.kick()
}
func scan(ctx context.Context, source string) (int64, int64, error) {
	var total, files int64
	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		total += info.Size()
		files++
		return nil
	})
	return total, files, err
}

type rcStats struct {
	Bytes        int64   `json:"bytes"`
	Checks       int64   `json:"checks"`
	Transfers    int64   `json:"transfers"`
	TotalBytes   int64   `json:"totalBytes"`
	Speed        float64 `json:"speed"`
	Transferring []struct {
		Name     string  `json:"name"`
		SpeedAvg float64 `json:"speedAvg"`
	} `json:"transferring"`
}
type logLine struct {
	Msg   string   `json:"msg"`
	Level string   `json:"level"`
	Stats *rcStats `json:"stats"`
}

func (m *Manager) runRclone(ctx context.Context, id string, args ...string) error {
	return m.runRcloneTracked(ctx, id, nil, args...)
}
func (m *Manager) runRcloneTracked(ctx context.Context, id string, onStats func(rcStats), args ...string) error {
	cmd := exec.CommandContext(ctx, m.rclone, args...)
	cmd.Env = append(os.Environ(), "RCLONE_CONFIG=/dev/null")
	stderr, e := cmd.StderrPipe()
	if e != nil {
		return e
	}
	if e = cmd.Start(); e != nil {
		return e
	}
	var tail []string
	var lastTick = time.Now()
	var lastBytes int64
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 4096), 1024*1024)
		for s.Scan() {
			line := s.Text()
			var record logLine
			if json.Unmarshal([]byte(line), &record) != nil {
				continue
			}
			if record.Stats != nil {
				if onStats != nil {
					onStats(*record.Stats)
					continue
				}
				now := time.Now()
				delta := now.Sub(lastTick).Seconds()
				rate := 0.0
				if delta > 0.15 && record.Stats.Bytes >= lastBytes {
					rate = float64(record.Stats.Bytes-lastBytes) / delta
				}
				lastTick = now
				lastBytes = record.Stats.Bytes
				m.mutate(id, func(j *Job) {
					j.CurrentBytes = record.Stats.Bytes
					if rate > 0 {
						if j.Speed > 0 {
							j.Speed = j.Speed*.35 + rate*.65
						} else {
							j.Speed = rate
						}
					}
					if j.Speed > 0 {
						j.ETA = int64(float64(max64(j.TotalBytes-j.DoneBytes-j.CurrentBytes, 0)) / j.Speed)
					} else {
						j.ETA = -1
					}
					j.ActiveTransfers = len(record.Stats.Transferring)
					j.CurrentFiles = j.CurrentFiles[:0]
					for _, t := range record.Stats.Transferring {
						if len(j.CurrentFiles) < 12 {
							j.CurrentFiles = append(j.CurrentFiles, t.Name)
						}
					}
					if len(j.CurrentFiles) > 0 {
						j.CurrentFile = j.CurrentFiles[0]
					} else {
						j.CurrentFile = ""
					}
				})
			} else if record.Level == "error" || record.Level == "fatal" {
				tail = append(tail, record.Msg)
				if len(tail) > 4 {
					tail = tail[len(tail)-4:]
				}
			}
		}
	}()
	err := cmd.Wait()
	<-scanDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if len(tail) > 0 {
			return fmt.Errorf("%s: %s", err, strings.Join(tail, "；"))
		}
		return fmt.Errorf("rclone 执行失败：%w", err)
	}
	return nil
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// activeFileSample aggregates multiple independent rclone processes into
// one bounded-progress view. Each task's concurrency is divided into worker
// capacities so the total --transfers never exceeds the requested limit.
type transferProgress struct {
	mu        sync.Mutex
	parts     map[int]rcStats
	entries   []Entry
	tick      time.Time
	lastBytes int64
	speed     float64
}

func newTransferProgress(entries []Entry) *transferProgress {
	return &transferProgress{parts: make(map[int]rcStats), entries: entries, tick: time.Now()}
}
func (p *transferProgress) record(m *Manager, id string, i int, stats *rcStats) {
	p.mu.Lock()
	if stats == nil {
		delete(p.parts, i)
	} else {
		p.parts[i] = *stats
	}
	var bytes int64
	var currentDoneFiles int64
	active := 0
	names := make([]string, 0, 12)
	for idx, stat := range p.parts {
		bytes += stat.Bytes
		// In rclone's JSON stats, checks are skipped/up-to-date files and
		// transfers are successfully transferred files. Both are finished
		// files, unlike the 'transferring' entries which are still in flight.
		finished := stat.Checks + stat.Transfers
		if finished < 0 {
			finished = 0
		}
		if finished > p.entries[idx].Files {
			finished = p.entries[idx].Files
		}
		currentDoneFiles += finished
		active += len(stat.Transferring)
		for _, file := range stat.Transferring {
			if len(names) >= 12 {
				break
			}
			display := p.entries[idx].Name
			if p.entries[idx].Dir && file.Name != "" {
				display += "/" + file.Name
			}
			names = append(names, display)
		}
	}
	now := time.Now()
	if elapsed := now.Sub(p.tick).Seconds(); elapsed >= 0.6 {
		delta := bytes - p.lastBytes
		if delta >= 0 {
			instant := float64(delta) / elapsed
			if instant > 0 {
				if p.speed <= 0 {
					p.speed = instant
				} else {
					p.speed = p.speed*.35 + instant*.65
				}
			}
		}
		p.tick = now
		p.lastBytes = bytes
	}
	speed := p.speed
	p.mu.Unlock()
	m.mutate(id, func(j *Job) {
		j.CurrentBytes = bytes
		j.CurrentDoneFiles = currentDoneFiles
		j.ActiveTransfers = active
		j.CurrentFiles = names
		if len(names) > 0 {
			j.CurrentFile = names[0]
		} else {
			j.CurrentFile = ""
		}
		j.Speed = speed
		if speed > 0 {
			j.ETA = int64(float64(max64(j.TotalBytes-j.DoneBytes-bytes, 0)) / speed)
		} else {
			j.ETA = -1
		}
	})
}

// transferConcurrent distributes the parallelism budget across independent
// sources. One selected folder gets all slots, while separate selected files
// can run concurrently. A job stays resumable via per-item completion flags.
func (m *Manager) transferConcurrent(ctx context.Context, id string, entries []Entry, step, parallel int, verify string) error {
	m.mu.Lock()
	j := m.lookup(id)
	if j == nil {
		m.mu.Unlock()
		return errors.New("任务不存在")
	}
	// Older task records used only the sequential Step checkpoint.
	anyCompleted := false
	for _, it := range j.Items {
		if it.Completed {
			anyCompleted = true
			break
		}
	}
	if !anyCompleted && step > 0 {
		for i := 0; i < minInt(step, len(j.Items)); i++ {
			j.Items[i].Completed = true
		}
	}
	// Reconstruct completed counters from durable per-source checkpoints.
	j.DoneBytes, j.DoneFiles = 0, 0
	for _, it := range j.Items {
		if it.Completed {
			j.DoneBytes += it.Bytes
			j.DoneFiles += it.Files
		}
	}
	_ = m.saveLocked()
	todo := make([]int, 0, len(entries))
	for i, it := range j.Items {
		if !it.Completed {
			todo = append(todo, i)
		}
	}
	m.mu.Unlock()
	if len(todo) == 0 {
		return nil
	}
	if parallel < 1 {
		parallel = 1
	}
	workers := minInt(len(todo), parallel)
	progress := newTransferProgress(entries)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan int, len(todo))
	for _, i := range todo {
		queue <- i
	}
	close(queue)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for worker := 0; worker < workers; worker++ {
		capacity := parallel / workers
		if worker < parallel%workers {
			capacity++
		}
		wg.Add(1)
		go func(slots int) {
			defer wg.Done()
			for idx := range queue {
				if workCtx.Err() != nil {
					return
				}
				it := entries[idx]
				// Revalidate every source/target before starting any write.
				if _, err := validateInputs([]string{it.Source}, filepath.Dir(it.Dest), parallel, verify); err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					return
				}
				m.mutate(id, func(j *Job) { j.State = "running" })
				opts := []string{"--stats=1s", "--stats-one-line", "--stats-log-level=NOTICE", "--use-json-log",
					"--transfers=" + strconv.Itoa(slots), "--checkers=" + strconv.Itoa(slots),
					"--retries=3", "--low-level-retries=10", "--multi-thread-streams=2"}
				if verify == "content" {
					opts = append(opts, "--checksum")
				} else {
					opts = append(opts, "--size-only")
				}
				var args []string
				if it.Dir {
					args = []string{"copy", it.Source, it.Dest}
				} else {
					args = []string{"copyto", it.Source, it.Dest}
				}
				args = append(args, opts...)
				handler := func(st rcStats) { progress.record(m, id, idx, &st) }
				err := m.runRcloneTracked(workCtx, id, handler, args...)
				if err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					return
				}
				m.mu.Lock()
				if j := m.lookup(id); j != nil && !j.Items[idx].Completed {
					j.Items[idx].Completed = true
					j.DoneBytes += it.Bytes
					j.DoneFiles += it.Files
					for j.Step < len(j.Items) && j.Items[j.Step].Completed {
						j.Step++
					}
					_ = m.saveLocked()
				}
				m.mu.Unlock()
				progress.record(m, id, idx, nil)
			}
		}(capacity)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if firstErr != nil {
		return firstErr
	}
	m.mutate(id, func(j *Job) {
		j.CurrentBytes = 0
		j.CurrentDoneFiles = 0
		j.CurrentFiles = nil
		j.ActiveTransfers = 0
		j.Speed = 0
		j.ETA = -1
		j.CurrentFile = ""
	})
	return nil
}

func (m *Manager) run(ctx context.Context, id string) {
	m.mu.Lock()
	j := m.lookup(id)
	entries := append([]Entry(nil), j.Items...)
	step := j.Step
	parallel := j.Transfers
	if parallel < 1 {
		parallel = j.Cores * 2
	}
	verify := j.Verify
	m.mu.Unlock()
	if len(entries) == 0 {
		m.mutate(id, func(j *Job) { j.State = "scanning"; j.TotalBytes = 0; j.TotalFiles = 0 })
		var total, files int64
		for i := range j.Sources {
			m.mu.Lock()
			path := j.Sources[i]
			target := j.Target
			m.mu.Unlock()
			input, err := validateInputs([]string{path}, target, parallel, verify)
			if err != nil {
				m.end(id, "error", err)
				return
			}
			v := input[0]
			v.Bytes, v.Files, err = scan(ctx, v.Source)
			if err != nil {
				m.end(id, "error", err)
				return
			}
			total += v.Bytes
			files += v.Files
			entries = append(entries, v)
			m.mutate(id, func(j *Job) {
				j.TotalBytes = total
				j.TotalFiles = files
				j.CurrentFile = "已扫描 " + strconv.Itoa(i+1) + "/" + strconv.Itoa(len(j.Sources)) + " 项"
			})
		}
		m.mu.Lock()
		if jj := m.lookup(id); jj != nil {
			jj.Items = entries
			jj.TotalBytes = total
			jj.TotalFiles = files
			jj.Step = 0
			_ = m.saveLocked()
		}
		m.mu.Unlock()
	}
	if err := m.transferConcurrent(ctx, id, entries, step, parallel, verify); err != nil {
		if ctx.Err() != nil {
			m.end(id, "paused", nil)
		} else {
			m.end(id, "error", err)
		}
		return
	}

	if verify == "content" {
		m.mutate(id, func(j *Job) {
			j.State = "verifying"
			j.CurrentFile = "正在逐块读取源和目标进行内容校验"
			j.Speed = 0
			j.ETA = -1
		})
		for _, it := range entries {
			if ctx.Err() != nil {
				m.end(id, "paused", nil)
				return
			}
			var src, dst string
			args := []string{"check"}
			if it.Dir {
				src, dst = it.Source, it.Dest
			} else {
				src, dst = filepath.Dir(it.Source), filepath.Dir(it.Dest)
				// Literal NUL-delimited names avoid interpreting glob metacharacters.
				listFile, err := os.CreateTemp(filepath.Dir(m.stateFile), "verify-*.lst")
				if err != nil {
					m.end(id, "error", err)
					return
				}
				_, err = listFile.Write(append([]byte(it.Name), 0))
				_ = listFile.Close()
				if err != nil {
					_ = os.Remove(listFile.Name())
					m.end(id, "error", err)
					return
				}
				defer os.Remove(listFile.Name())
				args = append(args, "--files-from0="+listFile.Name())
			}
			args = append(args, src, dst, "--download", "--one-way", "--checkers="+strconv.Itoa(parallel), "--use-json-log", "--stats=1s", "--stats-one-line", "--stats-log-level=NOTICE")
			if err := m.runRclone(ctx, id, args...); err != nil {
				if ctx.Err() != nil {
					m.end(id, "paused", nil)
				} else {
					m.end(id, "error", fmt.Errorf("内容校验未通过：%w", err))
				}
				return
			}
		}
	}
	m.end(id, "completed", nil)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, to any) bool {
	// fnOS proxies the browser request through an internal gateway. Internal Host
	// can differ from the page Origin, so equality is not a valid CSRF check.
	// Non-simple JSON and custom header require cross-origin browser preflight;
	// this service never authorizes CORS preflights.
	if r.Header.Get("X-Fnos-Quickcopy-UI") != "1" {
		fail(w, 403, "缺少应用请求标识")
		return false
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, 415, "请使用 JSON 请求")
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "不接受跨站请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(to); err != nil {
		fail(w, 400, "请求数据有误："+err.Error())
		return false
	}
	return true
}
func (m *Manager) Serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == "GET" && path == "/api/status" {
		m.mu.Lock()
		out := make([]Job, 0, len(m.jobs))
		for i := len(m.jobs) - 1; i >= 0; i-- {
			item := *m.jobs[i]
			item.Items = nil
			out = append(out, item)
		}
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{"jobs": out, "cpu": runtime.NumCPU(), "rclone": filepath.Base(m.rclone), "defaultParallel": minInt(runtime.NumCPU()*2, 256)})
		return
	}
	if r.Method == "GET" && path == "/api/detail" {
		id := r.URL.Query().Get("id")
		m.mu.Lock()
		j := m.lookup(id)
		if j == nil {
			m.mu.Unlock()
			fail(w, 404, "任务不存在")
			return
		}
		copy := *j
		copy.Items = append([]Entry(nil), j.Items...)
		m.mu.Unlock()
		writeJSON(w, 200, copy)
		return
	}
	if r.Method == "POST" && path == "/api/create" {
		var q struct {
			Sources  []string `json:"sources"`
			Target   string   `json:"target"`
			Parallel int      `json:"parallel"`
			Verify   string   `json:"verify"`
		}
		if !decode(w, r, &q) {
			return
		}
		if q.Parallel == 0 {
			q.Parallel = minInt(runtime.NumCPU()*2, 256)
		}
		if q.Verify == "" {
			q.Verify = "size"
		}
		items, e := validateInputs(q.Sources, q.Target, q.Parallel, q.Verify)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		normalized := make([]string, 0, len(items))
		for _, it := range items {
			normalized = append(normalized, it.Source)
		}
		job := &Job{ID: idgen(), Sources: normalized, Target: filepath.Clean(q.Target), Transfers: q.Parallel, Verify: q.Verify, State: "queued", Created: time.Now(), Updated: time.Now(), ETA: -1}
		m.mu.Lock()
		m.jobs = append(m.jobs, job)
		e = m.saveLocked()
		m.mu.Unlock()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		m.kick()
		writeJSON(w, 201, job)
		return
	}
	if r.Method == "POST" && (path == "/api/pause" || path == "/api/resume" || path == "/api/remove") {
		var q struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &q) {
			return
		}
		m.mu.Lock()
		j := m.lookup(q.ID)
		if j == nil {
			m.mu.Unlock()
			fail(w, 404, "任务不存在")
			return
		}
		switch path {
		case "/api/pause":
			if j.State != "running" && j.State != "scanning" && j.State != "verifying" && j.State != "queued" {
				m.mu.Unlock()
				fail(w, 409, "任务当前不能暂停")
				return
			}
			j.State = "paused"
			j.ETA = -1
			j.Speed = 0
			if m.active == q.ID && m.cancel != nil {
				m.cancel()
			}
		case "/api/resume":
			if j.State != "paused" && j.State != "error" {
				m.mu.Unlock()
				fail(w, 409, "只有暂停或失败的任务可以继续")
				return
			}
			j.State = "queued"
			j.Error = ""
			j.ETA = -1
			j.CurrentBytes = 0
			j.CurrentDoneFiles = 0
			j.Speed = 0
			// A verification-only resume should re-run verification.
			if j.Step >= len(j.Items) && j.Verify == "content" {
				j.Step = len(j.Items)
			}
		case "/api/remove":
			if m.active == q.ID || j.State == "running" || j.State == "scanning" || j.State == "verifying" {
				m.mu.Unlock()
				fail(w, 409, "请先暂停并等待任务停止，再删除记录")
				return
			}
			for i, x := range m.jobs {
				if x.ID == q.ID {
					m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
					break
				}
			}
		}
		e := m.saveLocked()
		m.mu.Unlock()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		if path == "/api/resume" || path == "/api/pause" {
			m.kick()
		}
		return
	}
	http.NotFound(w, r)
}

// ImportCompletedHistory merges only completed legacy jobs from the standalone
// copy app. Active jobs are never imported; their old process may still run.
func (m *Manager) ImportCompletedHistory(filename string) (int, error) {
	raw, e := os.ReadFile(filename)
	if errors.Is(e, os.ErrNotExist) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	if len(raw) > 8<<20 {
		return 0, errors.New("legacy history too large")
	}
	var state struct {
		Jobs []*Job `json:"jobs"`
	}
	if e = json.Unmarshal(raw, &state); e != nil {
		return 0, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]bool, len(m.jobs))
	for _, j := range m.jobs {
		ids[j.ID] = true
	}
	// Import only the three newest legacy completions. Repeated app launches
	// must not repeatedly re-import already pruned historical records.
	completed := make([]*Job, 0)
	for _, j := range state.Jobs {
		if j != nil && j.ID != "" && j.State == "completed" {
			completed = append(completed, j)
		}
	}
	sort.SliceStable(completed, func(i, j int) bool { return completed[i].Updated.After(completed[j].Updated) })
	if len(completed) > 3 {
		completed = completed[:3]
	}
	added := 0
	for _, j := range completed {
		if ids[j.ID] {
			continue
		}
		m.jobs = append(m.jobs, j)
		ids[j.ID] = true
		added++
	}
	if added > 0 {
		if e = m.saveLocked(); e != nil {
			return 0, e
		}
	}
	return added, nil
}

// Stop makes the in-progress rclone command exit on service shutdown.
// Persistence is handled when the worker observes context cancellation.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}
