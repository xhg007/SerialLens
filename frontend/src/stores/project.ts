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
  openProject: (dir: string) => Promise<void>;
  createProject: (dir: string, name: string, description: string) => Promise<void>;
  closeProject: () => Promise<void>;
  refreshRecents: () => Promise<void>;
  removeRecent: (path: string) => Promise<void>;
}

export const useProjectStore = create<ProjectState>((set) => {
  const refreshCurrent = async () => {
    const cur = await ProjectService.GetCurrentProject();
    if (cur && cur["ok"]) {
      set({ current: { path: cur["path"] as string, name: (cur["meta"] as any)?.Name ?? (cur["meta"] as any)?.name ?? "" } });
    } else {
      set({ current: null });
    }
  };

  Events.On("project:opened", () => void refreshCurrent());
  Events.On("project:closed", () => set({ current: null }));
  Events.On("recent:updated", (ev: any) => {
    set({ recents: (ev.data?.projects ?? []) as RecentEntry[] });
  });

  return {
    current: null,
    recents: [],
    openProject: async (dir) => {
      await ProjectService.OpenProject(dir);
      await refreshCurrent();
      set({ recents: ((await ProjectService.ListRecentProjects()) ?? []) as RecentEntry[] });
    },
    createProject: async (dir, name, description) => {
      await ProjectService.CreateProject(dir, name, description);
      await refreshCurrent();
      set({ recents: ((await ProjectService.ListRecentProjects()) ?? []) as RecentEntry[] });
    },
    closeProject: async () => {
      await ProjectService.CloseProject();
      set({ current: null });
    },
    refreshRecents: async () => {
      set({ recents: ((await ProjectService.ListRecentProjects()) ?? []) as RecentEntry[] });
    },
    removeRecent: async (path) => {
      await ProjectService.RemoveRecentProject(path);
    },
  };
});
