import { useEffect } from "react";
import { useConnectionStore } from "../stores/connection";

const BAUDRATES = [1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600, 1000000];
const PARITIES = ["N", "E", "O"];

export function ConnectionPanel() {
  const { state, config, ports, listPorts, setConfig, connect, disconnect, bytesIn, bytesOut } =
    useConnectionStore();

  useEffect(() => {
    void listPorts();
  }, [listPorts]);

  const connected = state === "connected";

  const cls =
    "w-full rounded-panel border border-border bg-bg-1 px-2 py-1 text-xs outline-none focus:border-accent disabled:opacity-50";

  return (
    <section className="rounded-panel border border-border bg-bg-2 p-2">
      <div className="mb-1 flex items-center justify-between">
        <h2 className="text-xs font-semibold text-text-2">连接</h2>
        <span className={`text-xs ${connected ? "text-rx" : "text-text-2"}`}>
          {connected ? "● 已连接" : "○ 未连接"}
        </span>
      </div>

      <div className="space-y-1">
        <select
          className={cls}
          value={config.port}
          disabled={connected}
          onChange={(e) => setConfig({ port: e.target.value })}
        >
          <option value="">选择串口…</option>
          {ports.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>

        <div className="grid grid-cols-2 gap-1">
          <select
            className={cls}
            value={config.baudrate}
            disabled={connected}
            onChange={(e) => setConfig({ baudrate: Number(e.target.value) })}
          >
            {BAUDRATES.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
          <select
            className={cls}
            value={config.parity}
            disabled={connected}
            onChange={(e) => setConfig({ parity: e.target.value })}
          >
            {PARITIES.map((p) => (
              <option key={p} value={p}>
                校验 {p}
              </option>
            ))}
          </select>
          <select
            className={cls}
            value={config.data_bits}
            disabled={connected}
            onChange={(e) => setConfig({ data_bits: Number(e.target.value) })}
          >
            {[7, 8].map((d) => (
              <option key={d} value={d}>
                数据位 {d}
              </option>
            ))}
          </select>
          <select
            className={cls}
            value={config.stop_bits}
            disabled={connected}
            onChange={(e) => setConfig({ stop_bits: Number(e.target.value) })}
          >
            {[1, 2].map((s) => (
              <option key={s} value={s}>
                停止位 {s}
              </option>
            ))}
          </select>
        </div>

        <button
          className={`w-full rounded-panel px-2 py-1 text-xs font-semibold ${
            connected
              ? "border border-danger bg-transparent text-danger hover:opacity-90"
              : "bg-accent text-bg-0 hover:opacity-90"
          }`}
          disabled={!connected && !config.port}
          onClick={() => void (connected ? disconnect() : connect())}
        >
          {connected ? "断开串口" : "打开串口"}
        </button>

        <div className="flex justify-between pt-1 text-[11px] text-text-2">
          <span>
            RX <span className="text-rx">{bytesIn}</span>
          </span>
          <span>
            TX <span className="text-tx">{bytesOut}</span>
          </span>
        </div>
      </div>
    </section>
  );
}
