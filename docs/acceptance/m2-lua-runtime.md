# M2 验收记录：Lua Runtime + 解析管线

日期：2026-11-13
分支：`feat/m2-lua-runtime` → `master`
PRD 条目：§8「M2 Lua 引擎：LuaRuntime + 池 + 沙箱 + 全量内置 API + frame:parsed；验收 = Modbus 示例脚本解析正确 / `-race` 下 100 goroutine 并发压测通过 / 1Mbps 持续 10 分钟 0 崩溃」

## 环境

| 项 | 值 |
| --- | --- |
| Go | 1.26.0（amd64） |
| Lua 实现 | `github.com/yuin/gopher-lua v1.1.1`（纯 Go） |
| `-race` 工具链 | MSYS2 `C:\tools\msys64\mingw64\bin\gcc.exe` 16.2.0，`CGO_ENABLED=1` |
| 串口硬件 | 无（本里程碑验收不依赖真实波形，仅引擎/管线） |

`-race` 复现命令：

```powershell
$env:PATH = "C:\tools\msys64\mingw64\bin;" + $env:PATH
$env:CGO_ENABLED='1'
go test -race ./internal/... ./cmd/loopcheck/
```

## 验收 1：Modbus RTU 解析正确

`internal/parser.TestModbusFrameParses`，脚本用真实 API（`crc16_modbus` / `struct_uint8` / `struct_uint16_be` / `band` / `brshift` / `string.sub`）：

输入帧 `01 03 04 00 64 00 C8 <CRC_L> <CRC_H>`（CRC 由 `lua.CRC16Modbus` 计算后拼入）

断言结果：

| 字段 | 期望 | 实际 |
| --- | --- | --- |
| `addr` | 1 | 1 |
| `func` | 3 | 3 |
| `reg1` | 100 | 100 |
| `reg2` | 200 | 200 |
| `crc_ok` | true | true |
| `chart:point` | 2 点（reg1=100, reg2=200） | 一致（33ms 合批） |

同时覆盖：
- `TestIdleReassemblesSplitFrame`：一帧分两批到达 → IDLE 合成一帧（只解析 1 次）；
- `TestStickyPacketsWithoutDetectBecomeOneFrame`：无 `detect_frame` 的粘包 → 合并成一帧且 CRC 校验失败而非崩溃；
- `TestDetectFrameSplitsStickyPackets`：脚本边界把 12 字节粘包切成 2 帧；
- `internal/app.TestProjectOpenWiresSerialTapToLua`：**端到端**——打开工程自动加载 `scripts/main.lua` → 串口读循环经 `rawTap` 进入管线 → `frame:parsed` 字段正确（覆盖此前无测试的 tap 接线）；
- `internal/app.TestProjectSwitchDisconnectsSerialAndClearsBytecode`：切换工程后串口变 `disconnected`、新脚本编译 1 份；关闭工程后字节码归 0 且 `project:closed` 正常发出。

## 验收 2：`-race` 并发压测（100 goroutine）

`internal/lua.TestConcurrentOnFrame`：100 goroutine × 50 次 `CallOnFrame`，断言每次返回字段与图表点正确。

`internal/parser.TestConcurrentStatsAndReloadDuringStreaming`：灌流 goroutine + `Stats()` 读线程 + 20 次热重载与 `Clear()` 并发执行，验证 `frMu`/`s.mu`/运行时世代切换无锁序问题。

全量结果：

```
ok  seriallens/internal/app      3.183s
ok  seriallens/internal/framer   1.301s
ok  seriallens/internal/lua      1.970s
ok  seriallens/internal/parser   6.923s
ok  seriallens/internal/project  2.213s
ok  seriallens/internal/serial   1.244s
ok  seriallens/cmd/loopcheck     1.812s
```

**过程中发现并修复 1 个真实数据竞争**：`internal/app` 的 `mockPort.Read` 在锁外读 `readErr`，与测试写入冲突（仅测试代码，非生产）。修复后 `-race` 全绿。

## 验收 3：1Mbps 持续 10 分钟 0 崩溃

`internal/parser.TestSoak1Mbps`（`SL_SOAK_SECONDS=600`）。脚本提供 `detect_frame` 返回 100 字节定长帧，1ms 投喂一帧（=1000 帧/秒 × 100B = 100 KB/s ≈ 1 Mbps 8N1）。

```
soak 600s: sent=593411 parsed=593411 errors=0 dropped=0 queue=0/256
--- PASS: TestSoak1Mbps (600.10s)
```

- 593,411 帧 = 59.3 MB，吞吐 98.9 KB/s（1 Mbps 理论 100 KB/s 的 98.9%，差额为定时器精度）；
- **errors=0、dropped=0、队列始终清空**（`queue=0/256`），说明消费速度远高于注入速度；
- 进程无崩溃、无 OOM（没有强制 GC，靠池复用）。

常规 `go test ./internal/parser/` 默认只跑 5 秒（`SL_SOAK_SECONDS` 不设时），避免每次 CI 等 10 分钟。

## 沙箱与超时可验证项

| 用例 | 结论 |
| --- | --- |
| `TestSandboxBlocksEscapes` | `os`/`io`/`package`/`debug`/`dofile`/`loadfile`/`load`/`require` 全部不可用 |
| `TestTimeoutDiscardsState` | `while true do end` 在 150ms 超时阈值下被打断（`ErrTimeout`），且下一次调用仍成功（运行时自愈） |
| `TestStructOutOfRangeSurfacesError` | 越界读抛 Lua 错误（带 offset/len 上下文），不 panic |
| `TestCompileErrorReportsScriptName` | 语法错误信息含脚本名，且不污染字节码缓存 |

## 工程切换（M2 第三态）

| 用例 | 结论 |
| --- | --- |
| `TestSwitchCallsHooksInOrder` | Create(A) → `after:A`；切 A→B → `before` → `after:B` |
| `TestOpenSameDirSkipsHooks` | 重开同一工程只重载脚本，不触发 `BeforeSwitch`（不白断串口） |
| `TestSwitchFailureKeepsPreviousProject` | `BeforeSwitch` 失败 → 停留原工程 + `project:error{code:project_switch_failed}` |
| `TestOpenInvalidProjectDoesNotTeardown` | 新工程 `project.json` 坏 → 根本不进入拆卸 |
| `TestCloseRunsHooksAndResetsState` | 关闭走 `BeforeSwitch`，落到 `NoProject` |
| `TestClearResetsBytecodeAndStopsParsing` | `Clear()` 后 `bytecode=0`，旧脚本不再解析（PRD §8 M0「残留字节码=0」在 M2 起生效） |

## 遗留与后续

1. **图表渲染**：`chart:point` 已批量推送，前端目前只显示计数与最近字段（`stores/parser.ts` + `FieldsBar`），uPlot/ECharts 渲染属 M3。
2. **虚拟滚动**：终端仍是「保留 5000 行 + DOM 1200 行」的裁剪方案（M1 决定），M3 图表联调时再上虚拟滚动。
3. **`decode_frame` 单帧试跑**：`ParserAPI.DecodeHexFrame` 已可用，M4 编辑器「测试此帧」直接复用。
4. **脚本模板**：Modbus 示例目前只存在于测试里，M6「工程模板」会把它落成 `scripts/main.lua` 预置内容。
5. **沙箱严格性**：`math.random` 未播种（脚本每次运行随机序列相同），如需随机应显式提供种子 API —— 当前不提供，避免 AI 脚本引入不可复现行为。
