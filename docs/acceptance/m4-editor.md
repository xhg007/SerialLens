# M4 验收记录：Monaco 脚本编辑器 + 保存即生效

日期：2026-11-13
分支：`feat/m4-editor` → `master`
PRD 条目：§8「M4 编辑器+热重载：Monaco + fsnotify + 保存即生效 + 错误行号回显；验收 = 修改脚本 < 100ms 生效」

## 环境

| 项 | 值 |
| --- | --- |
| 编辑器 | monaco-editor 0.57.0（`editor.api` + 7 个 contrib + lua register，React.lazy 独立 chunk） |
| 后端 | `internal/scripts`（fsnotify 100ms 防抖）+ `EditorAPI`（保存/试运行/文件管理） |
| 串口 | com0com **COM11 → COM12**，`loopcheck -rate 6400`（64B 帧 × 100 帧/秒） |
| 脚本 | `detect_frame` 返回 64，每帧 `emit_chart` 10 通道 |

测量在 **wails3 dev 真实应用窗口** 里进行（MCP 驱动 UI，非单元测试）。

## 验收 1：修改脚本 < 100ms 生效

操作：通过编辑器保存链路（store `content` → `SaveScript`，与用户 Ctrl+S 完全一致）连续 3 次改写脚本（每次新增一个字段 `m4okN`），从发起到 `frame:parsed` 出现新字段的端到端耗时：

| 轮次 | Go 侧 保存→装载 | 端到端（含下一帧到达+渲染） | < 100ms |
| --- | --- | --- | --- |
| 1 | **5 ms**（装载 1ms） | **30 ms** | ✓ |
| 2 | **3 ms**（装载 1ms） | **81 ms** | ✓ |
| 3 | **4 ms**（装载 1ms） | **83 ms** | ✓ |

端到端耗时的大头是等下一帧（100fps → 批间隔 ~33ms）+ React 渲染；编译/装载本身是毫秒级。

## 验收 2：编译错误行号回显 + 旧脚本不断流

操作：保存语法错误脚本（`local x = +`）：

| 检查 | 结果 |
| --- | --- |
| SaveScript 返回 | `ok=false`，`diagnostic.line=3`，消息 `line:3(column:15) near '+': syntax error` |
| 编辑器 | 底部红条「第 3 行」+ Monaco marker（红波浪线） |
| 旧脚本继续解析 | **是**：0.8s 内新解析 80 帧（=100fps），字段仍来自旧脚本 |
| 用户编辑 | **已落盘**（内容先写盘，校验失败不回滚文件） |

## 验收 3：外部 IDE 修改热同步

| 场景 | 结果 |
| --- | --- |
| 本地无未保存修改时外部改文件 | 编辑器内容直接跟随（含新注释标记），状态仍「已保存」，解析管线同步重载 |
| 本地有未保存修改时外部改文件 | 弹「已被外部修改」条：`采用磁盘版本` / `保留我的修改`，本地编辑未被覆盖 |
| 点击「采用磁盘版本」 | 内容切换为磁盘版本，横幅消失 |
| 自己保存 | 不触发外部重载（内容摘要判定，loader 只跑一次） |

## 验收 4：试运行（不污染在线脚本）

| 检查 | 结果 |
| --- | --- |
| 编辑器内容 + hex `2A` → `struct_uint8(raw,0)` | `fields.test=42`，图表点 `t=42`，耗时 µs 级 |
| 在线脚本 | 未受影响（`parser.Stats().script` 不变） |
| 试运行里调 `send_bytes` | **被拦截**：返回诊断 `send_bytes is disabled during test run`，串口无写入 |
| 试运行源码有语法错误 | 带行号返回诊断，不影响在线脚本 |

## 验收 5：脚本管理

| 操作 | 结果 |
| --- | --- |
| 新建/复制/重命名/删除 | 全部可用；非法名（路径分隔符/非 .lua/点前缀）在 Go 侧拒绝 |
| 重命名**活动**脚本 | `project.json` 的 `active_script` 同步更新，解析管线按新名装载 |
| 删除**活动**脚本 | 拒绝（`ErrActiveScript`） |
| 切换活动脚本 | 写回 project.json + 立即装载 |

## 自动化测试

```
go test ./internal/... ./cmd/...        全绿（新增 internal/scripts 9 个用例、app 集成 6 个）
go test -race ./internal/... ./cmd/...  全绿（fsnotify 监听 + loader 并发）
npm run build                           通过
  主包 586.83 kB / gzip 199.35 kB（与 M3 持平，Monaco 未进主包）
  EditorPanel chunk 3,276.73 kB / gzip 837.77 kB（懒加载）
  editor.worker 303.77 kB
```

## 过程中发现并修正的取舍

1. **`window.confirm` 在 WebView2 里不可靠** —— 文件切换的脏检查改为「先保存再切换」，失败时文件也已落盘。
2. **整包 monaco 太大** —— 只引 `editor.api` + 实际用到的 contrib；不引则补全/悬停/查找等 widget 无法工作（editor.api 只有核心，widget 按需注册）。
3. **monaco 0.57 的包结构变了** —— `basic-languages/lua` 目录已不存在，语言定义移到 `esm/vs/languages/definitions/lua/register.js`，需按新路径 + exports map 引入。
4. **诊断消息前缀** —— gopher-lua 错误带 `lua: compile main.lua:` 内部前缀，展示前裁剪；行号解析覆盖 `line:N(column:M)` / `name:N:` / `chunk:N:M:` 三种格式（`TestParseLineColVariants`）。
5. **运行期错误也要有行号** —— `script:error` 事件从 `lua.DiagnosticFromError` 提取（此前 `line: 0`），AI 自修复（M5）与编辑器都依赖它。

## 遗留

1. **charts 拖拽布局**：与编辑器联动（M5 AI Diff 预览）一起做。
2. **脚本内 goto 定义 / 引用**：单文件场景收益低，未做。
3. **编辑器分栏宽度/开关状态未持久化**：重启回到默认；如用户反馈再加 localStorage。
