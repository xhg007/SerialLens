import { useEffect, useRef, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { getContextEditor } from "../lib/activeEditor";

// 全局右键菜单：接管 WebView2 原生菜单（开发模式里带 Inspect），只保留复制/粘贴/全选。
// 原生菜单在 JS 里 preventDefault 即可压掉（dev 实测）；生产构建另外在 main.go
// 设置了 DefaultContextMenuDisabled，双保险。

type MenuKind = "field" | "editor" | "selection";

interface MenuState {
  x: number;
  y: number;
  kind: MenuKind;
  field: HTMLInputElement | HTMLTextAreaElement | null;
  canCopy: boolean;
  canPaste: boolean;
  canSelectAll: boolean;
}

const MENU_W = 132;
const MENU_H = 96;

export function ContextMenu() {
  const [menu, setMenu] = useState<MenuState | null>(null);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onContextMenu = (e: MouseEvent) => {
      // 先压掉原生菜单，再决定是否弹自定义菜单。
      e.preventDefault();
      e.stopPropagation();

      const target = e.target as HTMLElement | null;
      if (!target) return;

      const field = target.closest<HTMLInputElement | HTMLTextAreaElement>("input, textarea");
      const editable = field && !field.disabled && !field.readOnly ? field : null;
      const inEditor = Boolean(target.closest(".monaco-editor"));
      const editorText = inEditor ? (getContextEditor()?.getSelectedText() ?? "") : "";
      const fieldText = editable
        ? editable.value.slice(editable.selectionStart ?? 0, editable.selectionEnd ?? 0)
        : "";
      const selectionText = !editable && !inEditor ? (window.getSelection()?.toString() ?? "") : "";

      const canCopy = Boolean(editorText || fieldText || selectionText);
      const canPaste = Boolean(editable || inEditor);
      const canSelectAll = Boolean(editable || inEditor);
      // 没有任何可执行动作时静默吞掉右键，不弹空白菜单。
      if (!canCopy && !canPaste && !canSelectAll) return;

      setMenu({
        x: Math.max(4, Math.min(e.clientX, window.innerWidth - MENU_W - 4)),
        y: Math.max(4, Math.min(e.clientY, window.innerHeight - MENU_H - 4)),
        kind: inEditor ? "editor" : editable ? "field" : "selection",
        field: editable,
        canCopy,
        canPaste,
        canSelectAll,
      });
    };

    window.addEventListener("contextmenu", onContextMenu, true);
    return () => window.removeEventListener("contextmenu", onContextMenu, true);
  }, []);

  useEffect(() => {
    if (!menu) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && e.target instanceof Node && ref.current.contains(e.target)) return;
      setMenu(null);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenu(null);
    };
    const close = () => setMenu(null);
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  const copy = async () => {
    if (!menu) return;
    const text =
      menu.kind === "editor"
        ? (getContextEditor()?.getSelectedText() ?? "")
        : menu.field
          ? menu.field.value.slice(menu.field.selectionStart ?? 0, menu.field.selectionEnd ?? 0)
          : (window.getSelection()?.toString() ?? "");
    setMenu(null);
    if (!text) return;
    try {
      await Clipboard.SetText(text);
    } catch {
      await navigator.clipboard?.writeText(text);
    }
  };

  const paste = async () => {
    if (!menu) return;
    const { kind, field } = menu;
    setMenu(null);
    let text = "";
    try {
      text = await Clipboard.Text();
    } catch {
      text = (await navigator.clipboard?.readText()) ?? "";
    }
    if (!text) return;
    if (kind === "editor") {
      getContextEditor()?.paste(text);
      return;
    }
    if (field) insertText(field, text);
  };

  const selectAll = () => {
    if (!menu) return;
    if (menu.kind === "editor") getContextEditor()?.selectAll();
    else if (menu.field) {
      menu.field.focus();
      menu.field.select();
    }
    setMenu(null);
  };

  if (!menu) return null;
  return (
    <div
      ref={ref}
      role="menu"
      className="fixed z-[100] min-w-32 rounded-panel border border-border bg-bg-1 py-1 text-xs text-text-1 shadow-lg"
      style={{ left: menu.x, top: menu.y }}
      onMouseDown={(e) => e.preventDefault()}
      onContextMenu={(e) => e.preventDefault()}
    >
      <MenuItem disabled={!menu.canCopy} onClick={() => void copy()}>
        复制
      </MenuItem>
      <MenuItem disabled={!menu.canPaste} onClick={() => void paste()}>
        粘贴
      </MenuItem>
      <MenuItem disabled={!menu.canSelectAll} onClick={selectAll}>
        全选
      </MenuItem>
    </div>
  );
}

function MenuItem({
  disabled,
  onClick,
  children,
}: {
  disabled: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      role="menuitem"
      disabled={disabled}
      onClick={onClick}
      className="block w-full px-3 py-1 text-left hover:bg-bg-2 disabled:text-text-2/40 disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}

// 受控输入框必须走原生 value setter 再派发 input，React 才会把新值吃进 state。
function insertText(el: HTMLInputElement | HTMLTextAreaElement, text: string) {
  el.focus();
  const start = el.selectionStart ?? el.value.length;
  const end = el.selectionEnd ?? start;
  const next = el.value.slice(0, start) + text + el.value.slice(end);
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, "value")?.set?.call(el, next);
  el.selectionStart = el.selectionEnd = start + text.length;
  el.dispatchEvent(new Event("input", { bubbles: true }));
}
