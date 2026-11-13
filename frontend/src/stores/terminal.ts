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

export const useTerminalStore = create<TerminalState>((set) => {
  unsubs.push(
    Events.On("serial:data", (ev: any) => {
      const line: TerminalLine = {
        id: nextId++,
        hex: String(ev.data?.hex ?? ""),
        ts: Number(ev.data?.ts ?? Date.now()),
        direction: "rx",
        mode: "hex",
      };
      set((st) => {
        if (st.paused) return st;
        return { lines: cap([...st.lines, line]) };
      });
    }),
  );

  return {
    lines: [],
    paused: false,
    fullscreen: false,
    append: (line) =>
      set((st) => ({ lines: cap([...st.lines, { ...line, id: nextId++ }]) })),
    clear: () => set({ lines: [] }),
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
