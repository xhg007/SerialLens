import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as SerialService from "../../bindings/seriallens/internal/app/serialservice";

export interface PortInfo {
  name: string;
  friendly: string;
  vid: string;
  pid: string;
}

export interface SerialConfigUI {
  port: string;
  baudrate: number;
  data_bits: number;
  stop_bits: number;
  parity: string;
  flow_control: string;
}

export interface ConnectionState {
  state: "disconnected" | "connected";
  bytesIn: number;
  bytesOut: number;
  ports: PortInfo[];
  config: SerialConfigUI;
  listPorts: () => Promise<void>;
  setConfig: (patch: Partial<SerialConfigUI>) => void;
  connect: () => Promise<void>;
  disconnect: () => Promise<void>;
  send: (data: string, hexMode: boolean) => Promise<void>;
}

export const useConnectionStore = create<ConnectionState>((set, get) => {
  Events.On("serial:status", (ev: any) => {
    set({
      state: (ev.data?.state ?? "disconnected") as ConnectionState["state"],
      bytesIn: Number(ev.data?.bytes_in ?? 0),
      bytesOut: Number(ev.data?.bytes_out ?? 0),
    });
  });

  return {
    state: "disconnected",
    bytesIn: 0,
    bytesOut: 0,
    ports: [],
    config: { port: "", baudrate: 115200, data_bits: 8, stop_bits: 1, parity: "N", flow_control: "none" },
    listPorts: async () => {
      set({ ports: ((await SerialService.ListPorts()) ?? []) as PortInfo[] });
    },
    setConfig: (patch) => set({ config: { ...get().config, ...patch } }),
    connect: async () => {
      await SerialService.Connect({ ...get().config });
    },
    disconnect: async () => {
      await SerialService.Disconnect();
    },
    send: async (data, hexMode) => {
      await SerialService.Send(data, hexMode);
    },
  };
});
