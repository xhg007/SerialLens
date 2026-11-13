import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import * as ProjectService from "../../bindings/seriallens/internal/app/projectservice";

export interface RecentEntry {
  path: string;
  name: string;
  last_opened: string;
  pinned: boolean;
  missing: boolean;
}

export interface ProjectState {
  current: { path: string; name: string } | null;
  recents: RecentEntry[];
  lastError: string;
  openProject: (dir: string) => Promise<void>;
  createProject: (dir: string, name: string, description: string) => Promise<void>;
  closeProject: () => Promise<void>;
  refreshRecents: () => Promise<void>;
  removeRecent: (path: string) => Promise<void>;
  /** 与后端对账当前工程（页面重载 / HMR 后恢复，事件补不回来时兜底） */
  syncCurrent: () => Promise<void>;
}

// HMR 安全：模块重新求值时先取消旧订阅，避免 dev 下事件双触发。
const unsubs: (() => void)[] = [];

export const useProjectStore = create<ProjectState>((set, get) => {
  const refreshCurrent = async () => {
    const cur = await ProjectService.GetCurrentProject();
    if (cur && cur["ok"]) {
      // Go 结构体 JSON 一律 snake_case（Wails 序列化约定）
      set({ current: { path: cur["path"] as string, name: (cur["meta"] as any)?.name ?? "" } });
    } else {
      set({ current: null });
    }
  };

  unsubs.push(
    Events.On("project:opened", () => void refreshCurrent()),
    Events.On("project:closed", () => set({ current: null })),
    Events.On("recent:updated", (ev: any) => {
      set({ recents: (ev.data?.projects ?? []) as RecentEntry[] });
    }),
    Events.On("project:error", (ev: any) => {
      set({ lastError: String(ev.data?.message ?? "工程操作失败") });
    }),
  );

  const refreshRecents = async () => {
    set({ recents: ((await ProjectService.ListRecentProjects()) ?? []) as RecentEntry[] });
  };

  return {
    current: null,
    recents: [],
    lastError: "",
    openProject: async (dir) => {
      await ProjectService.OpenProject(dir);
      await refreshCurrent();
      await refreshRecents();
    },
    createProject: async (dir, name, description) => {
      await ProjectService.CreateProject(dir, name, description);
      await refreshCurrent();
      await refreshRecents();
    },
    closeProject: async () => {
      await ProjectService.CloseProject();
      set({ current: null });
    },
    refreshRecents,
    removeRecent: async (path) => {
      await ProjectService.RemoveRecentProject(path);
      // 事件之外再兜底刷新一次，避免事件丢失导致 UI 不同步
      await get().refreshRecents();
    },
    syncCurrent: refreshCurrent,
  };
});

if (import.meta.hot) {
  import.meta.hot.dispose(() => unsubs.splice(0).forEach((off) => off()));
}
