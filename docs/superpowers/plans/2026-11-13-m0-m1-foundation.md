# SerialLens M0+M1 实施计划（工程域 + 骨架）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 M0 工程域（ProjectService 状态机、project.json、最近列表、目录骨架）与 M1 骨架（Wails3 服务注册 + 串口收发 + 终端回显 + 工程切换器 UI），交付可运行的桌面应用。

**Architecture:** Go 后端按 PRD §7.3 目录结构组织：`internal/project/` 为核心枢纽（路径单一来源），`internal/serial/` 为 Port 接口抽象（v2 总线扩展预留），`internal/app/` 是 Wails Services 薄壳（方法绑定 + 事件推送）。前端 React+TS 用 Zustand 存 store、`@wailsio/runtime` 订阅事件。

**Tech Stack:** Go 1.22+（脚手架为 1.26）、Wails v3.0.0（锁定）；串口 github.com/bugst/go-serial/v2（PRD §10.1；若 `go get` 不可用则回退 v1 `github.com/bugst/go-serial`，接口签名不变）；前端 React 18 + TS + Vite + Zustand + **Tailwind CSS v4（@tailwindcss/vite 插件）**。

## UI 设计决策（2026-11-13 用户确认，所有前端任务必须遵守）

- **布局**：工作台式三栏 + 顶栏 + 状态栏。左栏 240px（工程/连接/命令/录制入口），主区**终端+图表默认上下分屏**（可拖拽），右侧 AI 面板 360px 可折叠；连接参数与状态在顶栏。
- **配色（深色单主题，CSS Variables 供 Tailwind @theme 消费）**：`bg-0 #0F131A`、`bg-1 #151B24`、`bg-2 #1C2430`、`border #26303E`、`text-1 #E6EDF3`、`text-2 #8B98A9`、`accent #2DD4BF`、`rx #34D399`、`tx #FB923C`、`danger #F87171`、`warn #FBBF24`。
- **图表通道 10 色板**（M3 charts.json 默认取色顺序）：`#2DD4BF #38BDF8 #A78BFA #F472B6 #FBBF24 #34D399 #FB7185 #22D3EE #F97316 #E879F9`。
- **风格**：极简 flat —— 6px 圆角、1px 描边、无阴影、数据密度优先。
- **图表全屏**：每个图表 widget 必须提供全屏功能（右上角全屏按钮或双击标题进入全屏 overlay，Esc 退出）——M3 图表任务必须实现，本计划终端面板的全屏按钮先行实现同一交互模式。
- **字体**：终端/代码 `Cascadia Code, JetBrains Mono, Consolas, monospace`，14px/1.5。

**Spec:** `prd.md`（§3 工程制工作区模型、§7 架构、§8 里程碑 M0/M1）

## Global Constraints

- Wails 版本锁定 v3.0.0，不升级（风险清单第 1 条）。
- 跨前后端数据仅允许 `map[string]any` 与基础类型（PRD §7.4 约定）。
- 所有需要工程路径的代码必须经 `ProjectService.Current()` 获取，禁止各自持有副本（§7.5）。
- 事件契约唯一事实来源：`internal/app/events.go`（本计划只建 M0/M1 用到的事件，后续里程碑追加）。
- JSON 落盘必校验：project.json 校验失败不写盘（§7.5）。
- 工程路径含中文/空格的处理统一走 Go `filepath`（风险清单）。
- 测试全部用 Go 标准库 `testing`，不引入 testify。
- Git 提交信息用 conventional commits（feat:/test:/chore:）。

## Review Focus

以下输入类是 PRD 隐含但各任务测试未覆盖、最可能咬人的场景（每行已在对应任务加了钉住它的测试）：

1. **工程路径含中文/空格** → Task 3/6 测试用 `测试 工程 #1` 作为目录名验证 Load/Create/Open 全链路。
2. **project.json 被手改损坏**（JSON 语法错 / 缺字段 / 波特率非法）→ Task 3 测试三种损坏形态均返回 error 且不写盘。
3. **recent_projects.json 失效条目**（路径不存在或缺 project.json）→ Task 5 测试 `Validate` 标记 `Missing=true` 不自动删除。
4. **切换工程失败不留半加载状态**（打开损坏工程时 Current() 必须保持不变）→ Task 6 测试失败回滚。
5. **串口断线（拔掉 USB）** → Task 9 读 goroutine 收到 error 必须置 Disconnected 状态并 emit 事件，不 panic、不泄漏 goroutine（用 mock Port 注入 error 验证）。

---

## File Structure（本计划涉及的文件）

```
seriallens/
├── main.go                          # 改：注册 ProjectService/SerialService，删除示例 ticker
├── go.mod                           # 改：module seriallens
├── greetservice.go                  # 删
├── internal/
│   ├── project/
│   │   ├── metadata.go              # project.json 结构体 + Load/Save + 校验
│   │   ├── metadata_test.go
│   │   ├── layout.go                # 目录骨架生成 + 空白模板（embed.FS）
│   │   ├── layout_test.go
│   │   ├── templates/blank/main.lua         # embed 模板
│   │   ├── templates/blank/charts.json
│   │   ├── recent.go                # ~/.seriallens/recent_projects.json
│   │   ├── recent_test.go
│   │   ├── service.go               # 生命周期状态机 Create/Open/Switch/Close + Current()
│   │   └── service_test.go
│   ├── serial/
│   │   ├── port.go                  # Port 接口 + Config + Enumerate
│   │   ├── serialport.go            # go.bug.st 实现
│   │   └── serialport_test.go       # 仅枚举冒烟（无硬件断言）
│   └── app/
│       ├── events.go                # 事件契约常量
│       ├── project_service.go       # Wails 薄壳
│       ├── serial_service.go        # Wails 薄壳 + 读 goroutine + 33ms 合帧
│       └── serial_service_test.go   # mock Port 注入
└── frontend/src/
    ├── stores/project.ts            # Zustand
    ├── stores/connection.ts
    ├── stores/terminal.ts
    ├── panels/ProjectSwitcher.tsx
    ├── panels/ConnectionPanel.tsx
    ├── panels/TerminalPanel.tsx
    └── App.tsx                      # 改：三面板布局
```

---

### Task 1: 工程基础修正

**Files:**
- Modify: `go.mod`（module changeme → seriallens）
- Modify: `main.go`（Name 保留 SerialLens，窗口 Title 改 "SerialLens"，Description 改 "智能串口协议调试工具"）
- Delete: `greetservice.go`
- Modify: `frontend/src/App.tsx`（移除 Greet 调用，占位布局）

**Interfaces:**
- Produces: module 名 `seriallens`，后续所有 import 路径为 `seriallens/internal/...`。

- [ ] **Step 1: 改 go.mod module 名为 seriallens**，全局替换代码中的 `changeme` 引用。
- [ ] **Step 2: 删除 greetservice.go，main.go 移除 GreetService 绑定与 ticker goroutine**，`main.go` 只保留 app 创建 + 窗口 + Run。
- [ ] **Step 3: 验证编译** 运行 `go build ./...`，期望无错误。
- [ ] **Step 4: Commit** `git commit -m "chore: rename module to seriallens, remove demo service"`

### Task 2: 事件契约 events.go

**Files:**
- Create: `internal/app/events.go`
- Test: `internal/app/events_test.go`

**Interfaces:**
- Produces: 常量 `EvProjectOpened="project:opened"`, `EvProjectClosed="project:closed"`, `EvProjectError="project:error"`, `EvRecentUpdated="recent:updated"`, `EvSerialStatus="serial:status"`, `EvSerialData="serial:data"`；类型 `Emitter func(event string, payload map[string]any)`（后续 Service 与 main.go 桥接用）。

- [ ] **Step 1: 写失败测试** `events_test.go` 断言各常量值与 PRD §7.4 字符串完全一致（如 `assert EvProjectOpened == "project:opened"`）。
- [ ] **Step 2: 运行确认失败** `go test ./internal/app/ -run TestEventContract -v`，期望编译失败（常量未定义）。
- [ ] **Step 3: 实现 events.go**：常量块 + `type Emitter func(event string, payload map[string]any)`，每个常量加注释说明 payload 结构（照抄 PRD §7.4）。
- [ ] **Step 4: 测试通过** `go test ./internal/app/ -v`。
- [ ] **Step 5: Commit** `git commit -m "feat: event contract constants (M0/M1 subset)"`

### Task 3: project.json 元数据读写与校验

**Files:**
- Create: `internal/project/metadata.go`
- Test: `internal/project/metadata_test.go`

**Interfaces:**
- Produces:
```go
type SerialConfig struct{ Baudrate, DataBits, StopBits int; Parity, FlowControl, LastPort string }
type AgentSettings struct{ MaxAttempts int; ModelOverride string; ContextHints string }
type ProjectMeta struct {
    Version, Name, Description string
    CreatedAt, UpdatedAt time.Time   // RFC3339 JSON 序列化
    SerialConfig   SerialConfig
    ActiveScript   string
    AgentSettings  AgentSettings
}
func DefaultMeta(name string) *ProjectMeta           // Baudrate=115200 DataBits=8 StopBits=1 Parity="N" FlowControl="none" ActiveScript="main.lua" MaxAttempts=3
func LoadMeta(dir string) (*ProjectMeta, error)      // 读 <dir>/project.json；JSON 错/缺 name/version → error
func SaveMeta(dir string, m *ProjectMeta) error      // Validate 失败不写盘；写前刷新 UpdatedAt
func Validate(m *ProjectMeta) error                  // name 非空、version=="1.0"、Baudrate∈[1200,10000000]、Parity∈{N,E,O}
func IsProject(dir string) bool                      // project.json 存在且 LoadMeta 成功
```
JSON 字段名照抄 PRD §3.3（snake_case）。

- [ ] **Step 1: 写失败测试**，用例：① LoadMeta 解析 PRD §3.3 示例 JSON 得到 `SerialConfig.Baudrate==115200`；② 目录不存在 → error；③ 三种损坏（非法 JSON、缺 `name`、`baudrate: 100`）→ error；④ SaveMeta 校验失败（空 name）不产生文件；⑤ SaveMeta→LoadMeta roundtrip 相等；⑥ 目录名 `测试 工程 #1`（中文+空格+特殊字符）全链路通过；⑦ `IsProject` 对含 project.json 与不含的目录返回 true/false。
- [ ] **Step 2: 运行确认失败** `go test ./internal/project/ -run TestMeta -v`。
- [ ] **Step 3: 实现 metadata.go**。编码 `json.MarshalIndent`；`SaveMeta` 写临时文件后 `os.Rename` 保证原子。
- [ ] **Step 4: 测试通过**。
- [ ] **Step 5: Commit** `git commit -m "feat: project.json metadata load/save with validation"`

### Task 4: 目录骨架与空白模板

**Files:**
- Create: `internal/project/layout.go`
- Create: `internal/project/templates/blank/main.lua`（PRD §10.2 契约注释骨架 + `function on_frame(raw) return nil, "not implemented" end`）
- Create: `internal/project/templates/blank/charts.json`（`{"version":"1.0","layout":{"grid":{"cols":12,"row_height":80}},"widgets":[]}`）
- Test: `internal/project/layout_test.go`

**Interfaces:**
- Consumes: `Task 3` 的 `ProjectMeta`/`DefaultMeta`/`SaveMeta`。
- Produces: `func Scaffold(dir, name, description string) (*ProjectMeta, error)` — 生成 §3.2 目录骨架：`scripts/`（含 main.lua）、`conversations/`、`data/`、`.seriallens/samples/`、`.seriallens/drafts/`、charts.json、project.json；已存在 project.json → error 不覆盖。模板以 `//go:embed templates/blank/*` 嵌入。

- [ ] **Step 1: 写失败测试**：① Scaffold 后用 `filepath.WalkDir` 断言 8 个路径存在；② 生成的 project.json 能被 `LoadMeta` 读回且 `ActiveScript=="main.lua"`；③ 对已含 project.json 的目录 Scaffold 返回 error；④ main.lua 内容含 `on_frame`、charts.json 是合法 JSON。
- [ ] **Step 2: 运行确认失败** `go test ./internal/project/ -run TestScaffold -v`。
- [ ] **Step 3: 实现模板文件与 layout.go**。
- [ ] **Step 4: 测试通过**。
- [ ] **Step 5: Commit** `git commit -m "feat: project directory scaffold with blank template (embed.FS)"`

### Task 5: 最近工程列表

**Files:**
- Create: `internal/project/recent.go`
- Test: `internal/project/recent_test.go`

**Interfaces:**
- Consumes: `Task 3` 的 `IsProject`。
- Produces（全局配置根目录可注入以便测试）:
```go
type RecentEntry struct{ Path, Name string; LastOpened time.Time; Pinned bool; Missing bool }
type RecentStore struct{ /* 构造时传入根目录，默认 ~/.seriallens */ }
func NewRecentStore(home string) *RecentStore
func (r *RecentStore) List() []RecentEntry            // last_opened 倒序，pinned 恒在前
func (r *RecentStore) RecordOpen(path, name string) error  // 存在则刷新时间，否则追加；超 20 条淘汰最旧未 pinned
func (r *RecentStore) Remove(path string) error
func (r *RecentStore) SetPinned(path string, pinned bool) error
func (r *RecentStore) Validate()                      // !IsProject(path) → Missing=true，不删除
```

- [ ] **Step 1: 写失败测试**（用 `t.TempDir()` 作 home）：① RecordOpen 3 条后 List 按 last_opened 倒序；② 超过 20 条时 pinned 项不被淘汰；③ SetPinned 后 pinned 恒在前；④ Remove 生效；⑤ Validate 将路径不存在的条目标 Missing=true 且保留；⑥ JSON 文件损坏时 List 返回空切片不 panic。
- [ ] **Step 2: 运行确认失败** `go test ./internal/project/ -run TestRecent -v`。
- [ ] **Step 3: 实现 recent.go**，文件 `recent_projects.json` 直接存在 home 下。
- [ ] **Step 4: 测试通过**。
- [ ] **Step 5: Commit** `git commit -m "feat: recent projects list (cap 20, pinned, missing detection)"`

### Task 6: 工程生命周期状态机

**Files:**
- Create: `internal/project/service.go`
- Test: `internal/project/service_test.go`

**Interfaces:**
- Consumes: Task 3 `LoadMeta/SaveMeta/IsProject`、Task 4 `Scaffold`、Task 5 `RecentStore`、Task 2 `Emitter`。
- Produces:
```go
type State int
const (StateNoProject State = iota; StateProjectOpen)
type Service struct{ /* mu sync.Mutex; state; currentDir string; meta *ProjectMeta; recents *RecentStore; emit Emitter */ }
func NewService(recents *RecentStore, emit Emitter) *Service
func (s *Service) State() State
func (s *Service) Current() (dir string, meta *ProjectMeta, ok bool)
func (s *Service) Create(dir, name, description string) error
func (s *Service) Open(dir string) error     // NoProject→Open 或 Open→Open（切换）；失败不改变现有状态
func (s *Service) Close() error              // → NoProject
```
`Open` 的切换语义（M0 阶段无串口/Runtime，先落"读新工程成功才替换"的事务顺序，M2+ 在此插入字节码清理）：先 `LoadMeta(newDir)` 成功 → 替换 current/meta → `RecordOpen` → emit `EvProjectOpened`（payload：`path,name,serial_config(map[string]any),active_script`）。Load 失败 → 返回 error 且不触碰现有状态、emit `EvProjectError`。

- [ ] **Step 1: 写失败测试**：① 初始 State==StateNoProject；② Create 后 State==StateProjectOpen 且 Current().ok；③ Open 合法目录成功且 emit 事件名==EvProjectOpened；④ Open 损坏目录返回 error 且 Current() 保持前一个工程不变；⑤ Close 后 State==StateNoProject；⑥ 中文+空格路径 Open 成功。
- [ ] **Step 2: 运行确认失败** `go test ./internal/project/ -run TestService -v`。
- [ ] **Step 3: 实现 service.go**。
- [ ] **Step 4: 测试通过**（全包 `go test ./internal/project/ -v`）。
- [ ] **Step 5: Commit** `git commit -m "feat: project lifecycle state machine (create/open/switch/close)"`

### Task 7: ProjectService Wails 薄壳

**Files:**
- Create: `internal/app/project_service.go`
- Modify: `main.go`（注册服务，`app.Event.Emit` 桥接为 Emitter）

**Interfaces:**
- Consumes: Task 6 `project.Service`、Task 5 `NewRecentStore("")`（默认 `~/.seriallens`）、Task 2 `Emitter`。
- Produces（Wails 绑定方法，前端经 bindings 调用）:
```go
type ProjectService struct{ svc *project.Service; app *application.App }
func NewProjectService() *ProjectService                     // OnStartup 拿到 app 后注入 emit
func (p *ProjectService) CreateProject(dir, name, description string) error
func (p *ProjectService) OpenProject(dir string) error
func (p *ProjectService) CloseProject() error
func (p *ProjectService) GetCurrentProject() map[string]any  // {path, meta} 或 {"ok":false}
func (p *ProjectService) ListRecentProjects() []map[string]any
func (p *ProjectService) RemoveRecentProject(path string) error
```
emit 桥接：`func(event string, payload map[string]any){ p.app.Event.Emit(event, payload) }`（Task 9 SerialService 同模式）。

- [ ] **Step 1: 实现 project_service.go**（薄壳无可测逻辑，不单测）。
- [ ] **Step 2: main.go 注册** `application.NewService(NewProjectService())`；通过 `app.Event` 在服务上注入 Emitter（Wails v3 服务可用 `ServiceStartup` 钩子拿 app，或 main 构造后注入——实现者按 v3.0.0 API 现状选择，保持薄）。
- [ ] **Step 3: 验证** `go build ./...` 通过。
- [ ] **Step 4: Commit** `git commit -m "feat: ProjectService wails binding shell"`

### Task 8: 串口 Port 接口与 go.bug.st 实现

**Files:**
- Create: `internal/serial/port.go`
- Create: `internal/serial/serialport.go`
- Test: `internal/serial/serialport_test.go`

**Interfaces:**
- Produces:
```go
type Config struct{ PortName string; BaudRate, DataBits, StopBits int; Parity, FlowControl string }
type PortInfo struct{ Name, FriendlyName string; VID, PID string }
type Port interface {
    Open(cfg Config) error
    Close() error
    Read(p []byte) (int, error)
    Write(p []byte) (int, error)
    IsOpen() bool
}
func NewPort() Port                          // go.bug.st 实现
func Enumerate() ([]PortInfo, error)         // 枚举系统串口
```
go.bug.st Parity 映射：`"N"/"E"/"O"` → `serial.NoParity/EvenParity/OddParity`；FlowControl `"none"/"rtscts"/"xonxoff"` 对应映射，未知值 error。

- [ ] **Step 1: 先 `go get github.com/bugst/go-serial/v2`**；失败则 `go get github.com/bugst/go-serial` 并相应调整 import（此为 Global Constraints 允许的回退）。
- [ ] **Step 2: 实现 port.go + serialport.go**（无硬件环境，接口层不单测行为）。
- [ ] **Step 3: 写枚举冒烟测试** `serialport_test.go`：`Enumerate()` 返回 err==nil（列表可为空）；`Config` 非法 FlowControl 时 Open 返回 error（不需真实端口，参数校验在 Open 前置）。
- [ ] **Step 4: 运行** `go test ./internal/serial/ -v` 通过。
- [ ] **Step 5: Commit** `git commit -m "feat: serial port interface + go.bug.st implementation"`

### Task 9: SerialService 薄壳（读 goroutine + 合帧）

**Files:**
- Create: `internal/app/serial_service.go`
- Test: `internal/app/serial_service_test.go`

**Interfaces:**
- Consumes: Task 8 `Port/Config/Enumerate`、Task 2 `Emitter`。
- Produces:
```go
type SerialService struct{ port serial.Port; /* mu; state; bytesIn, bytesOut uint64; emit Emitter; stop chan struct{} */ }
func NewSerialService(port serial.Port, emit Emitter) *SerialService
func (s *SerialService) ListPorts() []map[string]any          // [{name,friendly,vid,pid}]
func (s *SerialService) Connect(cfg map[string]any) error     // key: port,baudrate,data_bits,stop_bits,parity,flow_control
func (s *SerialService) Disconnect() error
func (s *SerialService) Send(data string, hexMode bool) error // hexMode 时按空格分隔 HEX 解码；TX 后 bytesOut 累加并 emit EvSerialStatus
func (s *SerialService) GetStatus() map[string]any            // {state:"disconnected|connected",bytes_in,bytes_out}
```
读 goroutine：4096 字节缓冲循环 `Read` → 累入 pending 缓冲；**33ms ticker**（PRD ADR-02）将 pending 整体 `emit(EvSerialData, {"hex": "<HEX字符串>", "ts": 毫秒时间戳})` 后清空。`Read` 返回 error → 状态置 disconnected、emit `EvSerialStatus`、退出 goroutine（Review Focus 第 5 条）。

- [ ] **Step 1: 写失败测试**（用内存 mock Port：`type mockPort struct{ readBuf chan []byte; closed chan struct{}; ... }` 实现 serial.Port）：① Connect 后 GetStatus 返回 connected；② mockPort 注入 2 块数据 → 100ms 内收到 EvSerialData 事件（payload.hex 为大写 HEX）；③ mockPort 注入 error → 状态变 disconnected 且 emit EvSerialStatus；④ Disconnect 后 IsOpen false；⑤ Send("48 65", true) 向 mock 写入 `[]byte{0x48,0x65}` 且 bytesOut==2；⑥ Send 非法 HEX 返回 error。
- [ ] **Step 2: 运行确认失败** `go test ./internal/app/ -run TestSerialService -v`。
- [ ] **Step 3: 实现 serial_service.go**。
- [ ] **Step 4: 测试通过** `go test ./internal/app/ -v`（含 Task 2 事件测试），再 `go test ./... -race` 全绿。
- [ ] **Step 5: Commit** `git commit -m "feat: serial service with read goroutine, 33ms batching, status events"`

### Task 10: 设计系统基建 + SerialService 注册 + 前端 Store 与事件订阅

**Files:**
- Modify: `main.go`（注册 `application.NewService(NewSerialService(...))`，SerialService 同样注入 app emit 桥接）
- Modify: `internal/app/serial_service.go`（如需为 Wails 暴露默认 Port 构造）
- Modify: `frontend/package.json`（`npm i zustand`；`npm i -D tailwindcss @tailwindcss/vite`）
- Modify: `frontend/vite.config.ts`（加 `@tailwindcss/vite` 插件）
- Modify: `frontend/src/index.css`（删除 demo 样式；`@import "tailwindcss"` + `@theme` 注入计划头部「UI 设计决策」的全部颜色 token 与等宽字体栈）
- Modify: `frontend/src/App.tsx`（重建为工作台骨架：顶栏 + 左栏占位 + 主区占位 + 状态栏，全部用 Tailwind 类）
- Create: `frontend/src/stores/project.ts`
- Create: `frontend/src/stores/connection.ts`
- Create: `frontend/src/stores/terminal.ts`

**Interfaces:**
- Consumes: Task 7/9 的绑定方法与事件（`@wailsio/runtime` 的 `Events.On` + 生成的 bindings；binding 未生成时可先 `Calls` 手写路径，wails3 dev 首次运行会生成 `frontend/bindings`）。
- Produces:
```ts
// project.ts: { current: {path,name}|null, recents: [], openProject(dir), createProject(...), closeProject(), refreshRecents() } 订阅 project:opened/closed/recent:updated
// connection.ts: { state:'disconnected'|'connected', bytesIn, bytesOut, ports: [], listPorts(), connect(cfg), disconnect(), send(data,hex) } 订阅 serial:status
// terminal.ts: { lines: {hex,ts,direction:'rx'|'tx'}[] (上限 5000 条环形裁剪), paused, append(line), clear(), togglePause() } 订阅 serial:data；TX 由 connection.send 成功后 append direction:'tx'
// index.css @theme 导出: --color-bg-0/-1/-2、--color-border、--color-text-1/-2、--color-accent/rx/tx/danger/warn、--font-mono
```
Tailwind v4 通过 `@theme` 将上述 CSS 变量注册为可用类（如 `bg-bg-1`、`text-accent`、`border-border`），后续面板任务只消费这些类。

- [ ] **Step 1: `npm i zustand` + `npm i -D tailwindcss @tailwindcss/vite`**（frontend 目录）。
- [ ] **Step 2: 配置 vite 插件 + 重写 index.css（@theme token）+ 重建 App.tsx 工作台骨架**（顶栏/左栏/主区/状态栏占位，验证 token 类生效）。
- [ ] **Step 3: 实现三个 store**，事件订阅在 store 创建时注册，`Events.On` 返回的取消函数在模块卸载不需要（应用级常驻）。
- [ ] **Step 4: 验证类型编译** `npm run build`（frontend 目录）通过。
- [ ] **Step 5: Commit** `git commit -m "feat: tailwind design system + frontend stores with event subscription"`

### Task 11: 工程切换器 + 连接面板 UI

**Files:**
- Create: `frontend/src/panels/ProjectSwitcher.tsx`
- Create: `frontend/src/panels/ConnectionPanel.tsx`
- Modify: `frontend/src/App.tsx`（左侧栏两面板 + 右侧占位）

**Interfaces:**
- Consumes: Task 10 的 `projectStore`、`connectionStore`。
- Produces: ProjectSwitcher —— 顶部当前工程名 + 「新建工程」（弹目录名/工程名输入）与「打开工程」（输入路径 + 最近列表下拉，失效条目灰显带移除按钮）；ConnectionPanel —— 串口下拉（ListPorts）+ 波特率/数据位/停止位/校验下拉 + 「打开串口/关闭串口」按钮 + 状态行（已连接时长、收发字节数）；工程打开后波特率默认值取 `GetCurrentProject().meta.serial_config`。

- [ ] **Step 1: 实现两面板**（样式从简，深色背景，保证可用；不做拖拽布局）。
- [ ] **Step 2: 验证** `npm run build` 通过。
- [ ] **Step 3: Commit** `git commit -m "feat: project switcher and connection panel UI"`

### Task 12: 终端回显面板

**Files:**
- Create: `frontend/src/panels/TerminalPanel.tsx`
- Modify: `frontend/src/App.tsx`（右侧主区挂载终端）

**Interfaces:**
- Consumes: Task 10 `terminalStore`。
- Produces: 终端区 —— HEX 显示模式行渲染（`ts + RX/TX 着色 RX=绿 TX=橙 + HEX 字节`）；工具条：暂停滚动开关、清空、关键字过滤输入（前端 contains 过滤）、**全屏按钮**（进入全屏 overlay 隐藏其他面板，Esc 退出——建立 M3 图表 widget 复用的全屏交互模式）；自动滚动到底（paused 时停止）；环形裁剪上限 5000 行由 store 保证，UI 不做虚拟滚动（M1 验收不含，M2+ 优化）。

- [ ] **Step 1: 实现 TerminalPanel**。
- [ ] **Step 2: 验证** `npm run build` 通过。
- [ ] **Step 3: Commit** `git commit -m "feat: terminal panel with hex echo, filter, pause scroll"`

### Task 13: M1 端到端冒烟验收

**Files:** 无新文件（验证任务）

- [ ] **Step 1: 全量测试** `go test ./... -race -v` 期望全 PASS。
- [ ] **Step 2: 构建** `wails3 build` 期望成功。
- [ ] **Step 3: dev 冒烟** `wails3 dev` 启动 → ① 新建工程到含中文/空格目录 → 界面显示工程名、project.json 落盘、最近列表出现；② 关闭应用重开 → 最近列表可一键重开该工程；③ 若有真实串口设备/USB 转串口：连接后终端出现 RX HEX 行，发送文本/HEX 对端可见（无硬件则跳过，依赖 Task 9 mock 测试保障）。
- [ ] **Step 4: 验收对照** PRD §8 M0（状态机正确）+ M1（115200 稳定收发、UI 不冻结）逐条确认。
- [ ] **Step 5: Commit**（如有微调）`git commit -m "chore: m0/m1 smoke fixes"`

---

## Self-Review 结论

- **Spec 覆盖**：M0 = Task 3/4/5/6（ProjectService、目录骨架、project.json、最近列表，验收"切换后 Runtime 残留字节码=0"中 Runtime 属 M2，Task 6 已预留事务顺序）；M1 = Task 7–13。工程 zip 导入导出、Modbus 模板属 M6，本计划不含。
- **步骤粒度**：每步单一动作；Go 测试用例给出了精确断言值；前端任务无测试框架，以 `npm run build` + 冒烟为验证步骤。
- **类型一致性**：`Emitter`、`serial.Port`、`project.Service.Current()` 在消费任务中的签名与生产任务一致。
- **Review Focus**：5 条风险场景分别由 Task 3（损坏 JSON）、5（失效条目）、6（失败回滚/特殊路径）、9（断线）的测试钉住；中文路径由 Task 3⑥ 与 Task 6⑥ 覆盖。
- **比例**：计划文档 < PRD 对应章节篇幅，无代码转录。
