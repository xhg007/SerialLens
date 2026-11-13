// DEV 专用：UI 冻结度量探针。
//  1) rAF 帧间隔：窗口可见时最直观，但窗口被遮挡/最小化时 WebView2 会节流 rAF，样本失真；
//  2) long task（>50ms 主线程任务）：不受窗口可见性影响，是「卡不卡」的硬指标。
// 生产构建不会引入本模块（main.tsx 仅在 import.meta.env.DEV 时动态 import）。
import { useTerminalStore } from "./stores/terminal";
import { applyChartPoints } from "./stores/charts";
import { useProjectStore } from "./stores/project";
import { useEditorStore } from "./stores/editor";
import { useParserStore } from "./stores/parser";
import { useAIStore } from "./stores/ai";
import * as ChartsAPI from "../bindings/seriallens/internal/app/chartsapi";
import * as ParserAPI from "../bindings/seriallens/internal/app/parserapi";

interface DevStats {
  frames: number;
  maxGapMs: number;
  p99GapMs: number;
  longTasks: number;
  maxLongTaskMs: number;
  p95LongTaskMs: number;
  visibility: string;
  sinceMs: number;
  lines: number;
  paused: boolean;
}

const gaps: number[] = [];
let longTasks: number[] = [];
const MAX_SAMPLES = 10000;
let last = performance.now();
let startedAt = Date.now();

function tick(now: number) {
  const gap = now - last;
  last = now;
  if (gap > 0 && gap < 5000) {
    gaps.push(gap);
    if (gaps.length > MAX_SAMPLES) gaps.shift();
  }
  requestAnimationFrame(tick);
}

if ("PerformanceObserver" in window) {
  try {
    new PerformanceObserver((list) => {
      for (const e of list.getEntries()) {
        longTasks.push(e.duration);
      }
      if (longTasks.length > 2000) longTasks = longTasks.slice(-2000);
    }).observe({ entryTypes: ["longtask"] });
  } catch {
    // 老版本 WebView 无 longtask 支持时退化为仅帧间隔
  }
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0;
  const idx = Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length));
  return sorted[idx];
}

function stats(): DevStats {
  const sorted = [...gaps].sort((a, b) => a - b);
  const lt = [...longTasks].sort((a, b) => a - b);
  const st = useTerminalStore.getState();
  return {
    frames: gaps.length,
    maxGapMs: Math.round(sorted[sorted.length - 1] ?? 0),
    p99GapMs: Math.round(percentile(sorted, 99)),
    longTasks: lt.length,
    maxLongTaskMs: Math.round(lt[lt.length - 1] ?? 0),
    p95LongTaskMs: Math.round(percentile(lt, 95)),
    visibility: document.visibilityState,
    sinceMs: Date.now() - startedAt,
    lines: st.lines.length,
    paused: st.paused,
  };
}

declare global {
  interface Window {
    __slDevStats?: () => DevStats;
    __slDevReset?: () => void;
    __slDevFeedCharts?: (channels: number, hz: number, seconds: number) => Promise<Record<string, number>>;
    __slDevCreateProject?: (dir: string, name: string) => Promise<void>;
    __slDevOpenProject?: (dir: string) => Promise<void>;
    __slDevReloadCharts?: () => Promise<void>;
    __slDevReloadScript?: () => Promise<void>;
    __slDevEditorSetContent?: (text: string) => void;
    __slDevEditorSave?: () => Promise<Record<string, unknown> | null>;
    __slDevWaitField?: (name: string, timeoutMs?: number) => Promise<number>;
    __slDevLastFields?: () => string[];
    __slDevInspectDir?: (dir: string) => Promise<Record<string, unknown>>;
    __slDevAISave?: (patch: Record<string, unknown>) => Promise<Record<string, unknown> | null>;
    __slDevAITest?: (patch: Record<string, unknown>) => Promise<string>;
    __slDevAISend?: (text: string) => Promise<void>;
    __slDevAICancel?: () => Promise<void>;
    __slDevAIState?: () => Record<string, unknown>;
  }
}

window.__slDevStats = stats;
window.__slDevReset = () => {
  gaps.length = 0;
  longTasks = [];
  last = performance.now();
  startedAt = Date.now();
};

// M3 验收：10 通道 × 100Hz 合成灌流，走与 chart:point 事件相同的入口
// （applyChartPoints → SeriesBuffer → uPlot setData）。
window.__slDevFeedCharts = async (channels = 10, hz = 100, seconds = 10) => {
  const intervalMs = 1000 / hz;
  const total = Math.round(seconds * hz);
  const t0 = performance.now();
  let sent = 0;
  for (let i = 0; i < total; i++) {
    const ts = Date.now();
    const pts: { ch: string; v: number; ts: number }[] = [];
    for (let c = 0; c < channels; c++) {
      pts.push({ ch: `ch${c + 1}`, v: Math.sin((i / 20) * (c + 1)) * 50 + 50 + c, ts });
    }
    applyChartPoints(pts);
    sent += pts.length;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  return {
    points: sent,
    elapsedMs: Math.round(performance.now() - t0),
    effectiveHz: Math.round(sent / channels / (seconds || 1)),
  };
};

// 工程操作钩子：MCP 自动化里没法点系统目录对话框，DEV 下直接走 store。
window.__slDevCreateProject = async (dir, name) => {
  await useProjectStore.getState().createProject(dir, name, "");
};
window.__slDevOpenProject = async (dir) => {
  await useProjectStore.getState().openProject(dir);
};
window.__slDevReloadCharts = async () => {
  await ChartsAPI.ReloadCharts();
};
window.__slDevReloadScript = async () => {
  await ParserAPI.ReloadActiveScript();
};

// M4 验收：编辑器保存路径与「< 100ms 生效」的端到端测量。
// 走 store 的 content → save 链路（与用户在 Monaco 里 Ctrl+S 完全一致），
// 只差键盘事件本身。
window.__slDevEditorSetContent = (text) => {
  useEditorStore.setState({ content: text });
};
window.__slDevEditorSave = async () => {
  await useEditorStore.getState().save();
  const s = useEditorStore.getState().lastSave;
  return s ? ({ ...s } as unknown as Record<string, unknown>) : null;
};
window.__slDevLastFields = () => {
  const f = useParserStore.getState().last?.fields;
  return f ? Object.keys(f) : [];
};
// 轮询到某字段出现在 frame:parsed 就返回耗时（ms），超时返回 -1。
window.__slDevWaitField = async (name, timeoutMs = 2000) => {
  const t0 = performance.now();
  while (performance.now() - t0 < timeoutMs) {
    const fields = useParserStore.getState().last?.fields;
    if (fields && name in fields) return Math.round(performance.now() - t0);
    await new Promise((r) => requestAnimationFrame(r));
  }
  return -1;
};

// M5 验收：AI 设置/连接测试/发送/取消都走真实 store → Wails 绑定链路。
window.__slDevAISave = async (patch) => {
  await useAIStore.getState().saveSettings(patch as any);
  return (useAIStore.getState().settings as unknown as Record<string, unknown>) ?? null;
};
window.__slDevAITest = async (patch) => {
  await useAIStore.getState().testConnection(patch as any);
  return useAIStore.getState().testResult;
};
window.__slDevAISend = async (text) => {
  await useAIStore.getState().send(text);
};
window.__slDevAICancel = async () => {
  await useAIStore.getState().cancel();
};
window.__slDevInspectDir = async (dir) => {
  const info = await useProjectStore.getState().inspectDir(dir);
  return { ...info } as unknown as Record<string, unknown>;
};
window.__slDevAIState = () => {
  const s = useAIStore.getState();
  return {
    busy: s.busy,
    error: s.error,
    messages: s.messages.map((m) => ({
      role: m.role,
      len: m.content.length,
      streaming: !!m.streaming,
      canceled: !!m.canceled,
      usage: m.usage,
      elapsedMs: m.elapsedMs,
      head: m.content.slice(0, 40),
    })),
  };
};

requestAnimationFrame(tick);
console.info("[devProbe] 探针已启用：window.__slDevStats()");
