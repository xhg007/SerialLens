// Package scripts 管理工程 scripts/ 目录（PRD §F4）：
// 文件列表/读写/新建/复制/重命名/删除、活动脚本切换、外部 IDE 修改的 fsnotify 热重载。
//
// 与 parser 的关系：本包只管文件与「谁该被加载」，真正的编译+切换由壳层通过
// Loader 回调（pipe.LoadScriptSource）完成，避免 scripts → parser → lua 的耦合。
package scripts

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"seriallens/internal/events"
	"seriallens/internal/lua"
)

// Debounce 外部编辑合并窗口（PRD §F4 明确 100ms）。
const Debounce = 100 * time.Millisecond

// 名称约束：工程可跨机迁移，脚本名必须是 scripts/ 下的纯文件名。
const maxNameLen = 96

var (
	// ErrNoProject 未打开工程。
	ErrNoProject = errors.New("scripts: no project open")
	// ErrBadName 非法脚本名。
	ErrBadName = errors.New("scripts: invalid script name")
	// ErrNotFound 脚本不存在。
	ErrNotFound = errors.New("scripts: script not found")
	// ErrExists 目标脚本已存在。
	ErrExists = errors.New("scripts: script already exists")
	// ErrActiveScript 不能删除正在运行的脚本。
	ErrActiveScript = errors.New("scripts: cannot delete the active script")
)

// File 脚本文件条目。
type File struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Active   bool      `json:"active"`
	HasError bool      `json:"has_error"` // 最近一次编译失败
}

// SaveResult 保存结果：诊断 + 耗时（PRD 验收「修改脚本 < 100ms 生效」）。
type SaveResult struct {
	OK         bool            `json:"ok"`
	ElapsedMS  int64           `json:"elapsed_ms"`
	CompileMS  int64           `json:"compile_ms"`
	LoadMS     int64           `json:"load_ms"`
	Diagnostic *lua.Diagnostic `json:"diagnostic,omitempty"`
}

// Loader 把脚本内容装载进解析管线；返回编译错误时旧脚本继续运行。
type Loader func(name string, src []byte) error

// Service 脚本目录服务。
type Service struct {
	mu     sync.Mutex
	dir    string // <project>/scripts
	active string
	emit   events.Emitter
	loader Loader

	digests map[string]string // 自写内容摘要（区分外部编辑）
	errs    map[string]bool   // 编译失败的脚本

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// New 创建服务；emit 可为 nil。
func New(emit events.Emitter) *Service {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &Service{
		emit:    emit,
		digests: map[string]string{},
		errs:    map[string]bool{},
	}
}

// SetEmitter 由壳层在启动后注入真实事件桥。
func (s *Service) SetEmitter(emit events.Emitter) {
	if emit == nil {
		return
	}
	s.mu.Lock()
	s.emit = emit
	s.mu.Unlock()
}

// SetLoader 注入解析管线装载器（工程打开前注入即可）。
func (s *Service) SetLoader(l Loader) {
	s.mu.Lock()
	s.loader = l
	s.mu.Unlock()
}

// Open 切换到工程的 scripts/ 目录并开始监听外部修改。
func (s *Service) Open(projectDir, active string) error {
	dir := filepath.Join(projectDir, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.stopWatch()
	s.mu.Lock()
	s.dir = dir
	s.active = active
	s.digests = map[string]string{}
	s.errs = map[string]bool{}
	s.mu.Unlock()
	s.startWatch(dir)
	s.emitList()
	return nil
}

// Close 停止监听并清空状态（工程切换/关闭）。
func (s *Service) Close() {
	s.stopWatch()
	s.mu.Lock()
	s.dir, s.active = "", ""
	s.digests, s.errs = map[string]string{}, map[string]bool{}
	s.mu.Unlock()
}

// Active 返回当前活动脚本名。
func (s *Service) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// SetActive 切换活动脚本并立即装载。
func (s *Service) SetActive(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	src, err := s.Read(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.active = name
	loader := s.loader
	s.mu.Unlock()

	if loader != nil {
		if err := loader(name, []byte(src)); err != nil {
			s.markError(name, true)
			s.emitError(name, "ui", err)
			return err
		}
	}
	s.markError(name, false)
	s.emitReloaded(name, "ui", 0, 0)
	s.emitList()
	return nil
}

// List 按名称排序列出脚本（活动脚本置顶）。
func (s *Service) List() ([]File, error) {
	s.mu.Lock()
	dir, active := s.dir, s.active
	s.mu.Unlock()
	if dir == "" {
		return nil, ErrNoProject
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".lua") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		f := File{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime(), Active: e.Name() == active}
		f.HasError = s.hasError(e.Name())
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Read 读取脚本内容。
func (s *Service) Read(name string) (string, error) {
	path, err := s.path(name)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return string(trimBOM(raw)), nil
}

// Save 写盘 + 编译校验 + （活动脚本时）热装载。
//
// 语义：**内容一定落盘**（不能吞掉用户的编辑），编译失败只报诊断、
// 旧脚本继续解析——半途换上一个坏脚本会让串口数据直接断流。
func (s *Service) Save(name, content string) (SaveResult, error) {
	path, err := s.path(name)
	if err != nil {
		return SaveResult{}, err
	}
	start := time.Now()

	src := []byte(content)
	sum := digest(src)
	if err := writeAtomic(path, src); err != nil {
		return SaveResult{}, err
	}

	s.mu.Lock()
	dir := s.dir
	loader := s.loader
	active := s.active
	s.digests[name] = sum
	s.mu.Unlock()
	if dir == "" {
		return SaveResult{}, ErrNoProject
	}

	res := SaveResult{OK: true}
	var loadErr error
	if name == active && loader != nil {
		t := time.Now()
		loadErr = loader(name, src)
		res.LoadMS = time.Since(t).Milliseconds()
	} else {
		t := time.Now()
		loadErr = lua.Compile(name, src)
		res.CompileMS = time.Since(t).Milliseconds()
	}
	if loadErr != nil {
		d := lua.DiagnosticFromError(name, loadErr)
		res.OK = false
		res.Diagnostic = &d
		s.markError(name, true)
		s.emitError(name, "ui", loadErr)
	} else {
		s.markError(name, false)
		if name == active {
			s.emitReloaded(name, "ui", res.CompileMS, res.LoadMS)
		}
	}
	// 内容已落盘（哪怕编译失败）：通知编辑器换掉旧内容，否则用户看到的还是上一版
	s.emitChanged(name, content, "ui")
	res.ElapsedMS = time.Since(start).Milliseconds()
	return res, nil
}

// Create 新建空脚本（已存在则报错）。
func (s *Service) Create(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	}
	return writeAtomic(path, []byte(defaultTemplate(name)))
}

// Duplicate 复制脚本。
func (s *Service) Duplicate(from, to string) error {
	src, err := s.Read(from)
	if err != nil {
		return err
	}
	path, err := s.path(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	}
	return writeAtomic(path, []byte(src))
}

// Rename 重命名脚本；调用方负责在改名的是活动脚本时同步 project.json。
func (s *Service) Rename(from, to string) error {
	if err := ValidateName(to); err != nil {
		return err
	}
	oldPath, err := s.path(from)
	if err != nil {
		return err
	}
	newPath, err := s.path(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(oldPath); err != nil {
		return ErrNotFound
	}
	if _, err := os.Stat(newPath); err == nil {
		return ErrExists
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	s.mu.Lock()
	if s.active == from {
		s.active = to
	}
	if d, ok := s.digests[from]; ok {
		delete(s.digests, from)
		s.digests[to] = d
	}
	if e, ok := s.errs[from]; ok {
		delete(s.errs, from)
		s.errs[to] = e
	}
	s.mu.Unlock()
	s.emitList()
	return nil
}

// Remove 删除脚本（活动脚本拒绝删除：删掉就没有解析器了）。
func (s *Service) Remove(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	active := s.active
	delete(s.digests, name)
	delete(s.errs, name)
	s.mu.Unlock()
	if name == active {
		return ErrActiveScript
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	s.emitList()
	return nil
}

// ValidateName 校验脚本名（纯文件名 + .lua）。
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return ErrBadName
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return ErrBadName
	}
	if strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, ".") {
		return ErrBadName
	}
	if !strings.EqualFold(filepath.Ext(name), ".lua") {
		return ErrBadName
	}
	return nil
}

func (s *Service) path(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()
	if dir == "" {
		return "", ErrNoProject
	}
	return filepath.Join(dir, name), nil
}

func (s *Service) hasError(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs[name]
}

func (s *Service) markError(name string, bad bool) {
	s.mu.Lock()
	if bad {
		s.errs[name] = true
	} else {
		delete(s.errs, name)
	}
	s.mu.Unlock()
}

func (s *Service) emitList() {
	files, err := s.List()
	if err != nil {
		return
	}
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	emit(events.EvScriptList, map[string]any{"files": files})
}

func (s *Service) emitReloaded(name, source string, compileMS, loadMS int64) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	emit(events.EvScriptReloaded, map[string]any{
		"name": name, "source": source, "compile_ms": compileMS, "load_ms": loadMS,
	})
}

func (s *Service) emitError(name, source string, err error) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	d := lua.DiagnosticFromError(name, err)
	emit(events.EvScriptError, map[string]any{
		"script": name, "error": d.Message, "line": d.Line, "column": d.Column, "source": source,
	})
}

// emitChanged 广播脚本内容变化。
//
// Save 也要发：写盘的一方不一定是编辑器（AI 应用草稿、CLI、将来别的面板），
// 编辑器只能靠这个事件把 Monaco 里的旧内容换掉。
func (s *Service) emitChanged(name, content, source string) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	emit(events.EvScriptChanged, map[string]any{"name": name, "content": content, "source": source})
}

// ---------- 文件系统 ----------

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sl-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // rename 成功后为 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func digest(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func trimBOM(b []byte) []byte {
	for _, bom := range [][]byte{{0xEF, 0xBB, 0xBF}} {
		if len(b) >= len(bom) {
			match := true
			for i := range bom {
				if b[i] != bom[i] {
					match = false
					break
				}
			}
			if match {
				return b[len(bom):]
			}
		}
	}
	return b
}

func defaultTemplate(name string) string {
	return `-- ` + name + `
-- 保存后立即生效（Ctrl+S），无需重启。

function on_frame(raw)
    -- raw 是一帧的原始字节（string）
    -- 返回 table：字段名 -> 值；数值字段可被图表引用
    return { len = #raw }
end
`
}

// ---------- fsnotify ----------

func (s *Service) startWatch(dir string) {
	s.stopWatch()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		s.emit(events.EvScriptError, map[string]any{"script": "", "error": "watch scripts: " + err.Error(), "line": 0})
		return
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		s.emit(events.EvScriptError, map[string]any{"script": "", "error": "watch scripts: " + err.Error(), "line": 0})
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.mu.Lock()
	s.watcher, s.stopCh, s.doneCh = w, stop, done
	s.mu.Unlock()
	go s.watchLoop(w, stop, done, dir)
}

func (s *Service) watchLoop(w *fsnotify.Watcher, stop, done chan struct{}, dir string) {
	defer close(done)
	var timer *time.Timer
	var timerC <-chan time.Time
	pending := map[string]bool{}

	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-stop:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			base := filepath.Base(ev.Name)
			if !strings.EqualFold(filepath.Ext(base), ".lua") {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			pending[base] = true
			if timer == nil {
				timer = time.NewTimer(Debounce)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(Debounce)
			}
		case <-timerC:
			timer, timerC = nil, nil
			names := make([]string, 0, len(pending))
			for n := range pending {
				names = append(names, n)
			}
			pending = map[string]bool{}
			for _, n := range names {
				s.handleExternalChange(dir, n)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			s.emit(events.EvScriptError, map[string]any{"script": "", "error": "watch: " + err.Error(), "line": 0})
		}
	}
}

func (s *Service) handleExternalChange(dir, name string) {
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		// 被删除/改名：让前端刷新列表
		s.mu.Lock()
		emit := s.emit
		delete(s.digests, name)
		delete(s.errs, name)
		s.mu.Unlock()
		emit(events.EvScriptChanged, map[string]any{"name": name, "content": "", "source": "external", "removed": true})
		s.emitList()
		return
	}
	src := trimBOM(raw)
	sum := digest(src)

	s.mu.Lock()
	selfWritten := s.digests[name] == sum
	active := s.active
	loader := s.loader
	emit := s.emit
	s.mu.Unlock()
	if selfWritten {
		return // 自己刚写的，UI 已经同步过
	}

	emit(events.EvScriptChanged, map[string]any{"name": name, "content": string(src), "source": "external"})
	if name != active || loader == nil {
		return
	}

	start := time.Now()
	if err := loader(name, src); err != nil {
		s.markError(name, true)
		s.emitError(name, "external", err)
		s.emitList()
		return
	}
	loadMS := time.Since(start).Milliseconds()
	s.markError(name, false)
	s.emitReloaded(name, "external", 0, loadMS)
	s.emitList()
}

func (s *Service) stopWatch() {
	s.mu.Lock()
	w, stop, done := s.watcher, s.stopCh, s.doneCh
	s.watcher, s.stopCh, s.doneCh = nil, nil, nil
	s.mu.Unlock()
	if w == nil {
		return
	}
	close(stop)
	_ = w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
