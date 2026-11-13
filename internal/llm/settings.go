package llm

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileName 用户级设置文件（与 recent_projects.json 同目录，PRD §4.10）。
const FileName = "ai.json"

// 环境变量覆盖：CI / 临时试用时不希望 Key 落盘，也不希望它出现在 UI 里。
const (
	EnvAPIKey   = "SERIALENS_AI_API_KEY"
	EnvBaseURL  = "SERIALENS_AI_BASE_URL"
	EnvModel    = "SERIALENS_AI_MODEL"
	EnvProvider = "SERIALENS_AI_PROVIDER"
)

// Settings 一份完整的 LLM 接入配置。
type Settings struct {
	Provider    string  `json:"provider"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	TimeoutSec  int     `json:"timeout_sec"`
	Enabled     bool    `json:"enabled"`
	// SystemExtra 追加到 system prompt 末尾的工程偏好（例如「优先用小端」）。
	SystemExtra string `json:"system_extra,omitempty"`

	// APIKey 只存在于内存：读盘时解密、写盘时加密，绝不进 Wails 返回值。
	APIKey string `json:"-"`
	// KeyFromEnv 表示 Key 来自环境变量（UI 上只允许「已从环境变量读取」）。
	KeyFromEnv bool `json:"-"`
}

// DefaultSettings 默认走 DeepSeek：国内可直连、兼容性最好、便宜。
func DefaultSettings() Settings {
	p := ProviderByID("deepseek")
	return Settings{
		Provider:    p.ID,
		BaseURL:     p.BaseURL,
		Model:       p.Models[0],
		Temperature: 0.3,
		MaxTokens:   2048,
		TimeoutSec:  60,
		Enabled:     true,
	}
}

// Resolved 补齐缺省值并夹紧范围；空 BaseURL/Model 回落到预设。
// 用户只改 provider 也能直接可用，不需要手抄 base_url。
func (s Settings) Resolved() Settings {
	out := s
	if out.Provider == "" {
		out.Provider = "deepseek"
	}
	p := ProviderByID(out.Provider)
	if strings.TrimSpace(out.BaseURL) == "" {
		out.BaseURL = p.BaseURL
	}
	if strings.TrimSpace(out.Model) == "" && len(p.Models) > 0 {
		out.Model = p.Models[0]
	}
	out.BaseURL = strings.TrimRight(strings.TrimSpace(out.BaseURL), "/")
	out.Model = strings.TrimSpace(out.Model)

	if out.Temperature < 0 {
		out.Temperature = 0
	}
	if out.Temperature > 2 {
		out.Temperature = 2
	}
	if out.MaxTokens <= 0 {
		out.MaxTokens = 2048
	}
	if out.MaxTokens > 32768 {
		out.MaxTokens = 32768
	}
	if out.TimeoutSec <= 0 {
		out.TimeoutSec = 60
	}
	if out.TimeoutSec > 600 {
		out.TimeoutSec = 600
	}
	return out
}

// Timeout 调用超时。
func (s Settings) Timeout() time.Duration {
	return time.Duration(s.Resolved().TimeoutSec) * time.Second
}

// Ready 是否可以发起请求（本地 Ollama 不要求 Key）。
func (s Settings) Ready() bool {
	r := s.Resolved()
	if r.BaseURL == "" || r.Model == "" {
		return false
	}
	if ProviderByID(r.Provider).NeedsKey && r.APIKey == "" {
		return false
	}
	return true
}

// Masked 给 UI 的安全视图：只有「有没有 Key」和末 4 位。
func (s Settings) Masked() map[string]any {
	r := s.Resolved()
	hint := ""
	if r.APIKey != "" {
		if len(r.APIKey) <= 4 {
			hint = "****"
		} else {
			hint = "****" + r.APIKey[len(r.APIKey)-4:]
		}
	}
	return map[string]any{
		"provider":     r.Provider,
		"base_url":     r.BaseURL,
		"model":        r.Model,
		"temperature":  r.Temperature,
		"max_tokens":   r.MaxTokens,
		"timeout_sec":  r.TimeoutSec,
		"enabled":      r.Enabled,
		"system_extra": r.SystemExtra,
		"has_key":      r.APIKey != "",
		"key_hint":     hint,
		"key_from_env": s.KeyFromEnv,
		"ready":        r.Ready(),
	}
}

// Store 设置文件读写（并发安全，原子替换）。
type Store struct {
	home string
	mu   sync.Mutex
}

// NewStore home 为空时用 ~/.seriallens（与最近工程列表同目录）。
func NewStore(home string) *Store {
	if home == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = os.TempDir() // 退化路径：至少能跑，不写 CWD
		}
		home = filepath.Join(homeDir, ".seriallens")
	}
	return &Store{home: home}
}

// Dir 返回设置目录（测试与文档用）。
func (s *Store) Dir() string { return s.home }

func (s *Store) filePath() string { return filepath.Join(s.home, FileName) }

// Load 读取配置；文件不存在或损坏都返回可用默认值 + 说明性 error
// （调用方只记录日志，不能让一个坏 JSON 把 AI 功能整体锁死）。
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.filePath())
	if err != nil {
		if os.IsNotExist(err) {
			return defaultWithEnv(nil), nil
		}
		return defaultWithEnv(nil), err
	}
	var doc struct {
		Provider    string  `json:"provider"`
		BaseURL     string  `json:"base_url"`
		Model       string  `json:"model"`
		KeyEnc      string  `json:"api_key_enc"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		TimeoutSec  int     `json:"timeout_sec"`
		Enabled     *bool   `json:"enabled"`
		SystemExtra string  `json:"system_extra"`
	}
	if err := json.Unmarshal(trimBOM(raw), &doc); err != nil {
		return defaultWithEnv(nil), fmt.Errorf("parse %s: %w", FileName, err)
	}
	out := Settings{
		Provider:    doc.Provider,
		BaseURL:     doc.BaseURL,
		Model:       doc.Model,
		Temperature: doc.Temperature,
		MaxTokens:   doc.MaxTokens,
		TimeoutSec:  doc.TimeoutSec,
		SystemExtra: doc.SystemExtra,
		Enabled:     true,
	}
	if doc.Enabled != nil {
		out.Enabled = *doc.Enabled
	}
	if doc.KeyEnc != "" {
		if key, derr := deobfuscate(doc.KeyEnc); derr == nil {
			out.APIKey = key
		}
	}
	r := defaultWithEnv(&out)
	return r, nil
}

// Save 写盘（0600）。空 APIKey 表示保留原有 Key（除非更新时显式清空）。
func (s *Store) Save(in Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := in.Resolved()
	keyEnc := ""
	if r.APIKey != "" {
		keyEnc = obfuscate(r.APIKey)
	} else if old := s.storedKeyLocked(); old != "" {
		keyEnc = old // 空 Key 表示「不改动已有 Key」，避免一次保存把密钥抹掉
	}
	doc := map[string]any{
		"version":      1,
		"provider":     r.Provider,
		"base_url":     r.BaseURL,
		"model":        r.Model,
		"temperature":  r.Temperature,
		"max_tokens":   r.MaxTokens,
		"timeout_sec":  r.TimeoutSec,
		"enabled":      r.Enabled,
		"system_extra": r.SystemExtra,
	}
	if keyEnc != "" {
		doc["api_key_enc"] = keyEnc
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.home, ".ai-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.filePath())
}

// storedKeyLocked 读取磁盘上已保存的 Key（已加密串），供「只改模型、不重填 Key」用。
func (s *Store) storedKeyLocked() string {
	raw, err := os.ReadFile(s.filePath())
	if err != nil {
		return ""
	}
	var doc struct {
		KeyEnc string `json:"api_key_enc"`
	}
	if err := json.Unmarshal(trimBOM(raw), &doc); err != nil {
		return ""
	}
	return doc.KeyEnc
}

// ClearKey 删除已保存的 Key（UI「清除密钥」）。
func (s *Store) ClearKey() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.filePath())
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(trimBOM(raw), &doc); err != nil {
		return nil
	}
	delete(doc, "api_key_enc")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), out, 0o600)
}

// defaultWithEnv 在给定配置上叠加环境变量（env 优先级最高，便于 CI 免落盘）。
func defaultWithEnv(in *Settings) Settings {
	out := DefaultSettings()
	if in != nil {
		out = *in
	}
	if v := os.Getenv(EnvProvider); v != "" {
		out.Provider = v
	}
	if v := os.Getenv(EnvBaseURL); v != "" {
		out.BaseURL = v
	}
	if v := os.Getenv(EnvModel); v != "" {
		out.Model = v
	}
	if v := os.Getenv(EnvAPIKey); v != "" {
		out.APIKey = v
		out.KeyFromEnv = true
	}
	return out.Resolved()
}

// ---------- Key 本机混淆 ----------
//
// 说明：这不是「加密」。PRD §5 要求 key 加密存储，真正的做法是
// Keychain(macOS) / DPAPI(Windows) / Secret Service(Linux)，需要平台专用 API。
// 当前实现用「主机+用户名派生密钥 + XOR + base64」，能挡住配置文件被随手拷走
// 或误传仓库，但挡不住在同一台机器上运行的程序。已在验收文档与 PRD 中标注，
// 后续可替换为平台 keychain（接口只有 obfuscate/deobfuscate 两个函数）。

func machineKey() []byte {
	host, _ := os.Hostname()
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	sum := sha256.Sum256([]byte("seriallens-ai-v1|" + host + "|" + name))
	return sum[:]
}

func obfuscate(plain string) string {
	if plain == "" {
		return ""
	}
	k := machineKey()
	buf := make([]byte, len(plain))
	for i := 0; i < len(plain); i++ {
		buf[i] = plain[i] ^ k[i%len(k)]
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func deobfuscate(enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	k := machineKey()
	for i := range raw {
		raw[i] ^= k[i%len(k)]
	}
	return string(raw), nil
}

func trimBOM(b []byte) []byte {
	return []byte(strings.TrimPrefix(string(b), "\ufeff"))
}
