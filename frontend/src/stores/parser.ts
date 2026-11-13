import { create } from "zustand";
import { Events } from "@wailsio/runtime";

// 解析管线的前端视图：只保留「最近一帧字段」与计数，帧本体走终端，避免双份内存。
export interface ParsedFrame {
  hex: string;
  ts: number;
  fields: Record<string, unknown> | null;
  durationUs: number;
}

export interface ParserState {
  script: string;
  frames: number;
  errors: number;
  lastError: string;
  last: ParsedFrame | null;
  chartPoints: number;
  setError: (msg: string) => void;
}

const unsubs: (() => void)[] = [];

export const useParserStore = create<ParserState>((set) => {
  unsubs.push(
    Events.On("frame:parsed", (ev: any) => {
      const d = ev.data ?? {};
      set((st) => ({
        frames: st.frames + 1,
        last: {
          hex: String(d.hex ?? ""),
          ts: Number(d.ts ?? Date.now()),
          fields: (d.fields ?? null) as Record<string, unknown> | null,
          durationUs: Number(d.duration_us ?? 0),
        },
      }));
    }),
  );
  unsubs.push(
    Events.On("chart:point", (ev: any) => {
      const points = ev.data?.points;
      if (Array.isArray(points)) {
        set((st) => ({ chartPoints: st.chartPoints + points.length }));
      }
    }),
  );
  unsubs.push(
    Events.On("script:reloaded", (ev: any) => {
      set({ script: String(ev.data?.name ?? ""), lastError: "" });
    }),
  );
  unsubs.push(
    Events.On("script:error", (ev: any) => {
      set((st) => ({
        errors: st.errors + 1,
        lastError: String(ev.data?.error ?? "脚本错误"),
      }));
    }),
  );
  unsubs.push(
    Events.On("project:opened", () => {
      // 切工程后计数归零，脚本名等 script:reloaded 到达再填
      set({ frames: 0, errors: 0, lastError: "", last: null, chartPoints: 0, script: "" });
    }),
  );
  unsubs.push(Events.On("project:closed", () => set({ script: "", last: null })));

  return {
    script: "",
    frames: 0,
    errors: 0,
    lastError: "",
    last: null,
    chartPoints: 0,
    setError: (msg) => set({ lastError: msg, errors: 0 }),
  };
});

if (import.meta.hot) {
  import.meta.hot.dispose(() => unsubs.splice(0).forEach((off) => off()));
}
