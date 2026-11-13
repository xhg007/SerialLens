# M3 验收记录：charts.json 图表映射 + 实时渲染

日期：2026-11-13
分支：`feat/m3-charts` → `master`
PRD 条目：§8「M3 图表：ChartsService + charts.json 加载 + uPlot/ECharts 渲染；验收 = 10 通道 × 100Hz 流畅 / 外部编辑 charts.json 实时同步」

## 环境

| 项 | 值 |
| --- | --- |
| 前端 | React 18 + Vite 8 + Tailwind v4，uPlot 1.6.32，ECharts 6.1.0（core + GaugeChart + CanvasRenderer） |
| 后端 | Go 1.26，fsnotify 1.10.1 |
| 串口 | com0com **COM11 → COM12**（真实驱动链路，非 mock） |
| 数据源 | `cmd/loopcheck -tx COM11 -sendonly -rate 6400`：64B 定长帧 × 100 帧/秒 |
| 脚本 | `detect_frame` 返回 64；`on_frame` 每帧 `emit_chart` 10 个通道 → **10ch × 100Hz = 1000 点/秒** |

测量窗口：串口连续灌流下的 20 秒无干扰采样（窗口可见，`visibilityState=visible`）。

## 验收 1：10 通道 × 100Hz 流畅

| 指标 | 结果 |
| --- | --- |
| 平均帧率 | **60 fps** |
| 最大帧间隔 | **17 ms** |
| p99 帧间隔 | 17 ms |
| long task（>50ms） | **0 个** |
| 最长 long task | 0 ms |
| 同时渲染 | 1 条 10 系列折线 + 1 个 ECharts 仪表 + 2 个指示灯 + 1 个字段表 |
| 解析侧 | 帧持续增长（实测 3096 帧/30s 量级），RX 字节数与 `帧数 × 64` 吻合 |

对照 M1 的终端结论（5000 行全量渲染时 20 秒内 46 个 long task、最大 120ms），图表层的做法完全不同、没有复用终端渲染路径：

1. 数据不进 React state：`lib/series.ts` 单例缓冲 + 版本号，图表组件只在版本变化时重画；
2. `chart:point` 本身就是 33ms 合批（后端已合并），前端不再二次节流；
3. uPlot 直接 `setData`，不经过 React diff；ECharts 只 `setOption({series})`。

## 验收 2：外部编辑 charts.json 实时同步

操作：用 PowerShell 在 UI 运行时向 `charts.json` 追加一个 `indicator` widget（`"热同步 6 号"`）。

| 指标 | 结果 |
| --- | --- |
| 写入 → UI 出现新面板 | **167 ms**（fsnotify 事件 + 150ms 合并窗口 + React 渲染） |
| 面板数 | 5 → 6 |
| 数据 | 新面板立即开始跟随 `ch4` 实时值（不走重启） |

同时验证的边界：

| 用例 | 结果 |
| --- | --- |
| 自己保存后是否回环重载 | 否（内容摘要比对，`TestSelfWriteDoesNotLoop`） |
| 外部写入非法配置 | 保留旧配置 + `charts:error`，**用户文件不被覆盖**（`TestInvalidExternalEditKeepsOldConfig`） |
| 带 UTF-8 BOM 的文件 | 正常读取（`TestLoadToleratesUTF8BOM`；实测 PowerShell 5.1 默认就写 BOM） |
| 切换工程 | 旧工程 watcher 关闭，改旧工程文件不再影响 UI（`TestSwitchProjectRebindsWatcher`） |
| 关闭工程 | 监听停止、配置清空（`TestProjectCloseStopsChartsWatch`） |
| 非法配置落盘 | 11 类校验用例全部拒绝且不覆盖原文件（`TestInvalidConfigRejectedAndNotWritten`） |

## 验收 3：自动化测试

```
go test ./internal/... ./cmd/...       全绿（含 internal/charts 19 个用例）
go test -race ./internal/...           全绿（含 fsnotify 监听 goroutine）
npm run build                          通过，产物 582.71 kB / gzip 197.98 kB
```

## 过程中发现并修复的缺陷

1. **图表建不起来（ECharts）**：无通道时组件提前 `return` 占位元素，`init` 的 `useEffect` 依赖为空数组，通道到位后不会重跑 → 永久空白。改为容器始终挂载 + 覆盖层提示。
2. **自写回环判定吃掉真实外部编辑**：原实现用 500ms 时间窗忽略自身写入，实测把紧随其后的用户编辑一起忽略。改为内容摘要比对（`saveWithDigest` + FNV-1a）。
3. **UTF-8 BOM 导致 charts.json 解析失败**：PowerShell 5.1 / 记事本写出的文件带 BOM，界面报 `invalid character '\ufeff'`。`Load` 增加 `trimBOM`。
4. **空白模板图表区全空**：模板 `widgets: []`，新工程打开后一片空白。模板改为预置三个 `channels:"auto"` 面板，并新增模板校验测试（`layout_test.go` 直接调 `charts.Load`）。
5. **启动期无意义报错**：`GetChartsConfig` 在无工程时返回 error，启动即刷 binding 错误日志；改为返回 nil。

## 遗留

1. **拖拽布局**：`SaveChartsConfig` + 校验已就绪，UI 拖拽层未做（M3 验收不含此项）。
2. **降采样**：窗口内点数超过约 5000 时仍全量交给 uPlot（uPlot 能画，但不再省 CPU）。长窗口（600s）+ 100Hz 需要 LTTB 降采样，留待录制回放（M6）一起做。
3. **图表包体**：ECharts 让主包到 583KB（gzip 198KB）。桌面端可接受，若要瘦身可把 gauge 改为自绘。
4. **多 y 轴**：当前所有系列共用左轴；`unit` 不同的通道混在一张图里会被同一尺度压缩。PRD 未要求，遇到再加。
