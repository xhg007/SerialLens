import { create } from "zustand";
import { Events } from "@wailsio/runtime";

export interface TerminalLine {
  hex: string;
  ts: number;
  direction: "rx" | "tx";
}

const MAX_LINES = 5000;

export interface TerminalState {
  lines: TerminalLine[];
  paused: boolean;
  fullscreen: boolean;
  append: (line: TerminalLine) => void;
  clear: () => void;
  togglePause: () => void;
  toggleFullscreen: () => void;
}

export const useTerminalStore = create<TerminalState>((set) => {
  Events.On("serial:data", (ev: any) => {
    const line: TerminalLine = {
      hex: String(ev.data?.hex ?? ""),
      ts: Number(ev.data?.ts ?? Date.now()),
      direction: "rx",
    };
    set((st) => {
      if (st.paused) return st;
      const lines = [...st.lines, line];
      if (lines.length > MAX_LINES) lines.splice(0, lines.length - MAX_LINES);
      return { lines };
    });
  });

  return {
    lines: [],
    paused: false,
    fullscreen: false,
    append: (line) =>
      set((st) => {
        const lines = [...st.lines, line];
        if (lines.length > MAX_LINES) lines.splice(0, lines.length - MAX_LINES);
        return { lines };
      }),
    clear: () => set({ lines: [] }),
    togglePause: () => set((st) => ({ paused: !st.paused })),
    toggleFullscreen: () => set((st) => ({ fullscreen: !st.fullscreen })),
  };
});
