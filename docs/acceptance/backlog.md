# 遗留问题清单（Backlog）

> 2026-11-14 由 6 份验收文档（m0~m5）的「遗留 / 已知差距」条目汇总核对而来。
> 每完成一项请就地打勾并注明提交号；新遗留随验收补录到对应分组。

## M6（下一里程碑，随录制回放/打包一起做）

- [ ] **终端虚拟滚动**：从 M1 顺延两次（M1→M3→现在），现仍为「保留 5000 行 + DOM 1200 行」裁剪方案。`TerminalPanel.tsx` 注释待更新。极端满额时偶发 100ms 级长帧。
- [ ] **LTTB 降采样**：窗口内 >5000 点仍全量交给 uPlot；长窗口（600s）+ 100Hz 必需。明确约定与 F7 录制回放（M6）一起做。
- [ ] **脚本模板落盘**：Modbus 示例只存在于测试里，M6「工程模板」要把它落成模板的 `scripts/main.lua` 预置内容（m2 遗留 ④）。
- [ ] **com0com Win11 实机验证**：官方 Win7 签名包在 Win11 高版本可能装不上；打包验收前补真实环境验证（m0-m1 遗留 ④）。
- [ ] **突发缓冲真实硬件复测**：M2 用 com0com 回环压测过，USB-Serial 实机未测（m0-m1 遗留 ②）。

## 阶段二（AI / 布局联动）

- [ ] **charts 拖拽布局**：三处遗留（m3 ①、m4 ①、m5-llm ⑥）统一在阶段二做。`SaveChartsConfig` + 校验已就绪，只差 UI 拖拽层；与 AI Diff 预览联动。
- [ ] **真平台 keychain**：`internal/llm/settings.go` 当前是「主机+用户名派生 XOR 混淆，不是加密」；替换为 Keychain(macOS) / DPAPI(Windows) / Secret Service(Linux)，接口只有 obfuscate/deobfuscate 两函数，改动面小（m5-llm ⑤）。
- [ ] **eval 扩量 + CI 门槛**：现 6 条手动跑，目标 20+ 条并接 CI（m5-agent ⑤）。
- [ ] **任务 B 真实供应商回归**：白名单与工具就位，但未跑真实供应商（需 API Key）（m5-agent ①）。
- [ ] **tool_call 历史重放**：上下文恢复只回放 user/assistant 文本，历史轮 tool_call 结果不重放（m5-agent ⑥）。
- [ ] **字段含义标注表**：PRD §4.4 侧栏未实现（m5-agent ④）。
- [ ] **`user_action` 全链路回归**：写入链路已补（`5a72cef`，含单测），真实 AI 运行 → 确认卡 → JSONL 落盘的端到端在阶段二跑一次（m5-agent ③）。

## 有意不做（有裁决，不是欠账）

| 项 | 理由 |
| --- | --- |
| Monaco DiffEditor（3.3MB chunk） | 确认卡片用文本 unified diff 足够，AI 页签不背编辑器包体 |
| 图表多 y 轴 | PRD 未要求；`unit` 不同的通道目前建议分面板 |
| ECharts 瘦身（gauge 自绘） | 主包 583KB 桌面端可接受 |
| 脚本 goto 定义/引用 | 单文件场景收益低 |
| 编辑器分栏状态持久化 | 等 user 反馈再加 localStorage |
| `math.random` 种子 API | 不提供，避免 AI 脚本引入不可复现行为 |

## 验收注意事项（非任务，环境相关）

- **com0com 无对端读会阻塞**：虚拟口写缓冲满后 `port.Write` 永久阻塞（2026-11-14 dev 实测）。录制回放 / 发送压测必须有对端在读。
