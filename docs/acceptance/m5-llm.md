# M5 阶段一验收记录：LLM 接入（DeepSeek / Qwen / Kimi / OpenAI 兼容）

日期：2026-11-13
分支：`feat/m5-llm` → `master`
PRD 条目：§4.10（Agent 配置项）+ §8 M5 阶段一「LLM 接入 + 多轮对话」
本阶段范围：**能连通、能配置、能多轮流式对话、能停**。双任务状态机 / 12 工具 / Diff 确认 / eval 在阶段二。

## 供应商支持

| 预设 | Base URL | 推荐模型 | 备注 |
| --- | --- | --- | --- |
| DeepSeek | `https://api.deepseek.com/v1` | deepseek-chat / deepseek-reasoner | reasoner 有思维链（reasoning_content），前端可折叠查看 |
| 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | qwen-plus / qwen-max / qwen-turbo / qwen3-coder-plus | 必须用兼容模式端点 |
| Kimi | `https://api.moonshot.cn/v1` | kimi-k2-0905-preview / kimi-latest / moonshot-v1-32k | |
| OpenAI | `https://api.openai.com/v1` | gpt-4o-mini / gpt-4o / … | |
| Ollama | `http://127.0.0.1:11434/v1` | qwen2.5-coder:7b 等 | 本地免 Key，完全离线 |
| 自定义 | 任意 OpenAI 兼容端点（vLLM / LM Studio / OneAPI…） | 手填 | 需含 `/v1` |

模型名不是白名单——手填任意值都允许（新模型上线快于客户端发版）。

## 验收 1：连通性测试（零 token）

| 操作 | 结果 |
| --- | --- |
| 指向本地 mock 端点，点「测试连接」 | `连接成功（模型列表）· 13ms · 可用模型 2 个`（走 `GET /models`，不消耗 token） |
| 端点无 /models | 自动退化为一次 `max_tokens=8` 短对话（`via=chat`） |
| Key 错误（401） | 中文提示「API Key 无效或无权限…」，不再花 token 重试 |

## 验收 2：流式多轮对话（本地 mock，真实 HTTP/SSE 链路）

环境：`go run ./cmd/mockllm -addr 127.0.0.1:11435 -delay 15ms`，真实 Wails 应用窗口 + MCP 驱动。

| 检查 | 结果 |
| --- | --- |
| 增量到达 | 内容长度 75 次变化（~16 字/60ms 合批），**非一次性弹出**；首段 ~250ms |
| 代码块渲染 | ```lua 块带语言标签 + 「复制」按钮；回复末尾显示 `170 tokens · 6121ms` |
| 上下文注入 | 第 1 轮请求 `messages=2`（system 含当前脚本 + API 清单）；第 2 轮 `messages=4`（system+user+assistant+user），mock 服务日志确认 |
| 停止 | 生成中点「停止」→ `已停止` 徽标、部分内容（91 字）保留进历史、mock 端收到 `client canceled` |
| 并发保护 | 生成中再发消息被拒绝；结束后可继续 |
| 设置持久化 | Key 落盘为混淆串（磁盘文件不含 `sk-` 明文）；UI 只回 `has_key/key_hint`，**永远不回明文** |

## 自动化测试

```
go test ./internal/... ./cmd/...        全绿（新增 internal/llm 15 个用例、app AI 集成 8 个）
go test -race ./internal/... ./cmd/...  全绿（流式回调 + 取消 + 事件桥并发）
npm run build                            通过
  主包 587.81 kB / gzip 199.53 kB（与 M4 持平）
  AIPanel chunk 14.01 kB（懒加载）
```

覆盖点（节选）：
- SSE 分片聚合：content / reasoning_content / tool_calls（乱序 index 重排）/ usage 尾帧
- `stream_options` 400 自动去参重试；ctx 取消立刻返回
- 设置：保存/加载往返、Key 不落明文、空 Key 不抹掉已有 Key、ClearKey、环境变量覆盖、BOM 与坏 JSON 兜底、数值夹紧
- AIAPI：多轮历史、生成中拒绝、取消保留部分内容、脱敏视图、未就绪拒绝、上下文注入（当前脚本 + system_extra）、FriendlyError 映射

## 安全边界（与 PRD 的差距，明确标注）

| 项 | PRD 要求 | 当前实现 | 差距与计划 |
| --- | --- | --- | --- |
| Key 存储 | 加密存储（Keychain/DPAPI） | 主机+用户名派生密钥 XOR+base64 本机混淆，文件 0600 | 能挡「配置文件被拷走/误传仓库」，**挡不住同机同权限程序**；接口只有 obfuscate/deobfuscate，后续接平台 keychain 无需改调用方 |
| 数据外发 | 首次启用告知 | 对话输入即视为同意；system prompt 只注入当前脚本与 API 清单 | 阶段二加一次性告知弹窗 |
| 仅本地模式 | `agent.local_mode` | 未启用 AI 时面板整体禁用；Ollama 预设即本地模式 | 无需改动 |

## 无 Key 使用说明

```
go run ./cmd/mockllm -addr 127.0.0.1:11435
```
应用里选「自定义」→ Base URL `http://127.0.0.1:11435/v1` → Key 随意填。
真实供应商：选预设填 Key 即可；也可用 `SERIALENS_AI_API_KEY` 环境变量（不落盘）。

## 遗留（阶段二）

1. **工具调用**：协议已就位（tool_calls 分片聚合 + tool role 消息），待接 12 工具与双任务状态机
2. **JSONL 会话持久化**（PRD §4.6：会话随工程存 `.seriallens/sessions/`）
3. **Diff 确认 UI**（request_user_confirm）与脚本草稿流
4. **eval 骨架**（`internal/agent/eval/`，PRD §4.9 要求先建 eval 再写 Agent——阶段二开工前补）
5. Key 真加密（平台 keychain）
6. charts 联动与拖拽布局（M3/M4 遗留，统一在阶段二做）
