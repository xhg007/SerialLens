import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as AIAPI from "../../bindings/seriallens/internal/app/aiapi";
import * as EditorAPI from "../../bindings/seriallens/internal/app/editorapi";
import { useEditorStore } from "./editor";

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

/** 一次工具调用的 UI 记录（时间线用）。 */
export interface AIToolCall {
  callId: string;
  name: string;
  args?: unknown;
  ok?: boolean;
  durationMs?: number;
  result?: unknown;
  error?: string;
  running: boolean;
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
  tools?: AIToolCall[];
  rounds?: number;
}

/** 后端发来的确认请求（阻塞等待前端决定）。 */
export interface AIConfirm {
  id: string;
  runId: string;
  kind: "script_draft" | "context_hints" | "generic";
  title: string;
  description: string;
  target?: string;
  content?: string;
  diff?: string;
}

export interface AISessionSummary {
  id: string;
  title: string;
  model: string;
  updated: number;
  size: number;
  turns: number;
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
  confirm: AIConfirm | null;
  confirming: boolean;
  sessionId: string;
  sessions: AISessionSummary[];
  task: "script" | "charts";

  hydrate: () => Promise<void>;
  saveSettings: (patch: AISettingsPatch) => Promise<void>;
  testConnection: (patch: AISettingsPatch) => Promise<void>;
  send: (text: string) => Promise<void>;
  cancel: () => Promise<void>;
  clear: () => Promise<void>;
  dismissError: () => void;
  resolveConfirm: (approved: boolean) => Promise<void>;
  refreshSessions: () => Promise<void>;
  loadSession: (id: string) => Promise<void>;
  setTask: (t: "script" | "charts") => void;
}

function errText(e: unknown): string {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  return String(e);
}

function newId(): string {
  return `m-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
}

/** 当前活动脚本名（后端没给 target 时兜底查询）。 */
async function activeScriptName(): Promise<string> {
  const list = (await EditorAPI.ListScripts()) as any[];
  return (list ?? []).find((f) => f?.active)?.name ?? "main.lua";
}

export const useAIStore = create<AIState>((set, get) => {
  const patchMessage = (id: string, fn: (m: AIMessage) => AIMessage) => {
    set({ messages: get().messages.map((m) => (m.id === id ? fn(m) : m)) });
  };
  const patchTool = (id: string, callId: string, fn: (t: AIToolCall) => AIToolCall) => {
    patchMessage(id, (m) => ({
      ...m,
      tools: (m.tools ?? []).map((t) => (t.callId === callId ? fn(t) : t)),
    }));
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
        { id, role: "assistant", content: "", ts: Date.now(), streaming: true, tools: [] },
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
        messages: [
          ...get().messages,
          { id, role: "assistant", content: "", ts: Date.now(), streaming: true, tools: [] },
        ],
      });
    }
    patchMessage(id, (m) => ({
      ...m,
      content: m.content + text,
      reasoning: (m.reasoning ?? "") + reasoning,
      streaming: true,
    }));
  });

  Events.On("ai:tool_call", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const call: AIToolCall = {
      callId: String(ev.data?.call_id ?? ""),
      name: String(ev.data?.name ?? "tool"),
      args: ev.data?.args,
      running: true,
    };
    if (!get().messages.some((m) => m.id === id)) return;
    patchMessage(id, (m) => ({ ...m, tools: [...(m.tools ?? []), call] }));
  });

  Events.On("ai:tool_result", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const callId = String(ev.data?.call_id ?? "");
    patchTool(id, callId, (t) => ({
      ...t,
      ok: Boolean(ev.data?.ok),
      durationMs: Number(ev.data?.duration_ms ?? 0),
      result: ev.data?.result,
      error: ev.data?.error ? String(ev.data.error) : undefined,
      running: false,
    }));
  });

  Events.On("ai:confirm", (ev: any) => {
    const d = ev.data ?? {};
    set({
      confirm: {
        id: String(d.id ?? ""),
        runId: String(d.run_id ?? ""),
        kind: (d.kind ?? "generic") as AIConfirm["kind"],
        title: String(d.title ?? "需要确认"),
        description: String(d.description ?? ""),
        target: d.target ? String(d.target) : undefined,
        content: d.content ? String(d.content) : undefined,
        diff: d.diff ? String(d.diff) : undefined,
      },
      confirming: false,
    });
  });

  Events.On("ai:done", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const content = String(ev.data?.content ?? "");
    const reasoning = String(ev.data?.reasoning ?? "");
    const canceled = Boolean(ev.data?.canceled);
    const usage = ev.data?.usage as AIMessage["usage"];
    const elapsedMs = Number(ev.data?.elapsed_ms ?? 0);
    const firstMs = Number(ev.data?.first_ms ?? 0);
    const rounds = Number(ev.data?.rounds ?? 0);
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
      rounds,
      tools: (m.tools ?? []).map((t) => (t.running ? { ...t, running: false } : t)),
    }));
    void get().refreshSessions();
  });

  Events.On("ai:error", (ev: any) => {
    const id = String(ev.data?.id ?? "");
    const message = String(ev.data?.message ?? "未知错误");
    set({ busy: false, runId: null, error: message });
    patchMessage(id, (m) => ({ ...m, streaming: false, error: message }));
  });

  Events.On("ai:session", (ev: any) => {
    set({ sessionId: String(ev.data?.session_id ?? "") });
    void get().refreshSessions();
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
    confirm: null,
    confirming: false,
    sessionId: "",
    sessions: [],
    task: "script",

    hydrate: async () => {
      try {
        const [providers, settings, conv, info] = await Promise.all([
          AIAPI.GetAIProviders(),
          AIAPI.GetAISettings(),
          AIAPI.GetAIConversation(),
          AIAPI.GetAISessionInfo(),
        ]);
        set({
          providers: providers as unknown as AIProvider[],
          settings: settings as unknown as AISettingsView,
          busy: Boolean((settings as any)?.running),
          sessionId: String((info as any)?.id ?? ""),
          messages: (conv as unknown as any[]).map((m) => ({
            id: newId(),
            role: m.role,
            content: m.content ?? "",
            reasoning: m.reasoning ?? "",
            ts: Number(m.ts ?? Date.now()),
          })),
        });
        void get().refreshSessions();
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
        await AIAPI.SendAIMessageAs(t, get().task);
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
        set({ messages: [], error: "", confirm: null, sessionId: "" });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    dismissError: () => set({ error: "" }),

    // 用户决定：先回填 Go 侧（阻塞的工具立刻恢复），再按 kind 执行本地动作。
    resolveConfirm: async (approved) => {
      const c = get().confirm;
      if (!c || get().confirming) return;
      set({ confirming: true });
      try {
        await AIAPI.ResolveAIConfirm(c.id, approved);
        if (approved && c.kind === "script_draft" && c.content) {
          // 只有用户确认才写正式脚本目录（PRD §4.1：AI 不越权）
          const active = c.target || (await activeScriptName());
          const report = (await EditorAPI.SaveScript(active, c.content)) as any;
          // 内容已落盘：立刻让编辑器换掉旧内容（事件也会到，这里保证同一帧生效）
          useEditorStore.getState().adoptExternal(active, c.content);
          if (report && report.ok === false) {
            const msg = report.diagnostic?.message ?? "未知错误";
            set({ error: `已写入 ${active}，但编译失败：${msg}` });
          }
        }
        set({ confirm: null, confirming: false });
        void get().refreshSessions();
      } catch (e) {
        set({ confirming: false, error: errText(e) });
      }
    },

    refreshSessions: async () => {
      try {
        const list = await AIAPI.ListAISessions();
        set({ sessions: (list ?? []) as unknown as AISessionSummary[] });
      } catch {
        /* 没有工程时静默 */
      }
    },

    loadSession: async (id) => {
      try {
        const res: any = await AIAPI.LoadAISession(id);
        const events: any[] = res?.events ?? [];
        const msgs: AIMessage[] = [];
        for (const e of events) {
          if (e.type === "message" && e.content && (e.role === "user" || e.role === "assistant")) {
            msgs.push({ id: newId(), role: e.role, content: e.content, ts: Date.parse(e.ts) || Date.now() });
          }
        }
        set({ messages: msgs, sessionId: String(res?.id ?? id), error: "" });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    setTask: (t) => set({ task: t }),
  };
});
