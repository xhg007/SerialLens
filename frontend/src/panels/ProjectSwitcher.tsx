import { useEffect, useState } from "react";
import { useProjectStore } from "../stores/project";

export function ProjectSwitcher() {
  const { current, recents, lastError, openProject, createProject, closeProject, refreshRecents, removeRecent } =
    useProjectStore();
  const [showCreate, setShowCreate] = useState(false);
  const [dir, setDir] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [showRecents, setShowRecents] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void refreshRecents();
  }, [refreshRecents]);

  const doCreate = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await createProject(dir, name, "");
      setShowCreate(false);
      setDir("");
      setName("");
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const doOpen = async (path: string) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await openProject(path);
      setShowRecents(false);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="rounded-panel border border-border bg-bg-2 p-2">
      <div className="mb-1 flex items-center justify-between">
        <h2 className="text-xs font-semibold text-text-2">工程</h2>
        <div className="flex gap-1">
          <button
            className="rounded-panel border border-border px-1.5 py-0.5 text-xs text-text-1 hover:border-accent hover:text-accent"
            onClick={() => setShowCreate((v) => !v)}
            title="新建工程"
          >
            + 新建
          </button>
          <button
            className="rounded-panel border border-border px-1.5 py-0.5 text-xs text-text-1 hover:border-accent hover:text-accent"
            onClick={() => setShowRecents((v) => !v)}
            title="最近工程"
          >
            ▾
          </button>
        </div>
      </div>

      {current ? (
        <div className="flex items-center justify-between text-sm">
          <span className="truncate" title={current.path}>
            {current.name}
          </span>
          <button className="text-xs text-text-2 hover:text-danger" onClick={() => void closeProject()}>
            关闭
          </button>
        </div>
      ) : (
        <div className="text-xs text-text-2">未打开工程</div>
      )}

      {showCreate && (
        <div className="mt-2 space-y-1 border-t border-border pt-2">
          <input
            className="w-full rounded-panel border border-border bg-bg-1 px-2 py-1 text-xs outline-none focus:border-accent"
            placeholder="工程目录（绝对路径）"
            value={dir}
            onChange={(e) => setDir(e.target.value)}
          />
          <input
            className="w-full rounded-panel border border-border bg-bg-1 px-2 py-1 text-xs outline-none focus:border-accent"
            placeholder="工程名称"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <button
            className="w-full rounded-panel bg-accent px-2 py-1 text-xs font-semibold text-bg-0 hover:opacity-90 disabled:opacity-50"
            disabled={busy || !dir || !name}
            onClick={() => void doCreate()}
          >
            {busy ? "创建中…" : "创建工程"}
          </button>
        </div>
      )}

      {showRecents && (
        <ul className="mt-2 max-h-48 space-y-0.5 overflow-auto border-t border-border pt-2">
          {recents.length === 0 && <li className="text-xs text-text-2">暂无记录</li>}
          {recents.map((r) => (
            <li key={r.path} className="flex items-center justify-between gap-1">
              <button
                className={`min-w-0 flex-1 truncate rounded-panel px-1.5 py-1 text-left text-xs hover:bg-bg-0 ${
                  r.missing ? "text-text-2/50 line-through" : "text-text-1 hover:text-accent"
                }`}
                title={r.path}
                disabled={r.missing}
                onClick={() => void doOpen(r.path)}
              >
                {r.name || r.path}
              </button>
              <button
                className="text-xs text-text-2 hover:text-danger"
                title="移除记录"
                onClick={() => void removeRecent(r.path)}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}

      {(error || lastError) && <div className="mt-2 text-xs text-danger">{error || lastError}</div>}
    </section>
  );
}
