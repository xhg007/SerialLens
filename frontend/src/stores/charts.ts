import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as ChartsAPI from "../../bindings/seriallens/internal/app/chartsapi";
import { seriesBuffer, type ChartPoint } from "../lib/series";

export interface ChannelCfg {
  name: string;
  color?: string;
  unit?: string;
  range?: [number, number];
  display_digits?: number;
  alarm_high?: number;
  alarm_low?: number;
  on_value?: number;
}

export interface WidgetCfg {
  id: string;
  type: "line" | "gauge" | "indicator" | "field_table";
  title: string;
  position: { x: number; y: number; w: number; h: number };
  channels: "auto" | ChannelCfg[];
  window_seconds?: number;
  y_axis_mode?: "auto" | "fixed";
}

export interface ChartsConfig {
  version: string;
  layout: { grid: { cols: number; row_height: number } };
  widgets: WidgetCfg[];
}

// 通道默认色（PRD 图表主题：深色底 + 高对比）
const PALETTE = ["#2DD4BF", "#FB923C", "#60A5FA", "#F472B6", "#A78BFA", "#FACC15", "#34D399", "#F87171"];

export function channelColor(name: string, explicit?: string): string {
  if (explicit) return explicit;
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

export interface ChartsState {
  config: ChartsConfig | null;
  error: string;
  /** 批次版本号：SeriesBuffer 变了就 +1，图表组件据此重绘（自己节流） */
  version: number;
  channels: string[];
  last: Record<string, number>;
  /** 已绑定的 widget 通道（auto 时用于渲染） */
  bind: (w: WidgetCfg) => ChannelCfg[];
  /** 启动/HMR 后从后端拉一次（事件只在工程打开时发） */
  hydrate: () => Promise<void>;
}

function channelList(w: WidgetCfg, known: string[]): ChannelCfg[] {
  if (w.channels !== "auto" && Array.isArray(w.channels) && w.channels.length > 0) {
    return w.channels;
  }
  return known.map((name) => ({ name }));
}

export const useChartsStore = create<ChartsState>((set, get) => {
  const unsubs: (() => void)[] = [];

  unsubs.push(
    Events.On("charts:updated", (ev: any) => {
      const cfg = ev.data?.config as ChartsConfig | undefined;
      if (!cfg) return;
      // 换工程/重载配置时通道可能变了，缓冲跟着重来
      seriesBuffer.reset();
      set({ config: cfg, channels: [], last: {}, version: get().version + 1, error: "" });
    }),
  );
  unsubs.push(
    Events.On("charts:error", (ev: any) => {
      set({ error: String(ev.data?.message ?? "图表配置错误") });
    }),
  );
  unsubs.push(
    Events.On("chart:point", (ev: any) => {
      const points = ev.data?.points as ChartPoint[] | undefined;
      applyChartPoints(points);
    }),
  );
  unsubs.push(
    Events.On("project:closed", () => {
      seriesBuffer.reset();
      set({ config: null, channels: [], last: {}, error: "", version: get().version + 1 });
    }),
  );

  return {
    config: null,
    error: "",
    version: 0,
    channels: [],
    last: {},
    bind: (w) => channelList(w, get().channels),
    hydrate: async () => {
      try {
        const raw = (await ChartsAPI.GetChartsConfig()) as unknown as ChartsConfig;
        if (raw && raw.widgets) set({ config: raw });
      } catch {
        // 没有打开工程时后端返回错误，属正常情况
      }
    },
  };
});

// 组件用：取 widget 绑定的通道（auto 时按已发现通道展开）
export function boundChannels(w: WidgetCfg, known: string[]): ChannelCfg[] {
  return channelList(w, known);
}

// applyChartPoints 是 chart:point 的唯一入口（事件与 DEV 压测探针共用同一条路径，
// 保证测出来的就是真实渲染链路）。
export function applyChartPoints(points: ChartPoint[] | undefined) {
  if (!Array.isArray(points) || points.length === 0) return;
  const st = useChartsStore.getState();
  seriesBuffer.push(points);
  const last = { ...st.last };
  for (const p of points) last[p.ch] = p.v;
  const known = seriesBuffer.knownChannels();
  useChartsStore.setState({
    version: st.version + 1,
    last,
    channels: known.length === st.channels.length ? st.channels : known,
  });
}
