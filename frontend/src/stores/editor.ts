import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as EditorAPI from "../../bindings/seriallens/internal/app/editorapi";

export interface ScriptFile {
  name: string;
  size: number;
  mod_time: string;
  active: boolean;
  has_error: boolean;
}

export interface Diagnostic {
  line: number;
  column: number;
  message: string;
  level: string;
}

export interface SaveReport {
  ok: boolean;
  elapsed_ms: number;
  compile_ms: number;
  load_ms: number;
  diagnostic?: Diagnostic;
}

export interface TestRunResult {
  ok: boolean;
  fields?: Record<string, unknown>;
  points?: { ch: string; v: number }[];
  duration_us?: number;
  diagnostic?: Diagnostic;
}

interface EditorState {
  files: ScriptFile[];
  current: string | null;
  content: string;
  savedContent: string;
  diag: Diagnostic | null;
  saving: boolean;
  lastSave: SaveReport | null;
  /** 外部改动但本地有未保存编辑时的提示；避免静默覆盖用户输入 */
  externalUpdate: { name: string; content: string } | null;
  error: string;
  testRun: TestRunResult | null;
  testRunning: boolean;

  dirty: () => boolean;
  refresh: () => Promise<void>;
  openFile: (name: string) => Promise<void>;
  setContent: (content: string) => void;
  save: () => Promise<void>;
  createScript: (name: string) => Promise<void>;
  duplicateScript: (name: string) => Promise<void>;
  renameScript: (name: string) => Promise<void>;
  deleteScript: (name: string) => Promise<void>;
  setActiveScript: (name: string) => Promise<void>;
  testRunFrame: (hex: string) => Promise<void>;
  applyExternal: () => void;
  dismissExternal: () => void;
  clearError: () => void;
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

export const useEditorStore = create<EditorState>((set, get) => {
  const refresh = async () => {
    try {
      const files = (await EditorAPI.ListScripts()) as unknown as ScriptFile[];
      set({ files, error: "" });
    } catch (e) {
      set({ files: [], error: errText(e) });
    }
  };

  Events.On("script:list", (ev: any) => {
    const files = ev.data?.files as ScriptFile[] | undefined;
    if (Array.isArray(files)) set({ files: [...files] });
  });

  Events.On("script:changed", (ev: any) => {
    const name = ev.data?.name as string | undefined;
    const content = ev.data?.content as string | undefined;
    const source = ev.data?.source as string | undefined;
    if (!name) return;
    if (source !== "external") return; // 自己写的，UI 已经是新的
    if (get().current === name && content != null) {
      if (get().content === get().savedContent) {
        // 本地没有未保存修改：直接跟随磁盘
        set({ content, savedContent: content, diag: null });
      } else {
        set({ externalUpdate: { name, content } });
      }
    }
    void refresh();
  });

  Events.On("script:error", (ev: any) => {
    const script = ev.data?.script as string | undefined;
    const d: Diagnostic = {
      line: Number(ev.data?.line ?? 0),
      column: Number(ev.data?.column ?? 0),
      message: String(ev.data?.error ?? "脚本错误"),
      level: "error",
    };
    if (!script || script === get().current) set({ diag: d });
  });

  Events.On("project:closed", () => {
    set({
      files: [],
      current: null,
      content: "",
      savedContent: "",
      diag: null,
      lastSave: null,
      externalUpdate: null,
      testRun: null,
      error: "",
    });
  });

  return {
    files: [],
    current: null,
    content: "",
    savedContent: "",
    diag: null,
    saving: false,
    lastSave: null,
    externalUpdate: null,
    error: "",
    testRun: null,
    testRunning: false,

    dirty: () => get().content !== get().savedContent,

    refresh,

    openFile: async (name) => {
      // 有未保存修改时先落盘再切：丢用户输入比多一次写盘严重得多。
      if (get().dirty()) await get().save();
      try {
        const res = (await EditorAPI.ReadScript(name)) as any;
        set({
          current: name,
          content: String(res?.content ?? ""),
          savedContent: String(res?.content ?? ""),
          diag: null,
          lastSave: null,
          externalUpdate: null,
          testRun: null,
          error: "",
        });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    setContent: (content) => set({ content }),

    save: async () => {
      const name = get().current;
      if (!name || get().saving) return;
      set({ saving: true });
      try {
        const res = (await EditorAPI.SaveScript(name, get().content)) as unknown as SaveReport;
        set({
          saving: false,
          lastSave: res,
          diag: res.diagnostic ?? null,
          savedContent: get().content,
          error: "",
        });
        void refresh();
      } catch (e) {
        set({ saving: false, error: errText(e) });
      }
    },

    createScript: async (name) => {
      try {
        await EditorAPI.CreateScript(name);
        await refresh();
        await get().openFile(name);
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    duplicateScript: async (name) => {
      const from = get().current;
      if (!from) return;
      try {
        await EditorAPI.DuplicateScript(from, name);
        await refresh();
        await get().openFile(name);
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    renameScript: async (name) => {
      const from = get().current;
      if (!from) return;
      try {
        await EditorAPI.RenameScript(from, name);
        await refresh();
        set({ current: name });
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    deleteScript: async (name) => {
      try {
        await EditorAPI.DeleteScript(name);
        await refresh();
        if (get().current === name) {
          const next = get().files.find((f) => !f.has_error);
          if (next) await get().openFile(next.name);
          else set({ current: null, content: "", savedContent: "" });
        }
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    setActiveScript: async (name) => {
      try {
        await EditorAPI.SetActiveScript(name);
        await refresh();
      } catch (e) {
        set({ error: errText(e) });
      }
    },

    testRunFrame: async (hex) => {
      if (get().testRunning) return;
      set({ testRunning: true });
      try {
        const res = (await EditorAPI.TestRunScript(get().content, hex)) as unknown as TestRunResult;
        set({ testRun: res, testRunning: false });
      } catch (e) {
        set({
          testRunning: false,
          testRun: { ok: false, diagnostic: { line: 0, column: 0, message: errText(e), level: "error" } },
        });
      }
    },

    applyExternal: () => {
      const ext = get().externalUpdate;
      if (!ext) return;
      set({ content: ext.content, savedContent: ext.content, externalUpdate: null, diag: null });
    },

    dismissExternal: () => set({ externalUpdate: null }),

    clearError: () => set({ error: "" }),
  };
});
