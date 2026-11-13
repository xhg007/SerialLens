package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 会话持久化（PRD §3.6）：每会话一个 JSONL，一行一事件，追加写入，崩溃不丢。
//
// 事件类型：meta / context_snapshot / message / tool_call / tool_result /
// user_action / error。大内容（脚本源码、HEX）以引用存放（指回 drafts/）。
const (
	EvSessionMeta      = "meta"
	EvSessionContext   = "context_snapshot"
	EvSessionMessage   = "message"
	EvSessionToolCall  = "tool_call"
	EvSessionToolRes   = "tool_result"
	EvSessionUserAct   = "user_action"
	EvSessionError     = "error"
	sessionMaxBytes    = 5 << 20
	sessionKeepRecent  = 20
	conversationsDirNm = "conversations"
)

// SessionEvent JSONL 里的一行。
type SessionEvent struct {
	TS       string         `json:"ts"`
	Type     string         `json:"type"`
	Role     string         `json:"role,omitempty"`
	Content  string         `json:"content,omitempty"`
	ToolName string         `json:"name,omitempty"`
	CallID   string         `json:"call_id,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	OK       *bool          `json:"ok,omitempty"`
	Result   any            `json:"result,omitempty"`
	Duration int64          `json:"duration_ms,omitempty"`
	Model    string         `json:"model,omitempty"`
	Title    string         `json:"title,omitempty"`
	Action   string         `json:"action,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// Session 一次对话的落盘句柄（并发安全）。
type Session struct {
	mu      sync.Mutex
	dir     string
	id      string
	path    string
	created bool
	bytes   int64
}

// SessionSummary 会话列表项。
type SessionSummary struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Model   string `json:"model"`
	Updated int64  `json:"updated"`
	Size    int64  `json:"size"`
	Turns   int    `json:"turns"`
}

// NewSession 在 <project>/conversations/ 下创建（或续写）会话文件。
// id 为空则按时间生成 sess_<ts>。
func NewSession(projectDir, id string) (*Session, error) {
	if projectDir == "" {
		return nil, fmt.Errorf("no project open")
	}
	if id == "" {
		id = "sess_" + time.Now().Format("20060102_150405")
	}
	if err := safeName(id + ".jsonl"); err != nil {
		return nil, err
	}
	dir := filepath.Join(projectDir, conversationsDirNm)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".jsonl")
	_, statErr := os.Stat(path)
	return &Session{dir: dir, id: id, path: path, created: statErr != nil}, nil
}

// ID 会话 id。
func (s *Session) ID() string { return s.id }

// Path 会话文件绝对路径。
func (s *Session) Path() string { return s.path }

// Append 追加一条事件（写失败不打断对话：日志丢了也比请求挂了强）。
func (s *Session) Append(ev SessionEvent) {
	if s == nil {
		return
	}
	if ev.TS == "" {
		ev.TS = time.Now().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bytes > 0 && s.bytes+int64(len(raw)) > sessionMaxBytes {
		s.trimLocked()
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	n, _ := f.Write(append(raw, '\n'))
	s.bytes += int64(n)
	s.created = false
}

// trimLocked 超过 5MB 时保留 meta 与最近 20 条（PRD §3.6）。
func (s *Session) trimLocked() {
	events, err := readEvents(s.path)
	if err != nil || len(events) <= sessionKeepRecent {
		return
	}
	keep := make([]SessionEvent, 0, sessionKeepRecent+2)
	for _, e := range events {
		if e.Type == EvSessionMeta || e.Type == EvSessionContext {
			keep = append(keep, e)
		}
	}
	keep = append(keep, events[len(events)-sessionKeepRecent:]...)

	var b strings.Builder
	for _, e := range keep {
		raw, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
	s.bytes = int64(b.Len())
}

// LoadEvents 读取会话全部事件（前端恢复历史用）。
func LoadEvents(path string) ([]SessionEvent, error) {
	if err := safeName(filepath.Base(path)); err != nil {
		return nil, err
	}
	return readEvents(path)
}

func readEvents(path string) ([]SessionEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []SessionEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e SessionEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // 单行坏掉不影响其余历史
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// ListSessions 列出工程内会话（按更新时间倒序）。
func ListSessions(projectDir string) ([]SessionSummary, error) {
	if projectDir == "" {
		return nil, fmt.Errorf("no project open")
	}
	dir := filepath.Join(projectDir, conversationsDirNm)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]SessionSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sum := SessionSummary{ID: strings.TrimSuffix(e.Name(), ".jsonl"), Updated: info.ModTime().UnixMilli(), Size: info.Size()}
		if evs, err := readEvents(filepath.Join(dir, e.Name())); err == nil {
			for _, ev := range evs {
				switch ev.Type {
				case EvSessionMeta:
					sum.Title, sum.Model = ev.Title, ev.Model
				case EvSessionMessage:
					if ev.Role == "user" {
						sum.Turns++
						if sum.Title == "" {
							sum.Title = firstLine(ev.Content, 40)
						}
					}
				}
			}
		}
		if sum.Title == "" {
			sum.Title = sum.ID
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
