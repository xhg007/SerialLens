import { useEffect, useMemo, useRef, useState } from "react";
import { useConnectionStore } from "../stores/connection";
import { useParserStore } from "../stores/parser";
import { useTerminalStore } from "../stores/terminal";

const TOOLBTN =
  "rounded-panel border border-border px-2 py-0.5 text-xs text-text-2 hover:border-accent hover:text-accent";
const TOOLBTN_ON = "rounded-panel border border-accent px-2 py-0.5 text-xs text-accent";

// 渲染窗口：store 保留 5000 行历史，DOM 只渲染最近 1200 行。
// 实测满额 5000 行全量渲染时每批数据要 50~60ms 主线程（约 15fps、20s 内 46 个 long task）；
// 裁剪到 1200 行后回落到 60fps 量级。完整虚拟滚动留待 M3。
const VIEW_LINES = 1200;

const MAX_HISTORY = 20;
type CRLF = "none" | "cr" | "lf" | "crlf";

// HEX 串（容忍空格）→ 可读 ASCII；不可打印字节用 · 占位。
export function hexToAscii(hex: string): string {
  const clean = hex.replace(/[^0-9a-fA-F]/g, "");
  let out = "";
  for (let i = 0; i + 1 < clean.length; i += 2) {
    const b = parseInt(clean.slice(i, i + 2), 16);
    out += b >= 0x20 && b <= 0x7e ? String.fromCharCode(b) : "·";
  }
  return out;
}

// 应用 CR/LF 追加：文本模式追加字符，HEX 模式追加对应字节。
// sent = 实际发送的内容；display = 终端 TX 行显示的内容。
export function applyCrLf(
  input: string,
  hexMode: boolean,
  crlf: CRLF,
): { sent: string; display: string } {
  const suffix =
    crlf === "cr" ? "\r" : crlf === "lf" ? "\n" : crlf === "crlf" ? "\r\n" : "";
  if (hexMode) {
    const base = input.replace(/\s+/g, "").toUpperCase();
    const hexSuffix =
      crlf === "cr" ? "0D" : crlf === "lf" ? "0A" : crlf === "crlf" ? "0D0A" : "";
    const sent = (base + hexSuffix).replace(/(..)/g, "$1 ").trim();
    return { sent, display: sent };
  }
  const sent = input + suffix;
  return { sent, display: sent };
}

export function TerminalPanel() {
  const { lines, paused, fullscreen, togglePause, clear, toggleFullscreen } = useTerminalStore();
  const { state, send } = useConnectionStore();
  const [filter, setFilter] = useState("");
  const [input, setInput] = useState("");
  const [hexMode, setHexMode] = useState(true);
  const [rxMode, setRxMode] = useState<"hex" | "text">("hex");
  const [crlf, setCrlf] = useState<CRLF>("none");
  const [history, setHistory] = useState<{ text: string; hexMode: boolean; crlf: CRLF }[]>([]);
  const [showHistory, setShowHistory] = useState(false);
  const [error, setError] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  // 过滤同时命中 HEX 串与解码后的 ASCII 文本（文本 TX 行 hex 即原文，同样命中）。
  const filtered = useMemo(() => {
    if (!filter) return lines;
    const q = filter.toUpperCase();
    return lines.filter(
      (l) => l.hex.toUpperCase().includes(q) || hexToAscii(l.hex).toUpperCase().includes(q),
    );
  }, [lines, filter]);

  const hiddenCount = Math.max(0, filtered.length - VIEW_LINES);
  const visible = hiddenCount > 0 ? filtered.slice(hiddenCount) : filtered;

  // 自动滚动：暂停或用户上滚时停止
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stick.current && !paused) el.scrollTop = el.scrollHeight;
  }, [filtered, paused]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 30;
  };

  const doSend = async () => {
    setError("");
    if (!input) return;
    const { sent, display } = applyCrLf(input, hexMode, crlf);
    try {
      await send(sent, hexMode);
      useTerminalStore.getState().append({
        hex: display,
        ts: Date.now(),
        direction: "tx",
        mode: hexMode ? "hex" : "text",
      });
      setHistory((h) =>
        [{ text: input, hexMode, crlf }, ...h.filter((x) => x.text !== input)].slice(0, MAX_HISTORY),
      );
      setInput("");
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  const onHistoryPick = (h: { text: string; hexMode: boolean; crlf: CRLF }) => {
    setInput(h.text);
    setHexMode(h.hexMode);
    setCrlf(h.crlf);
    setShowHistory(false);
  };

  const fieldsBar = <FieldsBar />;

  const body = (
    <div
      ref={scrollRef}
      onScroll={onScroll}
      className="min-h-0 flex-1 overflow-auto bg-bg-0 px-2 py-1 text-sm leading-relaxed"
    >
      {filtered.length === 0 && <div className="text-text-2">等待数据…</div>}
      {hiddenCount > 0 && (
        <div className="text-text-2">
          … 已折叠更早的 {hiddenCount} 行（内存保留 5000 行，可用过滤检索）
        </div>
      )}
      {visible.map((l) => (
        <div key={l.id} className="term-row whitespace-pre">
          <span className="text-text-2">{ts(l.ts)}</span>
          <span className={`mx-2 ${l.direction === "rx" ? "text-rx" : "text-tx"}`}>
            {l.direction === "rx" ? "RX" : "TX"}
          </span>
          <span className={l.direction === "rx" ? "text-rx/90" : "text-tx/90"}>
            {l.direction === "rx" && rxMode === "text" ? hexToAscii(l.hex) : l.hex}
          </span>
        </div>
      ))}
    </div>
  );

  const toolbar = (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-bg-1 px-2 py-1">
      <span className="text-xs font-semibold text-text-2">终端</span>
      <button className={TOOLBTN} onClick={togglePause}>
        {paused ? "▶ 继续" : "⏸ 暂停"}
      </button>
      <button className={TOOLBTN} onClick={clear}>
        清空
      </button>
      <button
        className={rxMode === "text" ? TOOLBTN_ON : TOOLBTN}
        onClick={() => setRxMode(rxMode === "hex" ? "text" : "hex")}
        title="接收区显示：HEX / 文本（不可打印字节显示为 ·）"
      >
        {rxMode === "hex" ? "HEX" : "文本"}
      </button>
      <input
        className="w-40 rounded-panel border border-border bg-bg-2 px-2 py-0.5 text-xs outline-none focus:border-accent"
        placeholder="过滤 HEX/关键字"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      <button className={`${TOOLBTN} ml-auto`} onClick={toggleFullscreen} title="全屏 (Esc 退出)">
        ⛶ 全屏
      </button>
      {fieldsBar}
    </div>
  );

  if (fullscreen) {
    return (
      <div
        className="fixed inset-0 z-50 flex flex-col bg-bg-0"
        tabIndex={0}
        onKeyDown={(e) => e.key === "Escape" && toggleFullscreen()}
        ref={(el) => el?.focus()}
      >
        {toolbar}
        {body}
        <SendBar
          input={input}
          setInput={setInput}
          hexMode={hexMode}
          setHexMode={setHexMode}
          crlf={crlf}
          setCrlf={setCrlf}
          history={history}
          showHistory={showHistory}
          setShowHistory={setShowHistory}
          onHistoryPick={onHistoryPick}
          doSend={doSend}
          disabled={state !== "connected"}
          error={error}
        />
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      {toolbar}
      {body}
      <SendBar
        input={input}
        setInput={setInput}
        hexMode={hexMode}
        setHexMode={setHexMode}
        crlf={crlf}
        setCrlf={setCrlf}
        history={history}
        showHistory={showHistory}
        setShowHistory={setShowHistory}
        onHistoryPick={onHistoryPick}
        doSend={doSend}
        disabled={state !== "connected"}
        error={error}
      />
    </div>
  );
}

function SendBar(props: {
  input: string;
  setInput: (v: string) => void;
  hexMode: boolean;
  setHexMode: (v: boolean) => void;
  crlf: CRLF;
  setCrlf: (v: CRLF) => void;
  history: { text: string; hexMode: boolean; crlf: CRLF }[];
  showHistory: boolean;
  setShowHistory: (v: boolean) => void;
  onHistoryPick: (h: { text: string; hexMode: boolean; crlf: CRLF }) => void;
  doSend: () => void;
  disabled: boolean;
  error: string;
}) {
  const histRef = useRef<HTMLDivElement | null>(null);

  // 点击面板外关闭历史下拉
  useEffect(() => {
    if (!props.showHistory) return;
    const onDown = (e: MouseEvent) => {
      if (histRef.current && !histRef.current.contains(e.target as Node)) props.setShowHistory(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.showHistory]);

  return (
    <div className="flex shrink-0 items-center gap-2 border-t border-border bg-bg-1 px-2 py-1">
      <label className="flex items-center gap-1 text-xs text-text-2">
        <input
          type="checkbox"
          checked={props.hexMode}
          onChange={(e) => props.setHexMode(e.target.checked)}
          className="accent-accent"
        />
        HEX
      </label>
      <select
        className="rounded-panel border border-border bg-bg-2 px-1 py-0.5 text-xs outline-none focus:border-accent"
        value={props.crlf}
        onChange={(e) => props.setCrlf(e.target.value as CRLF)}
        title="发送时追加回车/换行"
        disabled={props.disabled}
      >
        <option value="none">无结尾</option>
        <option value="cr">+CR (\r)</option>
        <option value="lf">+LF (\n)</option>
        <option value="crlf">+CRLF (\r\n)</option>
      </select>
      <div ref={histRef} className="relative">
        <button
          className={TOOLBTN}
          onClick={() => props.setShowHistory(!props.showHistory)}
          title="发送历史（输入框内按 ↑ 回填上一条）"
          disabled={props.history.length === 0}
        >
          历史
        </button>
        {props.showHistory && props.history.length > 0 && (
          <div className="absolute bottom-full left-0 z-20 mb-1 max-h-56 w-72 overflow-auto rounded-panel border border-border bg-bg-1 shadow-lg">
            {props.history.map((h, i) => (
              <button
                key={`${h.text}-${i}`}
                className="block w-full truncate px-2 py-1 text-left font-mono text-xs text-text-1 hover:bg-bg-2 hover:text-accent"
                onClick={() => props.onHistoryPick(h)}
                title={`${h.hexMode ? "HEX" : "文本"}${h.crlf !== "none" ? ` +${h.crlf.toUpperCase()}` : ""}`}
              >
                <span className="mr-2 text-text-2">{h.hexMode ? "H" : "T"}</span>
                {h.text}
              </button>
            ))}
          </div>
        )}
      </div>
      <input
        className="min-w-0 flex-1 rounded-panel border border-border bg-bg-2 px-2 py-1 text-xs outline-none focus:border-accent"
        placeholder={props.hexMode ? "AA 06 01 02" : "文本内容"}
        disabled={props.disabled}
        value={props.input}
        onChange={(e) => props.setInput(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") props.doSend();
          if (e.key === "ArrowUp" && props.history.length > 0) {
            e.preventDefault();
            props.onHistoryPick(props.history[0]);
          }
        }}
      />
      <button
        className="rounded-panel bg-accent px-3 py-1 text-xs font-semibold text-bg-0 hover:opacity-90 disabled:opacity-40"
        disabled={props.disabled}
        onClick={props.doSend}
      >
        发送
      </button>
      {props.error && <span className="text-xs text-danger">{props.error}</span>}
    </div>
  );
}

function ts(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, "0")}`;
}

// FieldsBar 展示最近一帧的解析结果（M2：验证 Lua 脚本输出落到 UI 的最短路径）。
function FieldsBar() {
  const script = useParserStore((s) => s.script);
  const last = useParserStore((s) => s.last);
  if (!script || !last || !last.fields) return null;
  const entries = Object.entries(last.fields).slice(0, 10);
  return (
    <div className="flex w-full flex-wrap items-center gap-2 border-t border-border/60 pt-1 text-[11px] text-text-2">
      <span className="text-accent">字段</span>
      {entries.map(([k, v]) => (
        <span key={k} className="rounded-panel bg-bg-2 px-1.5 py-0.5">
          <span className="text-text-2">{k}=</span>
          <span className="text-rx">
            {v === null || typeof v !== "object" ? String(v) : JSON.stringify(v)}
          </span>
        </span>
      ))}
      <span className="ml-auto">{last.durationUs} µs</span>
    </div>
  );
}
