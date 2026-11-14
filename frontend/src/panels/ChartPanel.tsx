import { useEffect, useMemo, useRef, useState } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import * as echarts from "echarts/core";
import { GaugeChart } from "echarts/charts";
import { CanvasRenderer } from "echarts/renderers";
import * as ChartsAPI from "../../bindings/seriallens/internal/app/chartsapi";
import * as ExportAPI from "../../bindings/seriallens/internal/app/exportapi";
import { seriesBuffer } from "../lib/series";
import { saveFileDialog } from "../lib/dialog";
import { boundChannels, channelColor, useChartsStore, type ChannelCfg, type WidgetCfg } from "../stores/charts";
import { useParserStore } from "../stores/parser";

echarts.use([GaugeChart, CanvasRenderer]);

const AXIS_COLOR = "#64748B";
const GRID_COLOR = "rgba(148,163,184,0.12)";
const TOOLBTN =
  "rounded-panel border border-border px-2 py-0.5 text-xs text-text-2 hover:border-accent hover:text-accent";

export function ChartPanel() {
  const config = useChartsStore((s) => s.config);
  const error = useChartsStore((s) => s.error);
  const [fullscreenId, setFullscreenId] = useState<string | null>(null);
  const [exportError, setExportError] = useState("");

  const grid = config?.layout.grid ?? { cols: 12, row_height: 80 };

  const widgets = config?.widgets ?? [];
  const full = fullscreenId ? widgets.find((w) => w.id === fullscreenId) : undefined;

  const toolbar = (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-bg-1 px-2 py-1">
      <span className="text-xs font-semibold text-text-2">图表</span>
      <span className="text-[11px] text-text-2">
        {config ? `${widgets.length} 个面板` : "未加载 charts.json"}
      </span>
      <button className={TOOLBTN} onClick={() => void ChartsAPI.ReloadCharts()} title="重新读取 charts.json">
        重载
      </button>
      <button
        className={TOOLBTN}
        onClick={() => void ChartsAPI.ResetChartsToDefault()}
        title="恢复默认布局（覆盖 charts.json）"
      >
        重置布局
      </button>
      <button
        className={TOOLBTN}
        onClick={() => void exportAllCsv(setExportError)}
        title="导出全部通道时间序列（缓冲区内，约 200s 历史）"
      >
        导出 CSV
      </button>
      {exportError && (
        <span className="truncate text-[11px] text-danger" title={exportError}>
          {exportError}
        </span>
      )}
      {error && (
        <span className="truncate text-[11px] text-danger" title={error}>
          {error}
        </span>
      )}
    </div>
  );

  if (!config) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {toolbar}
        <div className="flex flex-1 items-center justify-center text-sm text-text-2">
          打开工程后由 charts.json 决定图表布局
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {toolbar}
      <div className="min-h-0 flex-1 overflow-auto p-2">
        <div
          className="grid gap-2"
          style={{
            gridTemplateColumns: `repeat(${grid.cols}, minmax(0, 1fr))`,
            gridAutoRows: `${grid.row_height}px`,
          }}
        >
          {widgets.map((w) => (
            <div
              key={w.id}
              id={`widget-${w.id}`}
              className="flex min-h-0 flex-col overflow-hidden rounded-panel border border-border bg-bg-1"
              style={{
                gridColumn: `${w.position.x + 1} / span ${w.position.w}`,
                gridRow: `${w.position.y + 1} / span ${w.position.h}`,
              }}
            >
              <div className="flex shrink-0 items-center gap-2 border-b border-border/60 px-2 py-0.5 text-[11px] text-text-2">
                <span className="truncate font-semibold">{w.title}</span>
                <button
                  className="ml-auto text-text-2 hover:text-accent"
                  title="导出 PNG"
                  onClick={() => void exportWidgetPng(w, setExportError)}
                >
                  ⇩
                </button>
                <button
                  className="text-text-2 hover:text-accent"
                  title="全屏"
                  onClick={() => setFullscreenId(w.id)}
                >
                  ⛶
                </button>
              </div>
              <div className="min-h-0 flex-1">
                <WidgetBody w={w} />
              </div>
            </div>
          ))}
        </div>
      </div>

      {full && (
        <div className="fixed inset-0 z-50 flex flex-col bg-bg-0" onKeyDown={(e) => e.key === "Escape" && setFullscreenId(null)} tabIndex={0} ref={(el) => el?.focus()}>
          <div className="flex shrink-0 items-center gap-2 border-b border-border bg-bg-1 px-3 py-1 text-xs text-text-2">
            <span className="font-semibold text-accent">{full.title}</span>
            <button className={`${TOOLBTN} ml-auto`} onClick={() => setFullscreenId(null)}>
              ✕ 退出 (Esc)
            </button>
          </div>
          <div className="min-h-0 flex-1 p-2">
            <WidgetBody w={full} />
          </div>
        </div>
      )}
    </div>
  );
}

function WidgetBody({ w }: { w: WidgetCfg }) {
  const channels = useChartsStore((s) => s.channels);
  const bound = boundChannels(w, channels);
  switch (w.type) {
    case "line":
      return <LineWidget w={w} bound={bound} />;
    case "gauge":
      return <GaugeWidget w={w} bound={bound} />;
    case "indicator":
      return <IndicatorWidget w={w} bound={bound} />;
    case "field_table":
      return <FieldTableWidget />;
    default:
      return <div className="p-2 text-[11px] text-text-2">未知面板类型：{w.type}</div>;
  }
}

// ---------- 折线（uPlot） ----------

function LineWidget({ w, bound }: { w: WidgetCfg; bound: ChannelCfg[] }) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const plotRef = useRef<uPlot | null>(null);
  const version = useChartsStore((s) => s.version);
  const windowSec = w.window_seconds ?? 60;
  const fixedRange = w.y_axis_mode === "fixed" ? bound.find((c) => c.range)?.range : undefined;
  const names = useMemo(() => bound.map((c) => c.name), [bound]);

  const draw = () => {
    const plot = plotRef.current;
    if (!plot) return;
    const { xs, ys } = seriesBuffer.build(names, windowSec);
    plot.setData([xs, ...ys]);
  };

  // 建图（只在挂载与通道集合变化时重建）
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const opts: uPlot.Options = {
      width: Math.max(80, host.clientWidth),
      height: Math.max(60, host.clientHeight),
      padding: [8, 8, 4, 4],
      cursor: { show: true, drag: { x: false, y: false, setScale: false } },
      legend: { show: names.length > 1, live: false },
      scales: {
        x: { time: true },
        y: fixedRange ? { range: () => fixedRange as [number, number] } : {},
      },
      axes: [
        {
          stroke: AXIS_COLOR,
          grid: { stroke: GRID_COLOR },
          ticks: { stroke: GRID_COLOR },
          font: "10px ui-monospace, monospace",
        },
        {
          stroke: AXIS_COLOR,
          grid: { stroke: GRID_COLOR },
          ticks: { stroke: GRID_COLOR },
          font: "10px ui-monospace, monospace",
          size: 46,
        },
      ],
      series: [
        {},
        ...bound.map((c) => ({
          label: c.unit ? `${c.name} (${c.unit})` : c.name,
          stroke: channelColor(c.name, c.color),
          width: 1.5,
          points: { show: false },
        })),
      ],
    };

    const plot = new uPlot(opts, [[], ...names.map(() => [])], host);
    plotRef.current = plot;
    draw();

    const ro = new ResizeObserver(() => {
      const rect = host.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) plot.setSize({ width: rect.width, height: rect.height });
    });
    ro.observe(host);
    return () => {
      ro.disconnect();
      plot.destroy();
      plotRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [names.join("|"), fixedRange?.[0], fixedRange?.[1]]);

  // 数据更新：最多 30fps（串口合批本身就是 30/s，多余帧直接跳过）
  useEffect(() => {
    draw();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version]);

  return (
    <div className="relative h-full w-full">
      <div ref={hostRef} className="h-full w-full" />
      {bound.length === 0 && (
        <div className="absolute inset-0 flex items-center justify-center text-[11px] text-text-2">
          等待 emit_chart 数据…
        </div>
      )}
    </div>
  );
}

// ---------- 仪表（ECharts gauge） ----------

function GaugeWidget({ w, bound }: { w: WidgetCfg; bound: ChannelCfg[] }) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);
  const last = useChartsStore((s) => s.last);
  const ch = bound[0];
  const value = ch ? last[ch.name] : undefined;

  // 容器必须始终挂载：若在「无通道」时提前 return 占位元素，
  // 后续通道到位后 init effect 不会再跑，图表永远建不起来。
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const chart = echarts.init(host, undefined, { renderer: "canvas" });
    chartRef.current = chart;
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(host);
    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !ch) return;
    const [lo, hi] = ch.range ?? [0, 100];
    const val = value ?? lo;
    const alarm = ch.alarm_high != null && val >= ch.alarm_high;
    chart.setOption(
      {
        series: [
          {
            type: "gauge",
            min: lo,
            max: hi,
            radius: "92%",
            center: ["50%", "62%"],
            progress: { show: true, width: 8, itemStyle: { color: alarm ? "#F87171" : channelColor(ch.name, ch.color) } },
            axisLine: { lineStyle: { width: 8, color: [[1, "#1E293B"]] } },
            axisTick: { show: false },
            splitLine: { show: false },
            axisLabel: { show: false },
            pointer: { show: false },
            anchor: { show: false },
            title: {
              show: true,
              offsetCenter: [0, "32%"],
              color: "#94A3B8",
              fontSize: 10,
            },
            detail: {
              valueAnimation: false,
              offsetCenter: [0, "2%"],
              color: alarm ? "#F87171" : "#E2E8F0",
              fontSize: 16,
              formatter: (v: number) => `${v.toFixed(ch.display_digits ?? 1)}${ch.unit ?? ""}`,
            },
            data: [{ value: val, name: ch.name }],
          },
        ],
      },
      { notMerge: false },
    );
  }, [value, ch]);

  return (
    <div className="relative h-full w-full">
      <div ref={hostRef} className="h-full w-full" />
      {!ch && (
        <div className="absolute inset-0 flex items-center justify-center text-[11px] text-text-2">
          仪表未绑定通道
        </div>
      )}
    </div>
  );
}

// ---------- 指示灯 ----------

function IndicatorWidget({ w, bound }: { w: WidgetCfg; bound: ChannelCfg[] }) {
  const last = useChartsStore((s) => s.last);
  const ch = bound[0];
  if (!ch) {
    return <div className="flex h-full items-center justify-center text-[11px] text-text-2">指示灯未绑定通道</div>;
  }
  const value = last[ch.name];
  const onValue = ch.on_value ?? 1;
  const on = value != null && value === onValue;
  const unknown = value == null;
  return (
    <div className="flex h-full items-center gap-2 px-3">
      <span
        className={`inline-block h-3 w-3 rounded-full ${unknown ? "bg-text-2/40" : on ? "bg-rx" : "bg-danger"}`}
        style={on && !unknown ? { boxShadow: "0 0 8px #34D399" } : undefined}
      />
      <span className="truncate text-xs text-text-1">{ch.name}</span>
      <span className="ml-auto font-mono text-xs text-text-2">{unknown ? "—" : String(value)}</span>
    </div>
  );
}

// ---------- 字段表 ----------

function FieldTableWidget() {
  const last = useParserStore((s) => s.last);
  const fields = last?.fields ?? null;
  if (!fields || Object.keys(fields).length === 0) {
    return <div className="flex h-full items-center justify-center text-[11px] text-text-2">等待解析结果…</div>;
  }
  return (
    <div className="h-full overflow-auto px-2 py-1 text-[11px]">
      <table className="w-full">
        <tbody>
          {Object.entries(fields).map(([k, v]) => (
            <tr key={k} className="border-b border-border/30 last:border-0">
              <td className="w-1/3 truncate py-0.5 pr-2 text-text-2">{k}</td>
              <td className="truncate py-0.5 font-mono text-rx">
                {v !== null && typeof v === "object" ? JSON.stringify(v) : String(v)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ---------- 导出（PNG / CSV，系统保存对话框 → ExportAPI.SaveFile 落盘） ----------

const BG = "#0F131A";

// widget 容器内第一个 canvas 合成到工程底色上再导出（uPlot/ECharts 均为 canvas）。
async function exportWidgetPng(w: WidgetCfg, onError: (msg: string) => void) {
  try {
    onError("");
    const canvas = document.getElementById(`widget-${w.id}`)?.querySelector("canvas");
    if (!canvas) {
      onError("该面板没有可导出的画面");
      return;
    }
    const out = document.createElement("canvas");
    out.width = canvas.width;
    out.height = canvas.height;
    const ctx = out.getContext("2d");
    if (!ctx) {
      onError("无法创建导出画布");
      return;
    }
    ctx.fillStyle = BG;
    ctx.fillRect(0, 0, out.width, out.height);
    ctx.drawImage(canvas, 0, 0);
    const b64 = out.toDataURL("image/png").split(",")[1] ?? "";
    const picked = await saveFileDialog({
      title: "导出 PNG",
      defaultName: `${w.title}.png`,
      filters: [{ name: "PNG 图片", pattern: "*.png" }],
    });
    if (!picked) return;
    const path = picked.toLowerCase().endsWith(".png") ? picked : `${picked}.png`;
    await ExportAPI.SaveFile(path, b64);
  } catch (e: any) {
    onError(String(e?.message ?? e));
  }
}

// 全通道时间序列 CSV：第一列 timestamp（ISO8601），随后每通道一列，缺测留空。
async function exportAllCsv(onError: (msg: string) => void) {
  try {
    onError("");
    const names = seriesBuffer.knownChannels();
    if (names.length === 0) {
      onError("暂无通道数据可导出");
      return;
    }
    // windowSec 取超大值 = 导出缓冲区全部历史（约 200s）
    const { xs, ys } = seriesBuffer.build(names, 1e9);
    const rows: string[] = [`timestamp,${names.join(",")}`];
    for (let i = 0; i < xs.length; i++) {
      const cells = ys.map((col) => {
        const v = col[i];
        return Number.isNaN(v) ? "" : String(v);
      });
      rows.push(`${new Date(xs[i] * 1000).toISOString()},${cells.join(",")}`);
    }
    const content = rows.join("\n") + "\n";
    const picked = await saveFileDialog({
      title: "导出 CSV",
      defaultName: "channels.csv",
      filters: [{ name: "CSV 文件", pattern: "*.csv" }],
    });
    if (!picked) return;
    const path = picked.toLowerCase().endsWith(".csv") ? picked : `${picked}.csv`;
    const bytes = new TextEncoder().encode(content);
    let binary = "";
    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
    await ExportAPI.SaveFile(path, btoa(binary));
  } catch (e: any) {
    onError(String(e?.message ?? e));
  }
}
