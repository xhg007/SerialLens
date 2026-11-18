import { Suspense, lazy, useCallback, useEffect, useRef, useState } from "react";
import { TitleBar } from "./components/TitleBar";
import { ChartPanel } from "./panels/ChartPanel";
import { ProjectSwitcher } from "./panels/ProjectSwitcher";
import { ConnectionPanel } from "./panels/ConnectionPanel";
import { TerminalPanel } from "./panels/TerminalPanel";
import { useChartsStore } from "./stores/charts";
import { useConnectionStore } from "./stores/connection";
import { useProjectStore } from "./stores/project";
import { useParserStore } from "./stores/parser";

// Monaco 不进主包：编辑器收起时（或首屏渲染时）不需要它，单独 chunk 按需加载。
const EditorPanel = lazy(() =>
  import("./panels/EditorPanel").then((m) => ({ default: m.EditorPanel })),
);
const AIPanel = lazy(() => import("./panels/AIPanel").then((m) => ({ default: m.AIPanel })));

type Dock = "script" | "ai";

export default function App() {
  const [dockOpen, setDockOpen] = useState(true);
  const [dock, setDock] = useState<Dock>("script");

  // 前端状态（工程/图表/脚本）在 HMR 或页面重载后会丢，向后端对账一次；
  // 正常启动时后端也没有工程，这些调用都是空操作。
  useEffect(() => {
    void useProjectStore.getState().syncCurrent();
    void useChartsStore.getState().hydrate();
  }, []);

  // Ctrl+E 收起/展开右侧面板，Ctrl+I 直接切到 AI 助手
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return;
      const k = e.key.toLowerCase();
      if (k === "e") {
        e.preventDefault();
        setDockOpen((v) => !v);
      } else if (k === "i") {
        e.preventDefault();
        setDockOpen(true);
        setDock("ai");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="flex h-screen flex-col bg-bg-0 text-text-1 font-mono">
      {/* 自绘标题栏（窗口无边框，见 internal/app/desktop.go） */}
      <TitleBar
        dockOpen={dockOpen}
        onOpenDock={(d) => {
          setDockOpen(true);
          setDock(d);
        }}
        onToggleDock={() => setDockOpen((v) => !v)}
      />

      <div className="flex min-h-0 flex-1">
        {/* 左侧栏 */}
        <aside className="flex w-60 shrink-0 flex-col gap-2 border-r border-border bg-bg-1 p-2">
          <ProjectSwitcher />
          <ConnectionPanel />
        </aside>

        {/* 主区：左「图表 + 终端」上下分屏，右「脚本 / AI」页签 */}
        <main className="min-w-0 flex-1">
          <MainSplit open={dockOpen} dock={dock} onSelectDock={setDock} />
        </main>
      </div>

      {/* 状态栏 */}
      <footer className="flex h-6 shrink-0 items-center gap-4 border-t border-border bg-bg-1 px-3 text-xs text-text-2">
        <StatusLine />
      </footer>
    </div>
  );
}

// 右侧面板宽度：最小 320；至少给左侧（240px 侧栏 + 图表终端）留 320px。
// 窗口最小宽度 960 时上限约 396px，默认 36% 会被夹紧，避免终端发送栏被挤出可视区。
const DOCK_MIN = 320;

function clampDockWidth(width: number): number {
  const max = Math.max(DOCK_MIN, window.innerWidth - 240 - 4 - 320);
  return Math.min(Math.max(Math.round(width), DOCK_MIN), max);
}

// MainSplit 左右分屏：左侧图表/终端，右侧「脚本 / AI」页签（宽度可拖拽，Ctrl+E 收起）。
function MainSplit({
  open,
  dock,
  onSelectDock,
}: {
  open: boolean;
  dock: Dock;
  onSelectDock: (d: Dock) => void;
}) {
  const [editorWidth, setEditorWidth] = useState(() => clampDockWidth(window.innerWidth * 0.36));
  const dragging = useRef(false);

  // 窗口缩小后重新夹紧：固定 px 宽度会把主区压到低于可用宽度。
  useEffect(() => {
    const onResize = () => setEditorWidth((w) => clampDockWidth(w));
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragging.current) return;
      const next = window.innerWidth - e.clientX;
      setEditorWidth(clampDockWidth(next));
    };
    const onUp = () => {
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  return (
    <div className="flex h-full min-h-0">
      <div className="min-w-0 flex-1">
        <SplitStack />
      </div>
      {open && (
        <>
          <div
            className="w-1 shrink-0 cursor-col-resize bg-border transition-colors hover:bg-accent"
            onMouseDown={(e) => {
              e.preventDefault();
              dragging.current = true;
              document.body.style.cursor = "col-resize";
              document.body.style.userSelect = "none";
            }}
            title="拖动调整面板宽度"
          />
          <div style={{ width: editorWidth }} className="flex min-w-0 shrink-0 flex-col border-l border-border">
            <div className="flex h-7 shrink-0 items-center gap-1 border-b border-border bg-bg-1 px-1">
              <DockTab active={dock === "script"} onClick={() => onSelectDock("script")}>
                脚本
              </DockTab>
              <DockTab active={dock === "ai"} onClick={() => onSelectDock("ai")}>
                AI 助手
              </DockTab>
            </div>
            <div className="min-h-0 flex-1">
              <Suspense
                fallback={<div className="flex h-full items-center justify-center text-xs text-text-2">加载中…</div>}
              >
                {dock === "script" ? <EditorPanel /> : <AIPanel />}
              </Suspense>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function DockTab({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-panel px-2 py-0.5 text-xs ${
        active ? "bg-bg-2 text-accent" : "text-text-2 hover:text-accent"
      }`}
    >
      {children}
    </button>
  );
}

// SplitStack 上下分屏：图表占剩余空间，终端高度可拖拽（默认 40%）。
function SplitStack() {
  const [termHeight, setTermHeight] = useState(() => Math.round(window.innerHeight * 0.4));
  const dragging = useRef(false);
  const containerRef = useRef<HTMLDivElement | null>(null);

  const onMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    dragging.current = true;
    document.body.style.cursor = "row-resize";
    document.body.style.userSelect = "none";
  }, []);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragging.current) return;
      const box = containerRef.current?.getBoundingClientRect();
      if (!box) return;
      const next = box.bottom - e.clientY - 24; // 24 = 状态栏
      setTermHeight(Math.min(Math.max(next, 120), box.height - 160));
    };
    const onUp = () => {
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  return (
    <div ref={containerRef} className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1">
        <ChartPanel />
      </div>
      <div
        className="h-1 shrink-0 cursor-row-resize bg-border transition-colors hover:bg-accent"
        onMouseDown={onMouseDown}
        title="拖动调整图表/终端比例"
      />
      <div className="min-h-0 shrink-0" style={{ height: termHeight }}>
        <TerminalPanel />
      </div>
    </div>
  );
}

function StatusLine() {
  const { state, config, bytesIn, bytesOut } = useConnectionStore();
  const current = useProjectStore((s) => s.current);
  return (
    <>
      <span>{current ? `工程: ${current.name}` : "无工程"}</span>
      <span>
        {state === "connected" ? `${config.port}·${config.baudrate}` : "串口未连接"}
      </span>
      <span>
        RX <span className="text-rx">{bytesIn}</span> · TX <span className="text-tx">{bytesOut}</span>
      </span>
      <ParserStatus />
    </>
  );
}

function ParserStatus() {
  const { script, frames, errors, lastError } = useParserStore();
  if (!script) return <span className="text-text-2">脚本: 未加载</span>;
  return (
    <>
      <span>
        脚本: <span className="text-accent">{script}</span>
      </span>
      <span>
        解析 <span className="text-rx">{frames}</span> 帧
        {errors > 0 && <span className="ml-1 text-danger">· {errors} 错误</span>}
      </span>
      {lastError && (
        <span className="truncate text-danger" title={lastError}>
          {lastError}
        </span>
      )}
    </>
  );
}
