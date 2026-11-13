import { useEffect, useState, type CSSProperties } from "react";
import { Events } from "@wailsio/runtime";
import * as DesktopAPI from "../../bindings/seriallens/internal/app/desktop";

// Wails 的拖拽判定读的就是这个自定义属性（会继承，所以按钮要显式 no-drag）
const DRAG: CSSProperties = { "--wails-draggable": "drag" } as CSSProperties;
const NO_DRAG: CSSProperties = { "--wails-draggable": "no-drag" } as CSSProperties;

const TOOL_BTN =
  "flex h-6 items-center rounded-panel px-2 text-xs text-text-2 hover:bg-bg-2 hover:text-accent";
const WIN_BTN = "flex h-9 w-11 items-center justify-center text-text-2 transition-colors";

export interface TitleBarProps {
  dockOpen: boolean;
  onOpenDock: (dock: "script" | "ai") => void;
  onToggleDock: () => void;
}

/**
 * 自绘标题栏（窗口是无边框的）：
 * 左侧品牌，中间拖拽区（双击最大化），右侧面板按钮 + 最小化/最大化/关闭。
 * 关闭 = 收进托盘（Desktop.CloseToTray），真正退出在托盘菜单里。
 */
export function TitleBar({ dockOpen, onOpenDock, onToggleDock }: TitleBarProps) {
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    void DesktopAPI.IsMaximised()
      .then((v) => setMaximised(Boolean(v)))
      .catch(() => {});
    // 双击标题栏、Aero Snap、系统快捷键都会走系统侧，靠事件对齐图标
    const offMax = Events.On("common:WindowMaximise", () => setMaximised(true));
    const offUnmax = Events.On("common:WindowUnMaximise", () => setMaximised(false));
    return () => {
      offMax();
      offUnmax();
    };
  }, []);

  const toggleMax = async () => {
    try {
      setMaximised(Boolean(await DesktopAPI.ToggleMaximise()));
    } catch {
      /* 窗口已销毁时忽略 */
    }
  };

  return (
    <header
      style={DRAG}
      onDoubleClick={() => void toggleMax()}
      className="flex h-9 shrink-0 select-none items-center border-b border-border bg-bg-1 pl-3"
    >
      {/* 品牌 */}
      <div className="flex items-center gap-2" style={NO_DRAG}>
        <span className="grid h-4 w-4 place-items-center rounded-[3px] bg-accent/15 text-accent">
          <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.4">
            <path d="M2 8.5 5 3.5l2.4 3.2L10 4.6" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </span>
        <span className="text-[13px] font-semibold tracking-wide text-accent">SerialLens</span>
        <span className="hidden text-[11px] text-text-2 md:inline">智能串口协议调试工具</span>
      </div>

      {/* 面板工具 */}
      <div
        className="ml-auto flex items-center gap-1 pr-1"
        style={NO_DRAG}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <button className={TOOL_BTN} onClick={() => onOpenDock("script")} title="脚本面板 (Ctrl+E)">
          脚本
        </button>
        <button className={TOOL_BTN} onClick={() => onOpenDock("ai")} title="AI 助手 (Ctrl+I)">
          AI 助手
        </button>
        <button className={TOOL_BTN} onClick={onToggleDock} title="收起 / 展开右侧面板">
          {dockOpen ? "收起面板" : "展开面板"}
        </button>
      </div>

      {/* 窗口按钮 */}
      <div
        className="ml-1 flex self-stretch border-l border-border"
        style={NO_DRAG}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <button
          className={`${WIN_BTN} hover:bg-bg-2 hover:text-text-1`}
          title="最小化"
          onClick={() => void DesktopAPI.Minimise()}
        >
          <svg width="12" height="12" viewBox="0 0 12 12" stroke="currentColor" strokeWidth="1.2">
            <path d="M3 9h6" strokeLinecap="round" />
          </svg>
        </button>
        <button
          className={`${WIN_BTN} hover:bg-bg-2 hover:text-text-1`}
          title={maximised ? "还原" : "最大化"}
          onClick={() => void toggleMax()}
        >
          {maximised ? (
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.2">
              <rect x="3.2" y="3.2" width="5.6" height="5.6" rx="0.6" />
              <path d="M5.2 3.2V2.4a.6.6 0 0 1 .6-.6h3.6a.6.6 0 0 1 .6.6v3.6a.6.6 0 0 1-.6.6h-.8" />
            </svg>
          ) : (
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.2">
              <rect x="2.8" y="2.8" width="6.4" height="6.4" rx="0.6" />
            </svg>
          )}
        </button>
        <button
          className={`${WIN_BTN} hover:bg-danger hover:text-bg-0`}
          title="关闭到托盘"
          onClick={() => void DesktopAPI.CloseToTray()}
        >
          <svg width="12" height="12" viewBox="0 0 12 12" stroke="currentColor" strokeWidth="1.2">
            <path d="M3.4 3.4l5.2 5.2M8.6 3.4l-5.2 5.2" strokeLinecap="round" />
          </svg>
        </button>
      </div>
    </header>
  );
}
