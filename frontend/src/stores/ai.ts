import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as AIAPI from "../../bindings/seriallens/internal/app/aiapi";

export interface AIProvider {
  id: string;
  name: string;
  base_url: string;
  models: string[];
  key_url?: string;
  doc_url?: string;
  note?: string;
  needs_key: boolean;
}

export interface AISettingsView {
  provider: string;
  base_url: string;
  model: string;
  temperature: number;
  max_tokens: number;
  timeout_sec: number;
  enabled: boolean;
  system_extra: string;
  has_key: boolean;
  key_hint: string;
  key_from_env: boolean;
  ready: boolean;
  warning?: string;
  running?: boolean;
  messages?: number;
}

export interface AIMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  reasoning?: string;
  ts: number;
  streaming?: boolean;
  canceled?: boolean;
  error?: string;
  usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number };
  elapsedMs?: number;
  firstMs?: number;
}

export interface AISettingsPatch {
  provider?: string;
  base_url?: string;
  model?: string;
  api_key?: string;
  temperature?: number;
  max_tokens?: number;
  timeout_sec?: number;
  system_extra?: string;
  enabled?: boolean;
  clear_key?: boolean;
}

interface AIState {
  settings: AISettingsView | null;
  providers: AIProvider[];
  messages: AIMessage[];
  busy: boolean;
  error: string;
  testing: boolean;
  testResult: string;
  runId: string | null;

  hydrate: () => Promise<void>;
  saveSettings: (patch: AISettingsPatch) => Promise<void>;
  testConnection: (patch: AISettingsPatch) => Promise<void>;
  send: (text: string) => Promise<void>;
  cancel: () => Promise<void>;
  clear: () => Promise<void>;
  dismissError: () => void;
}

function errText(e: unknown): string {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  return String(e);
}

function newId(): string {
  return `m-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
}

export const useAIStore = create<AIState>((set, get) => {
  const patchMessage = (id: string, fn: (m: AIMessage) => AIMessage) => {
    set({ messages: get().messages.map((m) => (m.id === id ? fn(m) : m)) });
  };

  // 事件契约见 internal/events/events.go（EvAI*），单一事实来源在 Go 侧。
  Events.On("ai:status", (ev: any) => {
    const d = ev.data ?? {};
    const cur = get().settings;
    if (!cur) return;
    set({
      settings: { ...cur, ...d } as AISettingsView,
      busy: Boolean(d.running),
    });
  });

  Events.On("ai:start", (ev: any) => {
    const id = String(ev.data?.id ?? newId());
    const exists = get().messages.some((m) => m.id === id);
    if (exists) return;
    set({
      busy: true,
      runId: id,
      error: "",
      messages: [
        ...get().messages,
        { id, role: "assistant", content: "", ts: Date.now(), streaming: true },
      ],
    });
  });

  Events.On("ai:delta", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const text = String(ev.data?.text ?? "");
    const reasoning = String(ev.data?.reasoning ?? "");
    if (!text && !reasoning) return;
    // 事件可能比 ai:start 先到（同一帧内），兜底新建
    if (!get().messages.some((m) => m.id === id && m.role === "assistant")) {
      set({
        messages: [...get().messages, { id, role: "assistant", content: "", ts: Date.now(), streaming: true }],
      });
    }
    patchMessage(id, (m) => ({
      ...m,
      content: m.content + text,
      reasoning: (m.reasoning ?? "") + reasoning,
      streaming: true,
    }));
  });

  Events.On("ai:done", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const content = String(ev.data?.content ?? "");
    const reasoning = String(ev.data?.reasoning ?? "");
    const canceled = Boolean(ev.data?.canceled);
    const usage = ev.data?.usage as AIMessage["usage"];
    const elapsedMs = Number(ev.data?.elapsed_ms ?? 0);
    const firstMs = Number(ev.data?.first_ms ?? 0);
    set({ busy: false, runId: null });
    if (!get().messages.some((m) => m.id === id)) return;
    // done 里的 content 是权威全文：覆盖式写入，避免增量丢帧导致重复/缺失
    patchMessage(id, (m) => ({
      ...m,
      content: content || m.content,
      reasoning: reasoning || m.reasoning,
      streaming: false,
      canceled,
      usage,
      elapsedMs,
      firstMs,
    }));
  });

  Events.On("ai:error", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const message = String(ev.data?.message ?? "未知错误");
    set({ busy: false, runId: null, error: message });
    patchMessage(id, (m) => ({ ...m, streaming: false, error: message }));
  });

  return {
    settings: null,
    providers: [],
    messages: [],
    busy: false,
    error: "",
    testing: false,
    testResult: "",
    runId: null,

    hydrate: async () => {
      try {
        const [providers, settings, conv] = await Promise.all([
          AIAPI.GetAIProviders(),
          AIAPI.GetAISettings(),
          AIAPI.GetAIConversation(),
        ]);
        set({
          providers: providers as unknown as AIProvider[],
          settings: settings as unknown as AISettingsView,
          busy: Boolean((settings as any)?.running),
          messages: (conv as unknown as any[]).map((m) => ({
            id: newId(),
            role: m.role,
            content: m.content ?? "",
            reasoning: m.reasoning ?? "",
            ts: Number(m.ts ?? Date.now()),
          })),
        });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    saveSettings: async (patch) => {
      try {
        const view = await AIAPI.SaveAISettings(patch as any);
        set({ settings: view as unknown as AISettingsView, error: "" });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    testConnection: async (patch) => {
      set({ testing: true, testResult: "" });
      try {
        const res: any = await AIAPI.TestAIConnection(patch as any);
        const via = res?.via === "models" ? "模型列表" : "短对话";
        const extra = res?.models?.length ? ` · 可用模型 ${res.models.length} 个` : "";
        set({
          testing: false,
          testResult: `连接成功（${via}）· ${res?.latency_ms ?? 0}ms${extra}`,
        });
      } catch (e) {
        set({ testing: false, testResult: `连接失败：${errText(e)}` });
      }
    },

    send: async (text) => {
      const t = text.trim();
      if (!t || get().busy) return;
      const user: AIMessage = { id: newId(), role: "user", content: t, ts: Date.now() };
      set({ messages: [...get().messages, user], error: "" });
      try {
        await AIAPI.SendAIMessage(t);
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    cancel: async () => {
      try {
        await AIAPI.CancelAI();
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    clear: async () => {
      try {
        await AIAPI.ClearAIConversation();
        set({ messages: [], error: "" });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    dismissError: () => set({ error: "" }),
  };
});
