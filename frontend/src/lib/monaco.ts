// Monaco 按需装配：只引入编辑器核心 + 实际用到的功能（补全/悬停/查找/注释/括号/多光标）
// 与 Lua 语言定义。整包 monaco 会把 60+ 语言和 TS 语言服务一起打进产物（约 5MB）。
import * as monaco from "monaco-editor/editor/editor.api.js";
import "monaco-editor/editor/contrib/suggest/browser/suggestController.js";
import "monaco-editor/editor/contrib/hover/browser/hoverContribution.js";
import "monaco-editor/editor/contrib/find/browser/findController.js";
import "monaco-editor/editor/contrib/bracketMatching/browser/bracketMatching.js";
import "monaco-editor/editor/contrib/comment/browser/comment.js";
import "monaco-editor/editor/contrib/multicursor/browser/multicursor.js";
import "monaco-editor/editor/contrib/snippet/browser/snippetController2.js";
import "monaco-editor/languages/definitions/lua/register.js";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";

// 桌面端不需要语言服务 worker；Lua 只需要基础 editor worker。
(self as unknown as { MonacoEnvironment: unknown }).MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
};

export { monaco };

const THEME = "seriallens-dark";

let initialized = false;
let docsRegistered = false;
let docs: ApiDoc[] = [];

export interface ApiDoc {
  name: string;
  kind: string; // function | callback
  call: string;
  doc: string;
  returns: string;
}

/** 主题 + Lua 语言细节（与 index.css 的设计 token 对齐）。 */
export function initMonaco() {
  if (initialized) return;
  initialized = true;

  monaco.editor.defineTheme(THEME, {
    base: "vs-dark",
    inherit: true,
    rules: [
      { token: "comment", foreground: "6b7a8f", fontStyle: "italic" },
      { token: "keyword", foreground: "2dd4bf" },
      { token: "string", foreground: "34d399" },
      { token: "number", foreground: "fbbf24" },
      { token: "type", foreground: "38bdf8" },
      { token: "delimiter", foreground: "8b98a9" },
    ],
    colors: {
      "editor.background": "#0f131a",
      "editor.foreground": "#e6edf3",
      "editorLineNumber.foreground": "#3d4a5c",
      "editorLineNumber.activeForeground": "#8b98a9",
      "editor.lineHighlightBackground": "#151b24",
      "editor.selectionBackground": "#1c2430",
      "editorCursor.foreground": "#2dd4bf",
      "editorIndentGuide.background1": "#1c2430",
      "editorWidget.background": "#151b24",
      "editorWidget.border": "#26303e",
      "editorSuggestWidget.selectedBackground": "#1c2430",
      "editorError.foreground": "#f87171",
      "editorGutter.background": "#0f131a",
    },
  });

  // 脚本里没有 require/load 等能力（沙箱），别让补全误导用户
  monaco.languages.registerCompletionItemProvider("lua", {
    triggerCharacters: ["_"],
    provideCompletionItems: (model, position) => {
      const word = model.getWordUntilPosition(position);
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: word.startColumn,
        endColumn: word.endColumn,
      };
      const sugg = docs.map((d) => ({
        label: { label: d.name, description: d.kind === "callback" ? "回调" : d.returns },
        kind:
          d.kind === "callback"
            ? monaco.languages.CompletionItemKind.Interface
            : monaco.languages.CompletionItemKind.Function,
        insertText: d.call,
        insertTextRules: d.call.includes("(")
          ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
          : undefined,
        detail: d.returns,
        documentation: { value: `**${d.call}**\n\n${d.doc}` },
        range,
        sortText: d.kind === "callback" ? `0${d.name}` : `1${d.name}`,
      }));
      return { suggestions: sugg };
    },
  });

  monaco.languages.registerHoverProvider("lua", {
    provideHover: (model, position) => {
      const word = model.getWordAtPosition(position);
      if (!word) return null;
      const d = docs.find((x) => x.name === word.word);
      if (!d) return null;
      return {
        range: new monaco.Range(position.lineNumber, word.startColumn, position.lineNumber, word.endColumn),
        contents: [
          { value: `\`\`\`lua\n${d.call}\n\`\`\`` },
          { value: d.doc },
          { value: `返回：${d.returns}` },
        ],
      };
    },
  });
}

/** 注入 Go 侧 API 清单（唯一来源，避免前端手抄一份）。 */
export function registerApiDocs(list: ApiDoc[]) {
  docs = list;
  docsRegistered = true;
  initMonaco();
}

export function apiDocsReady(): boolean {
  return docsRegistered;
}

export function editorOptions(): monaco.editor.IStandaloneEditorConstructionOptions {
  return {
    theme: THEME,
    language: "lua",
    fontSize: 12,
    lineHeight: 18,
    fontFamily: '"Cascadia Code", "JetBrains Mono", Consolas, monospace',
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    automaticLayout: true,
    renderLineHighlight: "line",
    smoothScrolling: true,
    tabSize: 4,
    insertSpaces: true,
    wordWrap: "off",
    glyphMargin: true,
    quickSuggestions: { other: true, comments: false, strings: false },
    suggestOnTriggerCharacters: true,
    scrollbar: { verticalScrollbarSize: 8, horizontalScrollbarSize: 8 },
    padding: { top: 6 },
    fixedOverflowWidgets: true,
  };
}
