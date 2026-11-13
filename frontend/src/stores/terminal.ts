import { create } from "zustand";
import { Events } from "@wailsio/runtime";

export interface TerminalLine {
  id: number;
  hex: string;
  ts: number;
  direction: "rx" | "tx";
  mode: "hex" | "text";
}

const MAX_LINES = 5000;
// 合并窗口：串口 33ms 一帧，若每帧都 set 一次会让 React 以 30fps 重渲染
// 整个列表（5000 行 × 3 span），实测会拖住 WebView。批量 100ms 提交一次。
const FLUSH_MS = 100;
let nextId = 1;

export interface TerminalState {
  lines: TerminalLine[];
  paused: boolean;
  fullscreen: boolean;
  append: (line: Omit<TerminalLine, "id">) => void;
  clear: () => void;
  togglePause: () => void;
  toggleFullscreen: () => void;
}

// HMR 安全：模块重新求值时先取消旧订阅。
const unsubs: (() => void)[] = [];

export const useTerminalStore = create<TerminalState>((set, get) => {
  let pending: TerminalLine[] = [];
  let timer: ReturnType<typeof setTimeout> | null = null;

  const flush = () => {
    timer = null;
    if (pending.length === 0) return;
    const batch = pending;
    pending = [];
    if (get().paused) return; // 暂停期间丢弃，避免恢复时一次性涌入
    set((st) => ({ lines: cap([...st.lines, ...batch]) }));
  };

  const enqueue = (line: TerminalLine) => {
    pending.push(line);
    if (pending.length > MAX_LINES) pending = pending.slice(-MAX_LINES);
    if (timer === null) timer = setTimeout(flush, FLUSH_MS);
  };

  unsubs.push(
    Events.On("serial:data", (ev: any) => {
      enqueue({
        id: nextId++,
        hex: String(ev.data?.hex ?? ""),
        ts: Number(ev.data?.ts ?? Date.now()),
        direction: "rx",
        mode: "hex",
      });
    }),
  );

  return {
    lines: [],
    paused: false,
    fullscreen: false,
    append: (line) => enqueue({ ...line, id: nextId++ }),
    clear: () => {
      pending = [];
      set({ lines: [] });
    },
    togglePause: () => set((st) => ({ paused: !st.paused })),
    toggleFullscreen: () => set((st) => ({ fullscreen: !st.fullscreen })),
  };
});

function cap(lines: TerminalLine[]): TerminalLine[] {
  return lines.length > MAX_LINES ? lines.slice(lines.length - MAX_LINES) : lines;
}

if (import.meta.hot) {
  import.meta.hot.dispose(() => unsubs.splice(0).forEach((off) => off()));
}
