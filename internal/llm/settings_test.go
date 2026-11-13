package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	st := NewStore(t.TempDir())
	s, err := st.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Provider != "deepseek" || s.BaseURL == "" || s.Model == "" {
		t.Fatalf("defaults not applied: %+v", s)
	}
	if !s.Enabled || s.TimeoutSec != 60 {
		t.Fatalf("default flags: %+v", s)
	}
}

func TestSaveLoadRoundTripAndKeyNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	in := DefaultSettings()
	in.Model = "deepseek-reasoner"
	in.APIKey = "sk-super-secret-1234"
	in.Temperature = 0.7
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-super-secret") {
		t.Fatal("api key must not be stored in plaintext")
	}

	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != in.APIKey {
		t.Fatalf("key roundtrip = %q", got.APIKey)
	}
	if got.Model != "deepseek-reasoner" || got.Temperature != 0.7 {
		t.Fatalf("settings roundtrip = %+v", got)
	}
	if !got.Ready() {
		t.Fatal("should be ready with key")
	}
}

// 「只改模型不重填 Key」是最常见操作，不能把已有 Key 抹掉。
func TestSaveWithoutKeyKeepsStoredKey(t *testing.T) {
	st := NewStore(t.TempDir())
	in := DefaultSettings()
	in.APIKey = "sk-keep-me"
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}

	next := DefaultSettings()
	next.Model = "deepseek-chat"
	if err := st.Save(next); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load()
	if got.APIKey != "sk-keep-me" {
		t.Fatalf("key lost after saving without key: %q", got.APIKey)
	}
}

func TestClearKey(t *testing.T) {
	st := NewStore(t.TempDir())
	in := DefaultSettings()
	in.APIKey = "sk-x"
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearKey(); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load()
	if got.APIKey != "" {
		t.Fatalf("key not cleared: %q", got.APIKey)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-from-env")
	t.Setenv(EnvModel, "env-model")
	t.Setenv(EnvBaseURL, "http://127.0.0.1:9999/v1")

	st := NewStore(t.TempDir())
	s, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.APIKey != "sk-from-env" || !s.KeyFromEnv {
		t.Fatalf("env key not applied: %+v", s)
	}
	if s.Model != "env-model" || s.BaseURL != "http://127.0.0.1:9999/v1" {
		t.Fatalf("env model/base not applied: %+v", s)
	}
}

func TestLoadToleratesBOMAndBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	body := []byte("\ufeff" + `{"provider":"kimi","model":"kimi-latest"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewStore(dir)
	s, err := st.Load()
	if err != nil {
		t.Fatalf("BOM should be tolerated: %v", err)
	}
	if s.Provider != "kimi" || s.BaseURL == "" {
		t.Fatalf("preset not resolved from provider: %+v", s)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = st.Load()
	if err == nil {
		t.Fatal("broken json should report an error")
	}
	if s.Provider != "deepseek" {
		t.Fatalf("broken config should fall back to defaults, got %+v", s)
	}
}

func TestResolvedClampsAndPresetFallback(t *testing.T) {
	s := Settings{Provider: "kimi", Temperature: 9, MaxTokens: -5, TimeoutSec: 99999}
	r := s.Resolved()
	if r.BaseURL != ProviderByID("kimi").BaseURL {
		t.Fatalf("base url not from preset: %q", r.BaseURL)
	}
	if r.Model != ProviderByID("kimi").Models[0] {
		t.Fatalf("model not from preset: %q", r.Model)
	}
	if r.Temperature != 2 || r.MaxTokens != 2048 || r.TimeoutSec != 600 {
		t.Fatalf("clamp failed: %+v", r)
	}
}

func TestMaskedHidesKey(t *testing.T) {
	s := DefaultSettings()
	s.APIKey = "sk-abcdefgh1234"
	m := s.Masked()
	if m["has_key"] != true {
		t.Fatal("has_key should be true")
	}
	if hint, _ := m["key_hint"].(string); strings.Contains(hint, "abcdefgh") {
		t.Fatalf("hint leaks key: %q", hint)
	}
	if _, ok := m["api_key"]; ok {
		t.Fatal("masked view must not contain api_key")
	}
}

// Ollama 这类本地端点不需要 Key，不该被判成「未配置」。
func TestReadyWithoutKeyForLocalProvider(t *testing.T) {
	s := Settings{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen3:8b"}
	if !s.Ready() {
		t.Fatal("local provider should be ready without key")
	}
}
