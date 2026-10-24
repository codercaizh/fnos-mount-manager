package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fnos-mount-manager/internal/copyengine"
)

//go:embed web/*
var assets embed.FS

const base = "/app/fnos-mount-manager/"

var volumeRE = regexp.MustCompile(`^vol[1-9][0-9]*$`)
var uidRE = regexp.MustCompile(`^[0-9]+$`)
var safeNameRE = regexp.MustCompile(`^[^/\\\x00-\x1f]+$`)

type Record struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Mode    string `json:"mode"`
	Enabled bool   `json:"enabled"`
	Owned   bool   `json:"owned"`
	MountID string `json:"mount_id,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Checked string `json:"checked,omitempty"`
}
type Store struct {
	sync.Mutex
	copier  *copyengine.Manager
	file    string
	Records []*Record `json:"records"`
}
type MountInfo struct{ Point, Device, ID, FSType, Options, Source string }

// The syscall adapters let tests exercise remount/rollback without requiring CAP_SYS_ADMIN.
var mountCall = syscall.Mount
var unmountCall = syscall.Unmount

func cleanAbsolute(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("必须使用绝对路径")
	}
	p := filepath.Clean(path)
	if p == "/" {
		return "", errors.New("不可使用文件系统根目录")
	}
	return p, nil
}
func pieces(path string) []string { return strings.Split(strings.Trim(path, "/"), "/") }
func localPath(path string, parent bool) bool {
	a := pieces(path)
	if len(a) < 3 || !volumeRE.MatchString(a[0]) || !uidRE.MatchString(a[1]) {
		return false
	}
	if strings.HasPrefix(a[2], "@") || strings.HasPrefix(a[2], ".") {
		return false
	}
	for _, v := range a[2:] {
		if strings.HasPrefix(v, "@") {
			return false
		}
	}
	return true
}
func remotePath(path string) bool {
	a := pieces(path)
	return len(a) >= 2 && a[0] == "vol02" && strings.HasPrefix(a[1], "1000-") || (len(a) >= 2 && a[0] == "vol02" && strings.Contains(a[1], "-"))
}
func sourceAllowed(p string) bool { return remotePath(p) || localPath(p, false) }
func checkNoSymlink(p string) error {
	if p == "" || p[0] != '/' {
		return errors.New("不是绝对路径")
	}
	prefix := "/"
	for _, part := range pieces(p) {
		prefix = filepath.Join(prefix, part)
		fi, err := os.Lstat(prefix)
		if err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("路径包含符号链接，已拒绝")
		}
		if !fi.IsDir() {
			return errors.New("路径不是目录")
		}
	}
	return nil
}
func strictName(name string) error {
	if name == "" || name == "." || name == ".." || name != strings.TrimSpace(name) ||
		!safeNameRE.MatchString(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") ||
		len([]byte(name)) > 140 {
		return errors.New("目录名称不合法（不得包含斜杠、控制字符、首尾空格或系统前缀）")
	}
	return nil
}

// duplicateSource returns another mapping with the same exact source path.
// Most such mappings are accidental: the create form must reselect a source.
func duplicateSource(records []*Record, source string, except *Record) *Record {
	for _, r := range records {
		if r != except && r.Source == source {
			return r
		}
	}
	return nil
}

func validateSourceEditState(record *Record, ms []MountInfo) error {
	if record.Enabled {
		return errors.New("请先停止备份任务并停用映射，然后更换来源")
	}
	if _, mounted := getMount(ms, record.Target); mounted {
		return errors.New("本地目标仍处于挂载状态，请先安全卸载")
	}
	return nil
}

// saveDisabledSource only changes durable metadata. It never touches the
// bind mount or target directory, and rolls back RAM state on save failure.
func saveDisabledSource(s *Store, record *Record, source string) error {
	oldSource, oldMountID, oldStatus, oldError, oldChecked :=
		record.Source, record.MountID, record.Status, record.Error, record.Checked
	record.Source = source
	record.MountID = ""
	record.Status = "disabled"
	record.Error = ""
	record.Checked = ""
	if err := persisted(s); err != nil {
		record.Source = oldSource
		record.MountID = oldMountID
		record.Status = oldStatus
		record.Error = oldError
		record.Checked = oldChecked
		return err
	}
	return nil
}

func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, "\\").Replace(s)
}
func mounts() ([]MountInfo, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMounts(string(b)), nil
}
func parseMounts(data string) []MountInfo {
	var out []MountInfo
	for _, line := range strings.Split(data, "\n") {
		halves := strings.SplitN(line, " - ", 2)
		if len(halves) != 2 {
			continue
		}
		before, after := strings.Fields(halves[0]), strings.Fields(halves[1])
		if len(before) < 6 || len(after) < 2 {
			continue
		}
		out = append(out, MountInfo{Point: unescapeMount(before[4]), Device: before[2], ID: before[0], Options: before[5], FSType: after[0], Source: unescapeMount(after[1])})
	}
	return out
}
func getMount(ms []MountInfo, p string) (MountInfo, bool) {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Point == p {
			return ms[i], true
		}
	}
	return MountInfo{}, false
}

// RemoteShare represents an actual SMB/CIFS mount root, not an arbitrary subfolder.
type RemoteShare struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Subtitle    string `json:"subtitle,omitempty"`
	LabelSource string `json:"labelSource,omitempty"`
}

// remoteSharesFromMounts is pure for reproducible tests.
func remoteSharesFromMounts(ms []MountInfo) []RemoteShare {
	roots := make(map[string]RemoteShare)
	for _, m := range ms {
		parts := pieces(m.Point)
		if len(parts) != 2 || !remotePath(m.Point) {
			continue
		}
		if m.FSType != "cifs" && m.FSType != "smb3" {
			continue
		}
		roots[m.Point] = RemoteShare{Path: m.Point, Name: shareLabelFromSource(m.Source, parts[1]), LabelSource: "smb"}
	}
	out := make([]RemoteShare, 0, len(roots))
	for _, r := range roots {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sourceLabelForRecord resolves a source against the matched live SMB roots.
// It is display-only: binding always uses the exact persisted Record.Source.
func sourceLabelForRecord(source string, shares []RemoteShare) string {
	bestLen := 0
	label := ""
	for _, share := range shares {
		if (source == share.Path || strings.HasPrefix(source, share.Path+"/")) && len(share.Path) > bestLen {
			suffix := strings.TrimPrefix(source, share.Path)
			label = share.Name + suffix
			bestLen = len(share.Path)
		}
	}
	return label
}
func remoteReady(p string, ms []MountInfo) bool {
	if !remotePath(p) {
		return true
	}
	// A remote entry must have its own mount, not just a directory within /vol02.
	best := ""
	for _, m := range ms {
		if m.Point != "/vol02" && strings.HasPrefix(m.Point, "/vol02/") &&
			(p == m.Point || strings.HasPrefix(p, m.Point+"/")) && len(m.Point) > len(best) {
			best = m.Point
		}
	}
	return best != ""
}
func persisted(s *Store) error {
	b, err := json.MarshalIndent(struct {
		Records []*Record `json:"records"`
	}{s.Records}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.file)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".mounts-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.file); err != nil {
		return err
	}
	if d, e := os.Open(dir); e == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
func load(path string) (*Store, error) {
	s := &Store{file: path, Records: []*Record{}}
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	var old struct {
		Records []*Record `json:"records"`
	}
	if e = json.Unmarshal(b, &old); e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, r := range old.Records {
		if r == nil || r.ID == "" || seen[r.ID] || !sourceAllowed(r.Source) || !localPath(filepath.Dir(r.Target), true) {
			return nil, errors.New("持久化配置包含无效记录")
		}
		seen[r.ID] = true
		if r.Mode != "ro" && r.Mode != "rw" {
			return nil, errors.New("不支持的挂载模式")
		}
		r.Status = "checking"
		r.Error = ""
		s.Records = append(s.Records, r)
	}
	return s, nil
}
func mountBind(source, target, mode string) error {
	if err := mountCall(source, target, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind 失败: %w", err)
	}
	if mode == "ro" {
		if err := mountCall("", target, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
			_ = unmountCall(target, 0)
			return fmt.Errorf("设置只读挂载失败: %w", err)
		}
	}
	return nil
}

// aliasRecoveryRoot defines the highest existing directory we trust for
// rebuilding an application's owned target alias. For fnOS volume paths,
// never create the volume or UID directory: both must already be available.
// Only records under fnOS user storage can reach reconcile in production
// (load/create paths validate them); the fallback keeps unit fixtures local.
func aliasRecoveryRoot(target string) string {
	components := pieces(target)
	if localPath(target, true) && len(components) >= 4 {
		return filepath.Join("/", components[0], components[1])
	}
	return filepath.Dir(target)
}

// ensureAliasTargetUnderRoot rebuilds a missing target path from an existing
// trusted root, one component at a time. No permissions of existing folders
// are changed and no pre-existing file, symlink, or mount is overwritten.
func ensureAliasTargetUnderRoot(r *Record, root string) error {
	target := filepath.Clean(r.Target)
	root = filepath.Clean(root)
	if !filepath.IsAbs(target) || !filepath.IsAbs(root) ||
		target == root || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return errors.New("挂载目标不在允许恢复的目录范围内")
	}
	if err := checkNoSymlink(root); err != nil {
		return fmt.Errorf("存储卷或共享目录根路径不可用，不能自动重建: %w", err)
	}
	if err := checkNoSymlink(target); err == nil {
		return nil
	}
	if !r.Owned {
		return errors.New("目标目录已消失，且不是本应用创建的目录；请手动恢复后重试")
	}

	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("目标目录不在安全的恢复范围内")
	}
	cur := root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if err := strictName(component); err != nil {
			return fmt.Errorf("待恢复目录名称异常: %w", err)
		}
		cur = filepath.Join(cur, component)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			// Mkdir creates *only* this component. Do not call MkdirAll on
			// an arbitrary path; validate each newly reached component.
			if err = os.Mkdir(cur, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("自动恢复目录 %s 失败: %w", cur, err)
			}
			fi, err = os.Lstat(cur)
		}
		if err != nil {
			return fmt.Errorf("检查目录 %s 失败: %w", cur, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("路径 %s 是文件或符号链接，拒绝覆盖", cur)
		}
	}
	if err := checkNoSymlink(target); err != nil {
		return fmt.Errorf("目录恢复后的安全校验失败: %w", err)
	}
	return nil
}

// ensureAliasTarget only re-creates paths belonging to application-owned
// mappings. It can rebuild multiple missing layers below a healthy volume
// and UID directory, but never creates a missing volume or user-storage root.
func ensureAliasTarget(r *Record) error {
	return ensureAliasTargetUnderRoot(r, aliasRecoveryRoot(r.Target))
}

func reconcile(s *Store, r *Record, ms []MountInfo) bool {
	oldSt, oldErr, oldID := r.Status, r.Error, r.MountID
	r.Checked = time.Now().Format("2006-01-02 15:04:05")
	errMsg := ""
	targetMount, found := getMount(ms, r.Target)
	switch {
	case !r.Enabled:
		r.Status = "disabled"
		if found && r.MountID != "" && targetMount.ID == r.MountID {
			if e := unmountCall(r.Target, 0); e != nil {
				r.Status = "error"
				errMsg = "已停用但旧挂载无法卸载: " + e.Error()
			}
		}
	case !remoteReady(r.Source, ms):
		r.Status = "source_offline"
		errMsg = "来源远程挂载未就绪"
		if found && r.MountID != "" && targetMount.ID == r.MountID {
			if e := unmountCall(r.Target, 0); e != nil {
				errMsg += "; 旧映射卸载失败: " + e.Error()
			}
		}
	case checkNoSymlink(r.Source) != nil:
		r.Status = "source_offline"
		errMsg = "来源目录已丢失或包含符号链接"
	case found:
		x, e1 := os.Stat(r.Source)
		y, e2 := os.Stat(r.Target)
		if e1 == nil && e2 == nil && os.SameFile(x, y) && r.MountID != "" && targetMount.ID == r.MountID {
			if r.Mode == "ro" && !strings.Contains(","+targetMount.Options+",", ",ro,") {
				r.Status = "error"
				errMsg = "目标已挂载，但只读属性不一致；请手动检查"
			} else {
				r.Status = "mounted"
			}
		} else if r.MountID != "" && r.MountID == targetMount.ID {
			// It is the exact prior mount made by this application, not another user's mount.
			if e := unmountCall(r.Target, 0); e != nil {
				r.Status = "error"
				errMsg = "旧映射仍在使用: " + e.Error()
			} else if e := mountBind(r.Source, r.Target, r.Mode); e != nil {
				r.Status = "error"
				errMsg = e.Error()
			} else {
				r.Status = "mounted"
			}
		} else {
			r.Status = "conflict"
			errMsg = "目标已由其他挂载占用，未覆盖"
		}
	default:
		if err := ensureAliasTarget(r); err != nil {
			r.Status = "error"
			errMsg = "目标路径不可用: " + err.Error()
			break
		}
		if files, err := os.ReadDir(r.Target); err != nil {
			r.Status = "error"
			errMsg = err.Error()
			break
		} else if len(files) > 0 {
			r.Status = "conflict"
			errMsg = "目标目录非空，拒绝覆盖"
			break
		}
		if err := mountBind(r.Source, r.Target, r.Mode); err != nil {
			r.Status = "error"
			errMsg = err.Error()
		} else {
			r.Status = "mounted"
		}
	}
	r.Error = errMsg
	if r.Status == "mounted" {
		if actual, ok := mounts(); ok == nil {
			if m, yes := getMount(actual, r.Target); yes {
				r.MountID = m.ID
			}
		}
	}
	return oldSt != r.Status || oldErr != r.Error || oldID != r.MountID
}
func reconcileAll(s *Store) {
	s.Lock()
	defer s.Unlock()
	ms, err := mounts()
	if err != nil {
		log.Println("mountinfo:", err)
		return
	}
	dirty := false
	for _, r := range s.Records {
		if changed := reconcile(s, r, ms); changed {
			dirty = true
			log.Printf("record %s: %s (%s)", r.ID, r.Status, r.Error)
		}
		// refresh before next record because we may have mounted this one
		ms, _ = mounts()
	}
	if dirty {
		if e := persisted(s); e != nil {
			log.Printf("failed to persist mount state: %v", e)
		}
	}
}
func response(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e string) {
	response(w, status, map[string]string{"error": e})
}
func newID() string { var b [10]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Header.Get("X-Fnos-Mount-UI") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "请求校验失败")
		return false
	}
	// The fnOS gateway forwards browser requests to this private Unix socket
	// and may rewrite Host to an internal address. Comparing Origin against
	// r.Host therefore incorrectly rejects legitimate same-origin requests.
	// A cross-origin browser cannot send our custom JSON header without a
	// CORS preflight (this app never enables CORS). Fetch Metadata additionally
	// rejects requests the browser explicitly labels cross-origin.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		fail(w, 403, "跨站请求已拒绝")
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			fail(w, 403, "非法来源")
			return false
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if e := json.NewDecoder(r.Body).Decode(v); e != nil {
		fail(w, 400, "JSON 无效或请求过大")
		return false
	}
	return true
}
func (s *Store) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, base) {
		path = "/" + strings.TrimPrefix(path, base)
	}
	// Legacy Quick Copy clients (cached inside fnOS WebView) sent /api/pause
	// and /api/resume. Route them to the merged copy engine only when using
	// the copy app's dedicated request header, never for Mount Manager calls.
	if r.Method == "POST" && (path == "/api/pause" || path == "/api/resume") &&
		r.Header.Get("X-Fnos-Quickcopy-UI") == "1" {
		if s.copier == nil {
			fail(w, 503, "复制服务未启动")
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = path
		s.copier.Serve(w, request)
		return
	}
	if strings.HasPrefix(path, "/api/copy/") {
		if s.copier == nil {
			fail(w, 503, "复制服务未启动")
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/api/" + strings.TrimPrefix(path, "/api/copy/")
		s.copier.Serve(w, request)
		return
	}
	if path == "/api/remote-shares" && r.Method == "GET" {
		ms, err := mounts()
		if err != nil {
			fail(w, 500, "无法读取当前挂载信息")
			return
		}
		shares := remoteSharesFromMounts(ms)
		enrichRemoteShares(r.Context(), shares)
		response(w, 200, map[string]any{"items": shares})
		return
	}
	if path == "/api/status" && r.Method == "GET" {
		s.Lock()
		defer s.Unlock()
		type view struct {
			*Record
			SourceLabel string `json:"sourceLabel,omitempty"`
		}
		type state struct {
			Records  []view `json:"records"`
			Root     bool   `json:"root"`
			Interval string `json:"interval"`
		}
		var shares []RemoteShare
		if ms, err := mounts(); err == nil {
			shares = remoteSharesFromMounts(ms)
			if aliases, err := readMountConfig(fnosMountConfigPath); err == nil {
				applyMountAliases(shares, aliases)
			}
		}
		views := make([]view, 0, len(s.Records))
		for _, record := range s.Records {
			views = append(views, view{record, sourceLabelForRecord(record.Source, shares)})
		}
		response(w, 200, state{views, os.Geteuid() == 0, "60 秒"})
		return
	}
	if path == "/api/create" && r.Method == "POST" {
		var in struct{ Source, Parent, Name, Mode string }
		if !readJSON(w, r, &in) {
			return
		}
		src, e := cleanAbsolute(in.Source)
		if e != nil || !sourceAllowed(src) {
			fail(w, 400, "来源仅支持远程挂载目录或飞牛本地共享目录")
			return
		}
		parent, e := cleanAbsolute(in.Parent)
		if e != nil || !localPath(parent, true) {
			fail(w, 400, "目标父目录必须属于飞牛本地用户共享目录")
			return
		}
		if e = strictName(in.Name); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if in.Mode != "ro" && in.Mode != "rw" {
			fail(w, 400, "模式必须为 ro 或 rw")
			return
		}
		target := filepath.Join(parent, in.Name)
		if overlap(src, target) {
			fail(w, 400, "来源和目标路径存在嵌套关系")
			return
		}
		if err := checkNoSymlink(src); err != nil {
			fail(w, 400, "来源路径异常: "+err.Error())
			return
		}
		if err := checkNoSymlink(parent); err != nil {
			fail(w, 400, "目标父目录异常: "+err.Error())
			return
		}
		ms, e := mounts()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if !remoteReady(src, ms) {
			fail(w, 409, "远程来源目前未挂载")
			return
		}
		s.Lock()
		defer s.Unlock()
		if used := duplicateSource(s.Records, src, nil); used != nil {
			fail(w, 409, "该来源已经映射到 "+used.Target+"，请重新选择其他来源")
			return
		}
		for _, v := range s.Records {
			if overlap(v.Target, target) || overlap(src, v.Target) || overlap(target, v.Source) {
				fail(w, 409, "与其他映射的目标或来源存在嵌套冲突")
				return
			}
		}
		if _, ok := getMount(ms, target); ok {
			fail(w, 409, "目标已被其他挂载占用")
			return
		}
		owned := false
		fi, e := os.Lstat(target)
		if errors.Is(e, os.ErrNotExist) {
			if e = os.Mkdir(target, 0755); e != nil {
				fail(w, 500, "创建目录失败: "+e.Error())
				return
			}
			owned = true
		} else if e != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			fail(w, 400, "目标必须是正常目录")
			return
		} else {
			arr, er := os.ReadDir(target)
			if er != nil || len(arr) > 0 {
				fail(w, 409, "目标目录非空，拒绝遮蔽已有文件")
				return
			}
		}
		rec := &Record{ID: newID(), Source: src, Target: target, Mode: in.Mode, Enabled: true, Owned: owned, Status: "checking"}
		s.Records = append(s.Records, rec)
		if e = persisted(s); e != nil {
			s.Records = s.Records[:len(s.Records)-1]
			if owned {
				_ = os.Remove(target)
			}
			fail(w, 500, e.Error())
			return
		}
		reconcile(s, rec, ms)
		if e := persisted(s); e != nil {
			log.Printf("persist after initial mount: %v", e)
		}
		response(w, 201, rec)
		return
	}
	// Changing the source never touches the target's files or remote SMB
	// content. A mapping must be disabled and fully unmounted first, so the
	// bind mount cannot change underneath active backup operations.
	if path == "/api/change-source" && r.Method == "POST" {
		var in struct{ ID, Source string }
		if !readJSON(w, r, &in) {
			return
		}
		newSource, err := cleanAbsolute(in.Source)
		if err != nil || !sourceAllowed(newSource) {
			fail(w, 400, "请选择有效的远程挂载或飞牛本地共享目录")
			return
		}
		if err := checkNoSymlink(newSource); err != nil {
			fail(w, 400, "新来源路径不存在或不可用: "+err.Error())
			return
		}
		ms, err := mounts()
		if err != nil {
			fail(w, 500, "无法读取当前挂载状态")
			return
		}
		if !remoteReady(newSource, ms) {
			fail(w, 409, "新来源尚未挂载")
			return
		}
		s.Lock()
		defer s.Unlock()
		var targetRecord *Record
		for _, v := range s.Records {
			if v.ID == in.ID {
				targetRecord = v
				break
			}
		}
		if targetRecord == nil {
			fail(w, 404, "映射记录不存在")
			return
		}
		if err := validateSourceEditState(targetRecord, ms); err != nil {
			fail(w, 409, err.Error())
			return
		}
		if overlap(newSource, targetRecord.Target) {
			fail(w, 400, "新来源与目标目录存在嵌套冲突")
			return
		}
		if used := duplicateSource(s.Records, newSource, targetRecord); used != nil {
			fail(w, 409, "该来源已映射到 "+used.Target+"，请选另一条来源")
			return
		}
		for _, other := range s.Records {
			if other == targetRecord {
				continue
			}
			if overlap(newSource, other.Target) || overlap(other.Source, targetRecord.Target) {
				fail(w, 409, "新来源与现有映射路径冲突")
				return
			}
		}
		if targetRecord.Source == newSource {
			response(w, 200, targetRecord)
			return
		}
		if err := saveDisabledSource(s, targetRecord, newSource); err != nil {
			fail(w, 500, "更新来源失败: "+err.Error())
			return
		}
		response(w, 200, targetRecord)
		return
	}
	if path == "/api/rename" && r.Method == "POST" {
		var in struct{ ID, Name string }
		if !readJSON(w, r, &in) {
			return
		}
		if e := strictName(in.Name); e != nil {
			fail(w, 400, e.Error())
			return
		}
		s.Lock()
		defer s.Unlock()
		var v *Record
		for _, r := range s.Records {
			if r.ID == in.ID {
				v = r
				break
			}
		}
		if v == nil {
			fail(w, 404, "记录不存在")
			return
		}
		oldTarget, oldOwned, oldID := v.Target, v.Owned, v.MountID
		newTarget := filepath.Join(filepath.Dir(oldTarget), in.Name)
		if newTarget == oldTarget {
			response(w, 200, v)
			return
		}
		for _, r := range s.Records {
			if r != v && (overlap(r.Target, newTarget) || overlap(r.Source, newTarget)) {
				fail(w, 409, "新目录与其他映射冲突")
				return
			}
		}
		if _, e := os.Lstat(newTarget); !errors.Is(e, os.ErrNotExist) {
			fail(w, 409, "新目标已存在或不可访问")
			return
		}
		if e := checkNoSymlink(filepath.Dir(oldTarget)); e != nil {
			fail(w, 400, e.Error())
			return
		}
		ms, e := mounts()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if m, ok := getMount(ms, oldTarget); ok {
			a, ea := os.Stat(v.Source)
			b, eb := os.Stat(oldTarget)
			if oldID == "" || oldID != m.ID || (ea == nil && eb == nil && !os.SameFile(a, b)) {
				fail(w, 409, "原目录不是本应用创建的挂载")
				return
			}
			if e = unmountCall(oldTarget, 0); e != nil {
				fail(w, 409, "原挂载正在使用，请先停止备份任务: "+e.Error())
				return
			}
		}
		if oldOwned {
			e = os.Rename(oldTarget, newTarget)
		} else {
			e = os.Mkdir(newTarget, 0755)
		}
		if e != nil {
			ms, _ = mounts()
			if v.Enabled {
				reconcile(s, v, ms)
			}
			fail(w, 500, "创建新目标目录失败: "+e.Error())
			return
		}
		v.Target = newTarget
		v.Owned = true
		v.MountID = ""
		if e = persisted(s); e != nil {
			v.Target = oldTarget
			v.Owned = oldOwned
			v.MountID = oldID
			if oldOwned {
				_ = os.Rename(newTarget, oldTarget)
			} else {
				_ = os.Remove(newTarget)
			}
			ms, _ = mounts()
			if v.Enabled {
				reconcile(s, v, ms)
			}
			fail(w, 500, "保存记录失败: "+e.Error())
			return
		}
		ms, _ = mounts()
		reconcile(s, v, ms)
		if e = persisted(s); e != nil {
			log.Printf("rename state persist: %v", e)
		}
		response(w, 200, v)
		return
	}
	if (path == "/api/remove" || path == "/api/toggle" || path == "/api/retry") && r.Method == "POST" {
		var in struct {
			ID      string
			Enabled *bool
		}
		if !readJSON(w, r, &in) {
			return
		}
		s.Lock()
		defer s.Unlock()
		idx := -1
		for i, v := range s.Records {
			if v.ID == in.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			fail(w, 404, "记录不存在")
			return
		}
		v := s.Records[idx]
		ms, e := mounts()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		currentMount, mounted := getMount(ms, v.Target)
		if path == "/api/remove" || (path == "/api/toggle" && in.Enabled != nil && !*in.Enabled) {
			if mounted {
				// Source may be offline. A persisted mount ID is required
				// to avoid unmounting another application's unrelated mount.
				if v.MountID == "" || v.MountID != currentMount.ID {
					fail(w, 409, "挂载归属不匹配，拒绝卸载其他程序的挂载")
					return
				}
				if e = unmountCall(v.Target, 0); e != nil {
					fail(w, 409, "目录正在使用或卸载失败: "+e.Error())
					return
				}
			}
		}
		if path == "/api/remove" {
			s.Records = append(s.Records[:idx], s.Records[idx+1:]...)
			if e = persisted(s); e != nil {
				s.Records = append(s.Records[:idx], append([]*Record{v}, s.Records[idx:]...)...)
				fail(w, 500, e.Error())
				return
			}
			if v.Owned {
				_ = os.Remove(v.Target)
			}
			response(w, 200, map[string]bool{"ok": true})
			return
		}
		if path == "/api/toggle" {
			if in.Enabled == nil {
				fail(w, 400, "缺少 enabled")
				return
			}
			v.Enabled = *in.Enabled
			if e = persisted(s); e != nil {
				fail(w, 500, e.Error())
				return
			}
		}
		ms, _ = mounts()
		reconcile(s, v, ms)
		if e := persisted(s); e != nil {
			log.Printf("persist after reconcile: %v", e)
		}
		response(w, 200, v)
		return
	}
	if (path == "/" || path == "/index.html") && r.Method == "GET" {
		b, _ := assets.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
		return
	}
	if (path == "/app.js" || path == "/style.css" || path == "/fnos-sdk.js" || path == "/auth-callback.html" || path == "/icon.png" || path == "/copy.html" || path == "/copy.css" || path == "/copy.js" || path == "/copy-auth-callback.html" || path == "/copy-v206.html" || path == "/copy-v206.js" || path == "/copy-v206.css") && r.Method == "GET" {
		b, e := assets.ReadFile("web" + path)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(path, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else if strings.HasSuffix(path, ".html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else if strings.HasSuffix(path, ".png") {
			w.Header().Set("Content-Type", "image/png")
		} else {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
		return
	}
	http.NotFound(w, r)
}
func cleanup(s *Store) error {
	s.Lock()
	defer s.Unlock()
	ms, e := mounts()
	if e != nil {
		return e
	}
	var failed []string
	for _, r := range s.Records {
		if m, ok := getMount(ms, r.Target); ok {
			if r.MountID == "" || r.MountID != m.ID {
				failed = append(failed, r.Target+"(挂载归属不匹配)")
				continue
			}
			if e := unmountCall(r.Target, 0); e != nil {
				failed = append(failed, r.Target+"("+e.Error()+")")
				continue
			}
		}
		if r.Owned {
			_ = os.Remove(r.Target)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("尚有挂载无法安全清理：%s", strings.Join(failed, ", "))
	}
	return nil
}
func main() {
	socket := flag.String("socket", "", "unix socket path")
	state := flag.String("state", "", "private state JSON path")
	group := flag.String("socket-group", "", "gateway socket group (optional)")
	copyState := flag.String("copy-state", "", "copy task state, defaults to sibling of mount state")
	rcloneBin := flag.String("rclone", "", "embedded rclone binary")
	cleanupOnly := flag.Bool("cleanup", false, "safely unmount app-managed aliases for uninstall")
	flag.Parse()
	if *state == "" {
		log.Fatal("missing --state")
	}
	s, e := load(*state)
	if e != nil {
		log.Fatal("load config: ", e)
	}
	if os.Geteuid() != 0 {
		log.Fatal("CAP_SYS_ADMIN/root required for bind mounts")
	}
	if *cleanupOnly {
		if e = cleanup(s); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *socket == "" {
		log.Fatal("missing --socket")
	}
	if *copyState == "" {
		*copyState = filepath.Join(filepath.Dir(*state), "copies.json")
	}
	if *rcloneBin == "" {
		exe, e := os.Executable()
		if e != nil {
			log.Fatal(e)
		}
		*rcloneBin = filepath.Join(filepath.Dir(exe), "rclone")
	}
	copier, e := copyengine.NewManager(*copyState, *rcloneBin)
	if e != nil {
		log.Fatal("load copy service: ", e)
	}
	s.copier = copier
	// Upgrade in place from Mount Manager: preserve mounts.json. If the
	// standalone Quick Copy app still exists, import its completed jobs.
	if n, err := copier.ImportCompletedHistory("/var/apps/fnos-quick-copy/var/private/tasks.json"); err != nil {
		log.Printf("standalone completed copy history import: %v", err)
	} else if n > 0 {
		log.Printf("imported %d completed copy jobs", n)
	}
	if e = os.MkdirAll(filepath.Dir(*socket), 0755); e != nil {
		log.Fatal(e)
	}
	_ = os.Remove(*socket)
	ln, e := net.Listen("unix", *socket)
	if e != nil {
		log.Fatal(e)
	}
	defer os.Remove(*socket)
	if *group != "" {
		g, e := user.LookupGroup(*group)
		if e != nil {
			log.Fatal("lookup gateway group: ", e)
		}
		gid, e := strconv.Atoi(g.Gid)
		if e != nil {
			log.Fatal(e)
		}
		if e = os.Chown(*socket, 0, gid); e != nil {
			log.Fatal(e)
		}
	}
	if e = os.Chmod(*socket, 0660); e != nil {
		log.Fatal(e)
	}
	reconcileAll(s)
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	go func() {
		for range tick.C {
			reconcileAll(s)
		}
	}()
	server := &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-signals; s.copier.Stop(); _ = server.Close() }()
	log.Printf("NAS Toolbox serving via %s, %d mount records", *socket, len(s.Records))
	e = server.Serve(ln)
	if e != nil && !errors.Is(e, http.ErrServerClosed) && !errors.Is(e, io.EOF) {
		log.Fatal(e)
	}
}
