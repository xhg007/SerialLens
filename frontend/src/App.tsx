import { useCallback, useEffect, useRef, useState } from "react";
import { ChartPanel } from "./panels/ChartPanel";
import { ProjectSwitcher } from "./panels/ProjectSwitcher";
import { ConnectionPanel } from "./panels/ConnectionPanel";
import { TerminalPanel } from "./panels/TerminalPanel";
import { useChartsStore } from "./stores/charts";
import { useConnectionStore } from "./stores/connection";
import { useProjectStore } from "./stores/project";
import { useParserStore } from "./stores/parser";

export default function App() {
  // 前端状态（工程/图表）在 HMR 或页面重载后会丢，向后端对账一次；
  // 正常启动时后端也没有工程，两个调用都是空操作。
  useEffect(() => {
    void useProjectStore.getState().syncCurrent();
    void useChartsStore.getState().hydrate();
  }, []);

  return (
    <div className="flex h-screen flex-col bg-bg-0 text-text-1 font-mono">
      {/* 顶栏 */}
      <header className="flex h-10 shrink-0 items-center gap-3 border-b border-border bg-bg-1 px-3 text-sm">
        <span className="font-semibold tracking-wide text-accent">SerialLens</span>
        <span className="text-text-2">智能串口协议调试工具</span>
        <div className="ml-auto flex items-center gap-3" />
      </header>

      <div className="flex min-h-0 flex-1">
        {/* 左侧栏 */}
        <aside className="flex w-60 shrink-0 flex-col gap-2 border-r border-border bg-bg-1 p-2">
          <ProjectSwitcher />
          <ConnectionPanel />
        </aside>

        {/* 主区：图表 + 终端 上下分屏 */}
        <main className="min-w-0 flex-1">
          <SplitStack />
        </main>
      </div>

      {/* 状态栏 */}
      <footer className="flex h-6 shrink-0 items-center gap-4 border-t border-border bg-bg-1 px-3 text-xs text-text-2">
        <StatusLine />
      </footer>
    </div>
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
