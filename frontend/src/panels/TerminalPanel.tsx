import { useEffect, useMemo, useRef, useState } from "react";
import { useConnectionStore } from "../stores/connection";
import { useTerminalStore } from "../stores/terminal";

const TOOLBTN =
  "rounded-panel border border-border px-2 py-0.5 text-xs text-text-2 hover:border-accent hover:text-accent";

export function TerminalPanel() {
  const { lines, paused, fullscreen, togglePause, clear, toggleFullscreen } = useTerminalStore();
  const { state, send } = useConnectionStore();
  const [filter, setFilter] = useState("");
  const [input, setInput] = useState("");
  const [hexMode, setHexMode] = useState(true);
  const [error, setError] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  const filtered = useMemo(() => {
    if (!filter) return lines;
    return lines.filter((l) => l.hex.toUpperCase().includes(filter.toUpperCase()));
  }, [lines, filter]);

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
    try {
      await send(input, hexMode);
      useTerminalStore.getState().append({
        hex: input.toUpperCase().replace(/\s+/g, " "),
        ts: Date.now(),
        direction: "tx",
      });
      setInput("");
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  const body = (
    <div
      ref={scrollRef}
      onScroll={onScroll}
      className="min-h-0 flex-1 overflow-auto bg-bg-0 px-2 py-1 text-sm leading-relaxed"
    >
      {filtered.length === 0 && <div className="text-text-2">等待数据…</div>}
      {filtered.map((l, i) => (
        <div key={i} className="whitespace-pre">
          <span className="text-text-2">{ts(l.ts)}</span>
          <span className={`mx-2 ${l.direction === "rx" ? "text-rx" : "text-tx"}`}>
            {l.direction === "rx" ? "RX" : "TX"}
          </span>
          <span className={l.direction === "rx" ? "text-rx/90" : "text-tx/90"}>{l.hex}</span>
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
      <input
        className="w-40 rounded-panel border border-border bg-bg-2 px-2 py-0.5 text-xs outline-none focus:border-accent"
        placeholder="过滤 HEX/关键字"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      <button className={`${TOOLBTN} ml-auto`} onClick={toggleFullscreen} title="全屏 (Esc 退出)">
        ⛶ 全屏
      </button>
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
  doSend: () => void;
  disabled: boolean;
  error: string;
}) {
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
      <input
        className="min-w-0 flex-1 rounded-panel border border-border bg-bg-2 px-2 py-1 text-xs outline-none focus:border-accent"
        placeholder={props.hexMode ? "AA 06 01 02" : "文本内容"}
        disabled={props.disabled}
        value={props.input}
        onChange={(e) => props.setInput(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && props.doSend()}
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
