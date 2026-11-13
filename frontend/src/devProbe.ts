// DEV 专用：UI 冻结度量探针。
//  1) rAF 帧间隔：窗口可见时最直观，但窗口被遮挡/最小化时 WebView2 会节流 rAF，样本失真；
//  2) long task（>50ms 主线程任务）：不受窗口可见性影响，是「卡不卡」的硬指标。
// 生产构建不会引入本模块（main.tsx 仅在 import.meta.env.DEV 时动态 import）。
import { useTerminalStore } from "./stores/terminal";
import { applyChartPoints } from "./stores/charts";
import { useProjectStore } from "./stores/project";
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

requestAnimationFrame(tick);
console.info("[devProbe] 探针已启用：window.__slDevStats()");
