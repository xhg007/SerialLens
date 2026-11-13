import { useEffect, useMemo, useRef, useState } from "react";
import { useAIStore, type AIMessage, type AIProvider } from "../stores/ai";
import { useParserStore } from "../stores/parser";

const BTN =
  "rounded-panel border border-border px-2 py-0.5 text-xs text-text-2 hover:border-accent hover:text-accent disabled:opacity-40 disabled:hover:border-border disabled:hover:text-text-2";
const INPUT =
  "w-full rounded-panel border border-border bg-bg-2 px-2 py-1 text-xs text-text-1 outline-none focus:border-accent";

const EXAMPLES = [
  "解释当前脚本每一帧的字段含义",
  "把 detect_frame 改成按帧头 0xAA55 + 长度切帧",
  "帮我给已有通道配一份折线图 charts.json",
];

export function AIPanel() {
  const settings = useAIStore((s) => s.settings);
  const providers = useAIStore((s) => s.providers);
  const messages = useAIStore((s) => s.messages);
  const busy = useAIStore((s) => s.busy);
  const error = useAIStore((s) => s.error);
  const testing = useAIStore((s) => s.testing);
  const testResult = useAIStore((s) => s.testResult);
  const scriptName = useParserStore((s) => s.script);

  const [showSettings, setShowSettings] = useState(false);
  const [draft, setDraft] = useState("");

  useEffect(() => {
    void useAIStore.getState().hydrate();
  }, []);

  // 首次进入且尚未配置时，直接把设置展开——否则用户看着一个不能用的输入框
  useEffect(() => {
    if (settings && !settings.ready) setShowSettings(true);
  }, [settings]);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, busy]);

  const send = () => {
    const text = draft.trim();
    if (!text || busy) return;
    setDraft("");
    void useAIStore.getState().send(text);
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-bg-1">
      {/* 头部 */}
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-2 py-1">
        <span className="text-xs font-semibold text-text-2">AI 脚本助手</span>
        <StatusDot ready={Boolean(settings?.ready)} running={busy} />
        <span className="truncate text-[11px] text-text-2" title={settings?.base_url}>
          {settings?.model || "未选择模型"}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <button className={BTN} onClick={() => setShowSettings((v) => !v)}>
            {showSettings ? "收起设置" : "设置"}
          </button>
          <button className={BTN} disabled={messages.length === 0} onClick={() => void useAIStore.getState().clear()}>
            清空
          </button>
        </div>
      </div>

      {showSettings && <SettingsForm providers={providers} testing={testing} testResult={testResult} />}

      {/* 消息区 */}
      <div ref={scrollRef} className="min-h-0 flex-1 overflow-auto px-2 py-2">
        {messages.length === 0 && (
          <div className="flex flex-col gap-2 text-[11px] text-text-2">
            <p>
              描述协议格式、粘贴文档，或让我修当前脚本。
              {scriptName ? (
                <span className="text-accent">（已注入脚本 {scriptName}）</span>
              ) : (
                <span>（未打开工程，只能做通用问答）</span>
              )}
            </p>
            <div className="flex flex-col gap-1">
              {EXAMPLES.map((e) => (
                <button
                  key={e}
                  className="rounded-panel border border-border px-2 py-1 text-left text-[11px] text-text-2 hover:border-accent hover:text-accent"
                  onClick={() => setDraft(e)}
                >
                  {e}
                </button>
              ))}
            </div>
          </div>
        )}
        {messages.map((m) => (
          <MessageRow key={m.id} m={m} />
        ))}
      </div>

      {error && (
        <div className="flex shrink-0 items-start gap-2 border-t border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger">
          <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">{error}</span>
          <button className={BTN} onClick={() => useAIStore.getState().dismissError()}>
            ✕
          </button>
        </div>
      )}

      {/* 输入区 */}
      <div className="shrink-0 border-t border-border p-2">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          rows={3}
          placeholder={settings?.ready ? "描述协议 / 粘贴样本帧 / 让它修脚本…（Enter 发送，Shift+Enter 换行）" : "先在设置里配置供应商与 API Key"}
          disabled={!settings?.ready && !showSettings}
          className="w-full resize-none rounded-panel border border-border bg-bg-2 px-2 py-1 text-xs text-text-1 outline-none focus:border-accent disabled:opacity-60"
        />
        <div className="mt-1 flex items-center gap-2 text-[11px] text-text-2">
          <span className="truncate">{settings?.has_key ? `Key ${settings.key_hint}` : "未配置 Key"}</span>
          <span className="ml-auto" />
          {busy ? (
            <button className={BTN} onClick={() => void useAIStore.getState().cancel()}>
              停止
            </button>
          ) : (
            <button className={BTN} disabled={!draft.trim() || !settings?.ready} onClick={send}>
              发送
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function StatusDot({ ready, running }: { ready: boolean; running: boolean }) {
  const color = running ? "bg-warn" : ready ? "bg-rx" : "bg-danger";
  const title = running ? "生成中" : ready ? "已就绪" : "未配置";
  return <span className={`h-2 w-2 shrink-0 rounded-full ${color}`} title={title} />;
}

function SettingsForm({
  providers,
  testing,
  testResult,
}: {
  providers: AIProvider[];
  testing: boolean;
  testResult: string;
}) {
  const settings = useAIStore((s) => s.settings);
  const [form, setForm] = useState(() => ({
    provider: settings?.provider ?? "deepseek",
    base_url: settings?.base_url ?? "",
    model: settings?.model ?? "",
    api_key: "",
    temperature: settings?.temperature ?? 0.3,
    max_tokens: settings?.max_tokens ?? 2048,
    timeout_sec: settings?.timeout_sec ?? 60,
    system_extra: settings?.system_extra ?? "",
  }));

  useEffect(() => {
    if (!settings) return;
    setForm((f) => ({
      ...f,
      provider: settings.provider,
      base_url: settings.base_url,
      model: settings.model,
      temperature: settings.temperature,
      max_tokens: settings.max_tokens,
      timeout_sec: settings.timeout_sec,
      system_extra: settings.system_extra ?? "",
    }));
  }, [settings]);

  const current = useMemo(() => providers.find((p) => p.id === form.provider), [providers, form.provider]);
  const models = current?.models ?? [];

  const pickProvider = (id: string) => {
    const p = providers.find((x) => x.id === id);
    setForm((f) => ({
      ...f,
      provider: id,
      base_url: p?.base_url || "",
      model: p?.models?.[0] || "",
    }));
  };

  const patch = () => ({
    provider: form.provider,
    base_url: form.base_url,
    model: form.model,
    api_key: form.api_key,
    temperature: form.temperature,
    max_tokens: form.max_tokens,
    timeout_sec: form.timeout_sec,
    system_extra: form.system_extra,
  });

  return (
    <div className="shrink-0 space-y-1.5 border-b border-border bg-bg-2/40 p-2 text-[11px]">
      <div className="flex gap-1.5">
        <label className="flex-1">
          <span className="text-text-2">供应商</span>
          <select value={form.provider} onChange={(e) => pickProvider(e.target.value)} className={INPUT}>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex-1">
          <span className="text-text-2">模型</span>
          <input
            list="ai-models"
            value={form.model}
            onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
            className={INPUT}
            placeholder="模型名"
          />
          <datalist id="ai-models">
            {models.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
        </label>
      </div>

      <label className="block">
        <span className="text-text-2">Base URL（OpenAI 兼容，通常含 /v1）</span>
        <input
          value={form.base_url}
          onChange={(e) => setForm((f) => ({ ...f, base_url: e.target.value }))}
          className={INPUT}
          placeholder="https://api.deepseek.com/v1"
        />
      </label>

      <label className="block">
        <span className="text-text-2">
          API Key {settings?.has_key && <span className="text-rx">已保存 {settings.key_hint}</span>}
        </span>
        <input
          type="password"
          value={form.api_key}
          onChange={(e) => setForm((f) => ({ ...f, api_key: e.target.value }))}
          className={INPUT}
          placeholder={settings?.has_key ? "留空表示不修改" : "sk-…"}
          autoComplete="off"
        />
      </label>

      <div className="flex gap-1.5">
        <label className="w-20">
          <span className="text-text-2">温度</span>
          <input
            type="number"
            step="0.1"
            min="0"
            max="2"
            value={form.temperature}
            onChange={(e) => setForm((f) => ({ ...f, temperature: Number(e.target.value) }))}
            className={INPUT}
          />
        </label>
        <label className="w-24">
          <span className="text-text-2">Max tokens</span>
          <input
            type="number"
            min="64"
            value={form.max_tokens}
            onChange={(e) => setForm((f) => ({ ...f, max_tokens: Number(e.target.value) }))}
            className={INPUT}
          />
        </label>
        <label className="w-20">
          <span className="text-text-2">超时(s)</span>
          <input
            type="number"
            min="5"
            value={form.timeout_sec}
            onChange={(e) => setForm((f) => ({ ...f, timeout_sec: Number(e.target.value) }))}
            className={INPUT}
          />
        </label>
      </div>

      <label className="block">
        <span className="text-text-2">工程偏好（追加到 system prompt）</span>
        <input
          value={form.system_extra}
          onChange={(e) => setForm((f) => ({ ...f, system_extra: e.target.value }))}
          className={INPUT}
          placeholder="例如：数值一律按小端解析"
        />
      </label>

      {current?.note && <p className="text-text-2/80">{current.note}</p>}
      {current?.key_url && (
        <p className="text-text-2/80">
          获取 Key：
          <a className="text-accent hover:underline" href={current.key_url} target="_blank" rel="noreferrer">
            {current.key_url}
          </a>
        </p>
      )}

      <div className="flex items-center gap-1.5">
        <button
          className={BTN}
          disabled={testing}
          onClick={() => void useAIStore.getState().testConnection(patch())}
        >
          {testing ? "测试中…" : "测试连接"}
        </button>
        <button
          className={BTN}
          onClick={async () => {
            await useAIStore.getState().saveSettings(patch());
            setForm((f) => ({ ...f, api_key: "" }));
          }}
        >
          保存
        </button>
        {settings?.has_key && (
          <button
            className={BTN}
            onClick={() => void useAIStore.getState().saveSettings({ clear_key: true })}
          >
            清除 Key
          </button>
        )}
        <span className="ml-auto truncate text-text-2" title={testResult}>
          {testResult}
        </span>
      </div>
    </div>
  );
}

function MessageRow({ m }: { m: AIMessage }) {
  if (m.role === "user") {
    return (
      <div className="mb-2 flex justify-end">
        <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-panel bg-bg-2 px-2 py-1 text-xs text-text-1">
          {m.content}
        </div>
      </div>
    );
  }
  return (
    <div className="mb-2">
      {m.reasoning && (
        <details className="mb-1 text-[11px] text-text-2">
          <summary className="cursor-pointer select-none">思维链</summary>
          <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-panel border border-border/60 p-1">
            {m.reasoning}
          </pre>
        </details>
      )}
      <div className="text-xs text-text-1">
        {m.content ? <RichText text={m.content} /> : m.streaming ? <span className="text-text-2">…</span> : null}
      </div>
      {m.streaming && <span className="text-accent">▍</span>}
      <div className="mt-0.5 flex items-center gap-2 text-[10px] text-text-2">
        {m.canceled && <span className="text-warn">已停止</span>}
        {m.error && <span className="text-danger">{m.error}</span>}
        {m.usage?.total_tokens ? <span>{m.usage.total_tokens} tokens</span> : null}
        {m.elapsedMs ? <span>{m.elapsedMs}ms</span> : null}
        {m.firstMs ? <span>首字 {m.firstMs}ms</span> : null}
      </div>
    </div>
  );
}

// RichText 极简渲染：只识别 ``` 代码块（Lua 输出必须能原样复制），
// 其余按纯文本 / 换行显示。不引 markdown 库——AI 面板不需要表格与嵌套列表。
function RichText({ text }: { text: string }) {
  const parts = text.split("```");
  return (
    <>
      {parts.map((part, i) => {
        if (i % 2 === 1) {
          const nl = part.indexOf("\n");
          const lang = nl >= 0 ? part.slice(0, nl).trim() : "";
          const code = nl >= 0 ? part.slice(nl + 1) : part;
          return <CodeBlock key={i} lang={lang} code={code} />;
        }
        return (
          <span key={i} className="whitespace-pre-wrap break-words">
            {part}
          </span>
        );
      })}
    </>
  );
}

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
