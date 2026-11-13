# M5 阶段二验收记录：工具调用 + 双任务隔离 + 会话落盘 + eval

日期：2026-11-13
PRD 条目：§3.6（JSONL 会话）/ §4.3（双任务）/ §4.4（12 工具）/ §4.8（安全边界）/ §4.9（eval）
范围：**阶段二** = 工具层 + 工具调用循环 + 确认关卡 + 会话持久化 + 评测骨架。
不在本次范围：图表任务的真实回归、字段含义标注表（侧栏）、`user_action` 事件、Monaco DiffEditor（当前用文本 diff）。

## 分层

```
internal/agent/            纯逻辑，无 Wails 依赖，可单测、可被 eval 复用
├── agent.go               Host 接口 / Task / ConfirmRequest
├── tools.go               12 工具注册表 + 任务白名单 + 执行器（5s 超时）
├── runner.go              流式 + tool_calls 回喂循环（最多 8 轮）
├── session.go             JSONL 会话（追加写 / 5MB 截断 / 列表）
├── samples.go             .seriallens/samples/ 与 data/*.srec 读取
├── prompt.go              任务 A/B 的 system prompt（每次现拼）
└── eval/                  评测骨架（用例集 + 打分 + 自修复重试）

internal/app/agent_host.go Host 的 Wails 侧实现（工程/脚本/图表/沙箱）
internal/app/ai_api.go     AIAPI：事件桥、确认桥、会话生命周期
frontend/src/stores/ai.ts  ai:tool_call / ai:tool_result / ai:confirm 状态机
frontend/src/panels/AIPanel.tsx  工具时间线 + 确认卡片 + 会话下拉 + 任务开关
```

## 验收 1：真实供应商端到端（DeepSeek，任务 A）

输入：「用样本 modbus1.hex 生成一个 4 通道 int16 大端的解码脚本（帧头 AA55，第3字节长度）。先编译再试跑，通过后存草稿并请我确认。」

| 序 | 工具 | 结果 |
|---|---|---|
| 1 | `list_recent_samples` | ok，1 条样本 |
| 2 | `read_sample_frame` | ok，11 字节 HEX |
| 3 | `read_charts_config` | ok |
| 4 | `list_builtin_api` | ok |
| 5 | `compile_lua` | **失败**：首次输出被 max_tokens 截断（模型自行察觉并重写） |
| 6 | `compile_lua` | ok |
| 7 | `run_lua_on_samples` | ok，ch1=10 ch2=300 ch3=0 ch4=10000 |
| 8 | `save_script_draft` | ok，写 `.seriallens/drafts/main_4ch_int16be.lua` |
| 9 | `request_user_confirm` | **阻塞 27s** 等用户在 Diff 卡片点「应用并写入脚本」 |

点确认后：前端调 `EditorAPI.SaveScript(active, content)` → 编译 → 热装载；随后用户手动试跑该脚本，
样本 `AA 55 08 00 0A 01 2C 00 00 27 10` 得到 `ch1=10, ch2=300, ch3=0, ch4=10000`（与 AI 自己试跑一致）。

整轮：7 次工具调用、8121 tokens、约 40s + 人工确认 27s。

## 验收 2：会话落盘

`<工程>/conversations/sess_20261113_1942xx.jsonl` 实际 11 行：

```
meta           {"title":"用样本 modbus1.hex 生成…","model":"deepseek-flash"}
message        user
tool_call      list_recent_samples / read_sample_frame / read_charts_config / list_builtin_api
tool_result    （每条带 ok / duration_ms / result）
tool_call      compile_lua（第一次失败，带 diagnostic）
message        assistant（中间态文本）
tool_result    request_user_confirm（ok=true, duration_ms≈27s）
```

`ListAISessions` 能正确显示标题、轮次、模型；`LoadAISession` 恢复历史消息并把 conv 重新灌进上下文。

## 验收 3：eval（PRD §4.9 指标）

```
go run ./cmd/evalagent -cases internal/agent/eval/cases.jsonl -max-attempts 3
```

| 用例 | 类别 | 结果 |
|---|---|---|
| aa55_len_4ch_int16be | 帧头+长度 | PASS 1 轮 |
| float32_be_temp | IEEE754 | PASS 1 轮 |
| le_uint32_seq | 小端陷阱 | PASS 1 轮 |
| nested_multi | 嵌套多字段（有符号+0.1 精度） | PASS 1 轮 |
| varlen_frame | 可变长帧（detect_frame） | PASS 1 轮 |
| pure_inference | 纯样本推测 | PASS 2 轮 |

**汇总：首试通过率 83.3%（5/6），3 轮内通过率 100%（6/6），平均 9.4K tokens / 25.4s**
PRD 门槛：首试 ≥50% ✓，3 轮内 ≥85% ✓。

评测过程中修掉两个真问题：
1. 重试时回传空 assistant 消息 → DeepSeek 报 `Invalid assistant message`（`BuildMessages` 现在丢弃空 assistant）
2. `nested_multi` 用例样本少一个字节（用例自身的 bug，模型表现其实是对的）

## 安全边界自查

| 检查 | 结论 |
|---|---|
| 是否存在 `write_file` / `send_serial` / `delete` 工具 | 不存在（`TestRegistryTaskIsolation` 断言） |
| 模型硬编造工具名 | `Execute` 二次校验注册表 + 任务白名单，直接返回错误 |
| 能否写正式脚本 | 不能，只能写 `.seriallens/drafts/`；应用需用户在卡片点确认 |
| 沙箱能否发串口 | `RunSandbox` 的 Send sink 被替换为报错 |
| 沙箱死循环 | `lua.Options.Timeout` = 5s，`SetContext` 强制中断 |
| 路径穿越 | 草稿名/样本名只允许纯文件名（拒绝 `/`、`\`、`..`） |
| 数据外发 | 首次发送弹**系统原生** Question 对话框（`application.Dialog.Question()`），确认后写入 `notice_ack`；本地端点（Ollama/localhost）不提示 |

## 已知差距

1. **任务 B（图表）**：白名单与工具已就位，但没有跑真实供应商回归；`charts:updated` 联动已在 M3 验证
2. **Diff 呈现**：确认卡片是文本 unified diff（红绿行），不是 Monaco DiffEditor（3.3MB chunk 只在脚本页签加载，避免 AI 页签背这个包袱）
3. **`user_action` 事件**：应用脚本后还没写进 JSONL（阶段三补；不影响回溯 tool 事件）
4. **字段含义标注表**（§4.4 侧栏）未实现
5. **eval 只有 6 条**：需要扩到 20+ 条并接入 CI 门槛（当前手动跑）
6. 上下文只回放 user/assistant 文本，历史轮次的 tool_call 结果不重放（模型需要时会重新读）
