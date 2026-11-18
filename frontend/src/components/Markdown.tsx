import {
  Children,
  isValidElement,
  memo,
  useState,
  type ComponentProps,
  type ReactElement,
  type ReactNode,
} from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

// Markdown 聊天渲染（GFM：表格、任务列表、删除线、自动链接）。
// 不开 rehype-raw —— 模型输出的原始 HTML 一律按文本处理，避免注入。
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="md-body">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={COMPONENTS}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

function Pre({ children }: ComponentProps<"pre">) {
  const el = Children.toArray(children).find(isValidElement) as
    | ReactElement<{ className?: string; children?: ReactNode }>
    | undefined;
  if (!el) return <pre>{children}</pre>;

  const lang = /language-([\w+#.-]+)/.exec(el.props.className ?? "")?.[1] ?? "";
  const raw = el.props.children;
  const code = (Array.isArray(raw) ? raw.join("") : String(raw ?? "")).replace(/\n$/, "");
  return <CodeBlock lang={lang} code={code} />;
}

const COMPONENTS: Components = {
  pre: Pre,
  // 外链统一新窗口打开：Wails WebView 里点链接不能把应用界面顶掉。
  a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noreferrer" />,
  // 宽表格自己横向滚动，不把消息区撑出横向滚动条。
  table: ({ node: _node, ...props }) => (
    <div className="md-table-wrap">
      <table {...props} />
    </div>
  ),
};

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="my-1 overflow-hidden rounded-panel border border-border">
      <div className="flex items-center gap-2 bg-bg-2 px-2 py-0.5 text-[10px] text-text-2">
        <span>{lang || "代码"}</span>
        <button
          className="ml-auto hover:text-accent"
          onClick={() => {
            void navigator.clipboard?.writeText(code).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            });
          }}
        >
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="max-h-80 overflow-auto bg-bg-0 p-2 text-[11px] leading-4 text-text-1">{code}</pre>
    </div>
  );
}
