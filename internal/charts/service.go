package charts

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"seriallens/internal/events"
)

// 外部编辑的合并窗口：编辑器保存往往是「写 tmp → rename」多次事件，
// 150ms 内的事件合并成一次重载。
const reloadDebounce = 150 * time.Millisecond

// Service 管理当前工程的图表配置与外部编辑热同步。
type Service struct {
	mu   sync.Mutex
	dir  string
	cfg  *Config
	emit events.Emitter

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
	doneCh  chan struct{}

	// lastSelfDigest 是我们最近一次写入文件的摘要：文件事件到达时若内容一致，
	// 说明是自己写的，直接跳过（用内容比对而非时间窗，避免吃掉紧邻的真实外部编辑）。
	lastSelfDigest atomic.Value // string
}

// NewService 创建服务；emit 可为 nil（测试/无 UI 场景）。
func NewService(emit events.Emitter) *Service {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &Service{emit: emit}
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

// Load 加载工程图表配置并启动外部编辑监听。
// charts.json 不存在时自动生成默认配置（首启可用，用户随后可手改）。
func (s *Service) Load(dir string) error {
	cfg, existed, err := Load(dir)
	if err != nil {
		// 非法配置不落盘、不切换：保留旧配置并向 UI 报错。
		s.emit(events.EvChartsError, map[string]any{"message": err.Error()})
		return err
	}
	if !existed {
		sum, saveErr := saveWithDigest(dir, cfg)
		if saveErr != nil {
			s.emit(events.EvChartsError, map[string]any{"message": saveErr.Error()})
			return saveErr
		}
		s.lastSelfDigest.Store(sum)
	}

	s.mu.Lock()
	s.dir = dir
	s.cfg = cfg
	s.mu.Unlock()

	s.emitUpdated("project")
	s.startWatch(dir)
	return nil
}

// Close 停止监听并清空当前配置（工程切换/关闭）。
func (s *Service) Close() {
	s.stopWatch()
	s.mu.Lock()
	s.dir = ""
	s.cfg = nil
	s.mu.Unlock()
}

// Config 返回当前配置副本。
func (s *Service) Config() *Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SaveConfig 由 UI（拖拽布局/AI）提交新配置：校验通过才落盘并广播。
func (s *Service) SaveConfig(cfg *Config) error {
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()
	if dir == "" {
		return ErrNoProject
	}
	sum, err := saveWithDigest(dir, cfg)
	if err != nil {
		s.emit(events.EvChartsError, map[string]any{"message": err.Error()})
		return err
	}
	s.lastSelfDigest.Store(sum)
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	s.emitUpdated("ui")
	return nil
}

// Reload 强制重新读取磁盘（UI 手动刷新）。
func (s *Service) Reload() error {
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()
	if dir == "" {
		return ErrNoProject
	}
	cfg, _, err := Load(dir)
	if err != nil {
		s.emit(events.EvChartsError, map[string]any{"message": err.Error()})
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	s.emitUpdated("external")
	return nil
}

func (s *Service) emitUpdated(source string) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg == nil {
		return
	}
	s.emit(events.EvChartsUpdated, map[string]any{
		"config": toMap(cfg),
		"source": source,
	})
}

// isSelfWritten 判断磁盘当前内容是否正是我们最后一次写入的内容。
func (s *Service) isSelfWritten(dir string) bool {
	want, _ := s.lastSelfDigest.Load().(string)
	if want == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return false
	}
	return digest(raw) == want
}

// startWatch 监听工程目录（不是文件本身）：Save 走 rename 替换，
// 直接 watch 文件会在第一次保存后失效。
func (s *Service) startWatch(dir string) {
	s.stopWatch()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		s.emit(events.EvChartsError, map[string]any{"message": "watch charts.json: " + err.Error()})
		return
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		s.emit(events.EvChartsError, map[string]any{"message": "watch charts.json: " + err.Error()})
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
			if ev.Name != "" && baseName(ev.Name) != FileName {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			// 合并编辑器连续事件
			if timer == nil {
				timer = time.NewTimer(reloadDebounce)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(reloadDebounce)
			}
		case <-timerC:
			timer, timerC = nil, nil
			if s.isSelfWritten(dir) {
				continue // 自己写的，UI 已经在 emit 里更新过
			}
			_ = s.Reload()
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			s.emit(events.EvChartsError, map[string]any{"message": "watch: " + err.Error()})
		}
	}
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

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// toMap 把配置转成 map 供 Wails 事件序列化（跨端只允许基础类型）。
func toMap(cfg *Config) map[string]any {
	widgets := make([]any, 0, len(cfg.Widgets))
	for _, w := range cfg.Widgets {
		channels := any("auto")
		if !w.Channels.IsAuto() {
			items := make([]any, 0, len(w.Channels.Items))
			for _, ch := range w.Channels.Items {
				m := map[string]any{"name": ch.Name}
				if ch.Color != "" {
					m["color"] = ch.Color
				}
				if ch.Unit != "" {
					m["unit"] = ch.Unit
				}
				if len(ch.Range) == 2 {
					m["range"] = []float64{ch.Range[0], ch.Range[1]}
				}
				if ch.Digits != nil {
					m["display_digits"] = *ch.Digits
				}
				if ch.AlarmHigh != nil {
					m["alarm_high"] = *ch.AlarmHigh
				}
				if ch.AlarmLow != nil {
					m["alarm_low"] = *ch.AlarmLow
				}
				if ch.OnValue != nil {
					m["on_value"] = *ch.OnValue
				}
				items = append(items, m)
			}
			channels = items
		}
		m := map[string]any{
			"id":       w.ID,
			"type":     w.Type,
			"title":    w.Title,
			"position": map[string]any{"x": w.Position.X, "y": w.Position.Y, "w": w.Position.W, "h": w.Position.H},
			"channels": channels,
		}
		if w.Type == TypeLine {
			m["window_seconds"] = w.WindowSeconds
			m["y_axis_mode"] = w.YAxisMode
		}
		widgets = append(widgets, m)
	}
	return map[string]any{
		"version": cfg.Version,
		"layout":  map[string]any{"grid": map[string]any{"cols": cfg.Layout.Grid.Cols, "row_height": cfg.Layout.Grid.RowHeight}},
		"widgets": widgets,
	}
}
