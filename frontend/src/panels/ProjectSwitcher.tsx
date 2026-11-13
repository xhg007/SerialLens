import { useEffect, useState } from "react";
import { baseName, pickDirectory } from "../lib/dialog";
import { useProjectStore } from "../stores/project";

const SMALL_BTN =
  "rounded-panel border border-border px-1.5 py-0.5 text-xs text-text-1 hover:border-accent hover:text-accent disabled:opacity-40 disabled:hover:border-border disabled:hover:text-text-1";

export function ProjectSwitcher() {
  const { current, recents, lastError, openProject, createProject, closeProject, refreshRecents, removeRecent, inspectDir } =
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

  // 新建：选目录 → 目录名当默认工程名（可改），并提前拦住「选到已有工程」
  const pickForCreate = async () => {
    setError("");
    try {
      const picked = await pickDirectory({ title: "选择工程目录（将在此目录创建工程）" });
      if (!picked) return;
      const info = await inspectDir(picked);
      if (!info.exists) {
        setError(`目录不可用：${picked}`);
        return;
      }
      if (info.is_project) {
        setError(`该目录已经是工程「${info.name || baseName(picked)}」，请用「打开工程」`);
        return;
      }
      setDir(picked);
      setName((prev) => prev || baseName(picked));
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  // 打开：选目录 → 确认是工程再打开（不然只会看到「read project.json」）
  const pickAndOpen = async () => {
    if (busy) return;
    setError("");
    try {
      const picked = await pickDirectory({ title: "打开 SerialLens 工程" });
      if (!picked) return;
      const info = await inspectDir(picked);
      if (!info.is_project) {
        setError(
          info.exists
            ? `所选目录不是 SerialLens 工程（缺 project.json）：${picked}`
            : `目录不存在：${picked}`,
        );
        return;
      }
      await doOpen(picked);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  const doCreate = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await createProject(dir, name.trim(), "");
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
          <button className={SMALL_BTN} onClick={() => void pickAndOpen()} disabled={busy} title="从磁盘选择已有工程">
            打开…
          </button>
          <button
            className={SMALL_BTN}
            onClick={() => setShowCreate((v) => !v)}
            title="新建工程"
          >
            + 新建
          </button>
          <button className={SMALL_BTN} onClick={() => setShowRecents((v) => !v)} title="最近工程">
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
          <button
            className="w-full rounded-panel border border-dashed border-border px-2 py-1 text-xs text-text-2 hover:border-accent hover:text-accent"
            onClick={() => void pickForCreate()}
          >
            {dir ? "重新选择目录…" : "选择工程目录…"}
          </button>
          <div className="truncate text-[11px] text-text-2" title={dir}>
            {dir || "将在此目录生成 project.json、scripts/、charts.json"}
          </div>
          <input
            className="w-full rounded-panel border border-border bg-bg-1 px-2 py-1 text-xs outline-none focus:border-accent"
            placeholder="工程名称"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <button
            className="w-full rounded-panel bg-accent px-2 py-1 text-xs font-semibold text-bg-0 hover:opacity-90 disabled:opacity-50"
            disabled={busy || !dir || !name.trim()}
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
