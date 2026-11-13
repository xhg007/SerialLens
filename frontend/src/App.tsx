import { ProjectSwitcher } from "./panels/ProjectSwitcher";
import { ConnectionPanel } from "./panels/ConnectionPanel";
import { TerminalPanel } from "./panels/TerminalPanel";
import { useConnectionStore } from "./stores/connection";
import { useProjectStore } from "./stores/project";

export default function App() {
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

        {/* 主区：终端（图表与编辑器 M3/M4 接入上下分屏） */}
        <main className="min-w-0 flex-1 bg-bg-0">
          <TerminalPanel />
        </main>
      </div>

      {/* 状态栏 */}
      <footer className="flex h-6 shrink-0 items-center gap-4 border-t border-border bg-bg-1 px-3 text-xs text-text-2">
        <StatusLine />
      </footer>
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
    </>
  );
}
