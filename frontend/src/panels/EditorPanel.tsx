import { useEffect, useRef, useState } from "react";
import * as EditorAPI from "../../bindings/seriallens/internal/app/editorapi";
import { editorOptions, initMonaco, monaco, registerApiDocs } from "../lib/monaco";
import { setContextEditor } from "../lib/activeEditor";
import { useEditorStore, type Diagnostic, type TestRunResult } from "../stores/editor";

const TOOLBTN =
  "rounded-panel border border-border px-2 py-0.5 text-xs text-text-2 hover:border-accent hover:text-accent disabled:opacity-40 disabled:hover:border-border disabled:hover:text-text-2";

export function EditorPanel() {
  const files = useEditorStore((s) => s.files);
  const current = useEditorStore((s) => s.current);
  const content = useEditorStore((s) => s.content);
  const savedContent = useEditorStore((s) => s.savedContent);
  const diag = useEditorStore((s) => s.diag);
  const saving = useEditorStore((s) => s.saving);
  const lastSave = useEditorStore((s) => s.lastSave);
  const external = useEditorStore((s) => s.externalUpdate);
  const error = useEditorStore((s) => s.error);
  const testRun = useEditorStore((s) => s.testRun);
  const testRunning = useEditorStore((s) => s.testRunning);

  const hostRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const modelsRef = useRef<Map<string, monaco.editor.ITextModel>>(new Map());

  const [prompt, setPrompt] = useState<{ mode: "create" | "rename" | "duplicate"; value: string } | null>(null);
  const [hex, setHex] = useState("01 03 00 00 00 0A");

  const dirty = content !== savedContent;

  // Monaco 初始化（含 Go 侧 API 清单 → 补全/悬停）
  useEffect(() => {
    initMonaco();
    EditorAPI.GetApiManifest()
      .then((docs) => registerApiDocs(docs as any))
      .catch(() => {});
  }, []);

  useEffect(() => {
    void useEditorStore.getState().refresh();
  }, []);

  // 工程打开后自动打开活动脚本（PRD 用例 1：main.lua 自动挂载）
  useEffect(() => {
    if (current || files.length === 0) return;
    const pick = files.find((f) => f.active) ?? files[0];
    void useEditorStore.getState().openFile(pick.name);
  }, [files, current]);

  // 建编辑器
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const ed = monaco.editor.create(host, { ...editorOptions(), value: "" });
    editorRef.current = ed;

    // 右键菜单只暴露复制/粘贴/全选；Monaco 的选区在 model 里，不能靠 DOM Selection。
    setContextEditor({
      getSelectedText: () => {
        const model = ed.getModel();
        const sel = ed.getSelection();
        return model && sel ? model.getValueInRange(sel) : "";
      },
      paste: (text) => {
        const model = ed.getModel();
        const sel = ed.getSelection();
        if (!model || !sel) return;
        ed.executeEdits("context-menu", [{ range: sel, text, forceMoveMarkers: true }]);
        ed.focus();
      },
      selectAll: () => {
        const model = ed.getModel();
        if (!model) return;
        ed.setSelection(model.getFullModelRange());
        ed.focus();
      },
    });

    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      void useEditorStore.getState().save();
    });

    const sub = ed.onDidChangeModelContent(() => {
      const model = ed.getModel();
      if (model) useEditorStore.getState().setContent(model.getValue());
    });

    return () => {
      sub.dispose();
      setContextEditor(null);
      ed.dispose();
      for (const m of modelsRef.current.values()) m.dispose();
      modelsRef.current.clear();
      editorRef.current = null;
    };
  }, []);

  // 文件切换：每个文件一个 model，保留各自的撤销栈
  useEffect(() => {
    const ed = editorRef.current;
    if (!ed || !current) return;
    let model = modelsRef.current.get(current);
    if (!model) {
      model = monaco.editor.createModel(content, "lua", monaco.Uri.file(current));
      modelsRef.current.set(current, model);
    } else if (model.getValue() !== content) {
      model.setValue(content);
    }
    if (ed.getModel() !== model) ed.setModel(model);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current]);

  // 内容被外部改变（采用磁盘版本 / 切换文件）时同步进 model
  useEffect(() => {
    const ed = editorRef.current;
    const model = ed?.getModel();
    if (!model) return;
    if (model.getValue() !== content) model.setValue(content);
  }, [content]);

  // 诊断 → 编辑器红波浪线
  useEffect(() => {
    const model = editorRef.current?.getModel();
    if (!model) return;
    if (!diag || diag.line <= 0) {
      monaco.editor.setModelMarkers(model, "seriallens", []);
      return;
    }
    const lineLength = model.getLineLength(Math.min(diag.line, model.getLineCount())) || 1;
    monaco.editor.setModelMarkers(model, "seriallens", [
      {
        severity: monaco.MarkerSeverity.Error,
        message: diag.message,
        startLineNumber: diag.line,
        startColumn: diag.column > 0 ? diag.column : 1,
        endLineNumber: diag.line,
        endColumn: Math.max(2, lineLength + 1),
      },
    ]);
    editorRef.current?.revealLineInCenter(diag.line);
  }, [diag, current]);

  const activeFile = files.find((f) => f.name === current);

  return (
    <div className="relative flex h-full min-h-0 flex-col bg-bg-1">
      {/* 工具栏 */}
      <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-border px-2 py-1">
        <span className="text-xs font-semibold text-text-2">脚本</span>
        <select
          value={current ?? ""}
          onChange={(e) => void useEditorStore.getState().openFile(e.target.value)}
          className="max-w-[160px] rounded-panel border border-border bg-bg-2 px-1 py-0.5 text-xs text-text-1 outline-none focus:border-accent"
        >
          {!current && <option value="">— 未选择 —</option>}
          {files.map((f) => (
            <option key={f.name} value={f.name}>
              {f.active ? "★ " : ""}
              {f.name}
              {f.has_error ? " (错误)" : ""}
            </option>
          ))}
        </select>
        <button className={TOOLBTN} onClick={() => setPrompt({ mode: "create", value: "script.lua" })}>
          新建
        </button>
        <button
          className={TOOLBTN}
          disabled={!current}
          onClick={() => setPrompt({ mode: "duplicate", value: current ? current.replace(/\.lua$/i, "-copy.lua") : "" })}
        >
          复制
        </button>
        <button
          className={TOOLBTN}
          disabled={!current}
          onClick={() => setPrompt({ mode: "rename", value: current ?? "" })}
        >
          重命名
        </button>
        <button className={TOOLBTN} disabled={!current} onClick={() => current && void useEditorStore.getState().deleteScript(current)}>
          删除
        </button>
        <button
          className={TOOLBTN}
          disabled={!current || activeFile?.active}
          title="设为活动脚本（解析管线立即切换）"
          onClick={() => current && void useEditorStore.getState().setActiveScript(current)}
        >
          设为活动
        </button>
        <button
          className={`${TOOLBTN} ml-auto ${dirty ? "border-warn text-warn" : ""}`}
          disabled={!current || saving}
          onClick={() => void useEditorStore.getState().save()}
          title="保存并热重载 (Ctrl+S)"
        >
          {saving ? "保存中…" : "保存"}
        </button>
      </div>

      {/* 状态行 */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border/60 px-2 py-0.5 text-[11px] text-text-2">
        {current ? (
          <>
            <span className={dirty ? "text-warn" : "text-rx"}>{dirty ? "● 未保存" : "已保存"}</span>
            {activeFile?.active && <span className="text-accent">活动脚本</span>}
            {lastSave && (
              <span>
                保存 {lastSave.elapsed_ms}ms
                {lastSave.load_ms > 0 && ` · 装载 ${lastSave.load_ms}ms`}
                {lastSave.compile_ms > 0 && ` · 校验 ${lastSave.compile_ms}ms`}
              </span>
            )}
          </>
        ) : (
          <span>打开工程后自动载入 scripts/main.lua</span>
        )}
        {error && (
          <span className="truncate text-danger" title={error}>
            {error}
          </span>
        )}
      </div>

      {/* 外部修改提示 */}
      {external && (
        <div className="flex shrink-0 items-center gap-2 border-b border-warn/40 bg-warn/10 px-2 py-1 text-[11px] text-warn">
          <span>磁盘上的 {external.name} 已被外部修改</span>
          <button className={TOOLBTN} onClick={() => useEditorStore.getState().applyExternal()}>
            采用磁盘版本
          </button>
          <button className={TOOLBTN} onClick={() => useEditorStore.getState().dismissExternal()}>
            保留我的修改
          </button>
        </div>
      )}

      {/* 编辑器 */}
      <div ref={hostRef} className="min-h-0 flex-1" />

      {/* 诊断 */}
      {diag && (
        <div className="flex shrink-0 items-start gap-2 border-t border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger">
          <span className="font-semibold">{diag.line > 0 ? `第 ${diag.line} 行` : "脚本错误"}</span>
          <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">{diag.message}</span>
          <button className={TOOLBTN} onClick={() => useEditorStore.setState({ diag: null })}>
            ✕
          </button>
        </div>
      )}

      {/* 试运行 */}
      <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-t border-border px-2 py-1 text-[11px]">
        <span className="text-text-2">试运行</span>
        <input
          value={hex}
          onChange={(e) => setHex(e.target.value)}
          spellCheck={false}
          placeholder="HEX 帧，如 01 03 00 00 00 0A"
          className="min-w-0 flex-1 rounded-panel border border-border bg-bg-2 px-1.5 py-0.5 font-mono text-[11px] text-text-1 outline-none focus:border-accent"
        />
        <button
          className={TOOLBTN}
          disabled={!current || testRunning}
          title="用编辑器当前内容试跑，不影响在线脚本与串口"
          onClick={() => void useEditorStore.getState().testRunFrame(hex)}
        >
          {testRunning ? "运行中…" : "运行"}
        </button>
        <TestRunSummary diag={diag} />
      </div>

      {testRun && <TestRunBar result={testRun} />}

      {prompt && (
        <NamePrompt
          title={prompt.mode === "create" ? "新建脚本" : prompt.mode === "rename" ? "重命名脚本" : "复制为"}
          value={prompt.value}
          onCancel={() => setPrompt(null)}
          onConfirm={(name) => {
            const st = useEditorStore.getState();
            if (prompt.mode === "create") void st.createScript(name);
            else if (prompt.mode === "rename") void st.renameScript(name);
            else void st.duplicateScript(name);
            setPrompt(null);
          }}
        />
      )}
    </div>
  );
}

function TestRunSummary({ diag }: { diag: Diagnostic | null }) {
  const testRun = useEditorStore((s) => s.testRun);
  if (!testRun) return null;
  if (!testRun.ok) return <span className="text-danger">试运行失败{diag?.line ? `（第 ${diag.line} 行）` : ""}</span>;
  const n = testRun.points?.length ?? 0;
  return (
    <span className="text-rx">
      {testRun.duration_us}µs · {n} 图表点
    </span>
  );
}

function TestRunBar({ result }: { result: TestRunResult }) {
  if (!result.ok) {
    return (
      <div className="shrink-0 border-t border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger">
        {result.diagnostic?.message ?? "试运行失败"}
      </div>
    );
  }
  const fields = Object.entries(result.fields ?? {});
  return (
    <div className="flex max-h-24 shrink-0 flex-wrap gap-1 overflow-auto border-t border-border px-2 py-1 text-[11px]">
      {fields.length === 0 && <span className="text-text-2">on_frame 返回空表</span>}
      {fields.map(([k, v]) => (
        <span key={k} className="rounded-panel bg-bg-2 px-1.5 py-0.5">
          <span className="text-text-2">{k}=</span>
          <span className="font-mono text-rx">{v !== null && typeof v === "object" ? JSON.stringify(v) : String(v)}</span>
        </span>
      ))}
      {(result.points ?? []).map((p, i) => (
        <span key={`${p.ch}-${i}`} className="rounded-panel bg-bg-2 px-1.5 py-0.5">
          <span className="text-text-2">{p.ch}=</span>
          <span className="font-mono text-accent">{p.v}</span>
        </span>
      ))}
    </div>
  );
}

function NamePrompt({
  title,
  value,
  onConfirm,
  onCancel,
}: {
  title: string;
  value: string;
  onConfirm: (name: string) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(value);
  return (
    <div className="absolute inset-0 z-30 flex items-center justify-center bg-bg-0/70">
      <div className="w-72 rounded-panel border border-border bg-bg-1 p-3">
        <div className="mb-2 text-xs text-text-1">{title}</div>
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onConfirm(name.trim());
            if (e.key === "Escape") onCancel();
          }}
          className="w-full rounded-panel border border-border bg-bg-2 px-2 py-1 font-mono text-xs outline-none focus:border-accent"
        />
        <div className="mt-2 flex justify-end gap-2">
          <button className={TOOLBTN} onClick={onCancel}>
            取消
          </button>
          <button className={TOOLBTN} onClick={() => onConfirm(name.trim())}>
            确定
          </button>
        </div>
      </div>
    </div>
  );
}
