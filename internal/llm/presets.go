// Package llm 提供 OpenAI 兼容的 LLM 接入层（PRD §4.10 / §6.1）。
//
// 设计取舍：不引入第三方 SDK。主流供应商（DeepSeek / 通义千问 / Kimi / OpenAI /
// Ollama）都提供 `/chat/completions` 兼容端点，差异集中在 base_url、model 名与
// 少量扩展字段（reasoning_content、stream_options）。手写 ~300 行 HTTP+SSE 即可
// 完整覆盖，同时避免 SDK 对多厂商私有字段的收紧，也不给安装包增加体积。
package llm

// Provider 一个可选的模型供应商预设（UI 下拉 + 一键填 base_url/model）。
type Provider struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	BaseURL string   `json:"base_url"`
	Models  []string `json:"models"`
	KeyURL  string   `json:"key_url,omitempty"`
	DocURL  string   `json:"doc_url,omitempty"`
	Note    string   `json:"note,omitempty"`
	// NeedsKey 为 false 时（本地 Ollama）不强制要求 API Key。
	NeedsKey bool `json:"needs_key"`
}

// Providers 内置预设。模型列表是「常用推荐」而非白名单——用户始终可以
// 手填任意模型名（新模型上线速度远快于客户端发版）。
func Providers() []Provider {
	return []Provider{
		{
			ID:       "deepseek",
			Name:     "DeepSeek",
			BaseURL:  "https://api.deepseek.com/v1",
			Models:   []string{"deepseek-chat", "deepseek-reasoner"},
			KeyURL:   "https://platform.deepseek.com/api_keys",
			DocURL:   "https://api-docs.deepseek.com/",
			Note:     "deepseek-chat 通用；deepseek-reasoner 会先输出思维链（reasoning_content）",
			NeedsKey: true,
		},
		{
			ID:       "qwen",
			Name:     "通义千问（DashScope 兼容模式）",
			BaseURL:  "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Models:   []string{"qwen-plus", "qwen-max", "qwen-turbo", "qwen3-coder-plus"},
			KeyURL:   "https://bailian.console.aliyun.com/?apiKey=1",
			DocURL:   "https://help.aliyun.com/zh/model-studio/compatibility-of-openai-with-dashscope",
			Note:     "用阿里云百炼的 API Key；兼容模式端点必须带 /compatible-mode/v1",
			NeedsKey: true,
		},
		{
			ID:       "kimi",
			Name:     "Kimi（Moonshot）",
			BaseURL:  "https://api.moonshot.cn/v1",
			Models:   []string{"kimi-k2-0905-preview", "kimi-latest", "moonshot-v1-32k"},
			KeyURL:   "https://platform.moonshot.cn/console/api-keys",
			DocURL:   "https://platform.moonshot.cn/docs/api/chat",
			Note:     "kimi-k2 系列支持长上下文；moonshot-v1-* 按上下文长度分档",
			NeedsKey: true,
		},
		{
			ID:       "openai",
			Name:     "OpenAI",
			BaseURL:  "https://api.openai.com/v1",
			Models:   []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini", "o4-mini"},
			KeyURL:   "https://platform.openai.com/api-keys",
			DocURL:   "https://platform.openai.com/docs/api-reference/chat",
			NeedsKey: true,
		},
		{
			ID:       "ollama",
			Name:     "Ollama（本地，无需 Key）",
			BaseURL:  "http://127.0.0.1:11434/v1",
			Models:   []string{"qwen2.5-coder:7b", "qwen3:8b", "llama3.1:8b", "deepseek-r1:7b"},
			DocURL:   "https://github.com/ollama/ollama/blob/main/docs/openai.md",
			Note:     "完全离线；上下文 8K 以下时建议用 coder 小模型",
			NeedsKey: false,
		},
		{
			ID:       "custom",
			Name:     "自定义（任意 OpenAI 兼容端点）",
			BaseURL:  "",
			Models:   []string{},
			Note:     "填 base_url（需含 /v1）、模型名；兼容 vLLM / LM Studio / OneAPI 等",
			NeedsKey: true,
		},
	}
}

// ProviderByID 查找预设；未知 ID 返回 custom。
func ProviderByID(id string) Provider {
	for _, p := range Providers() {
		if p.ID == id {
			return p
		}
	}
	return Providers()[len(Providers())-1]
}
