// DEV 专用：UI 冻结度量探针。
//  1) rAF 帧间隔：窗口可见时最直观，但窗口被遮挡/最小化时 WebView2 会节流 rAF，样本失真；
//  2) long task（>50ms 主线程任务）：不受窗口可见性影响，是「卡不卡」的硬指标。
// 生产构建不会引入本模块（main.tsx 仅在 import.meta.env.DEV 时动态 import）。
import { useTerminalStore } from "./stores/terminal";

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
  }
}

window.__slDevStats = stats;
window.__slDevReset = () => {
  gaps.length = 0;
  longTasks = [];
  last = performance.now();
  startedAt = Date.now();
};

requestAnimationFrame(tick);
console.info("[devProbe] 探针已启用：window.__slDevStats()");
