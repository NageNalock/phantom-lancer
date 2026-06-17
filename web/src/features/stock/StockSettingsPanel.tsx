import { useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import type { Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import conf from "highlight.js/lib/languages/nginx";
import go from "highlight.js/lib/languages/go";
import json from "highlight.js/lib/languages/json";
import { friendlyError } from "../../api/client";
import type { AppActions } from "../../app/App";
import type { AppData, StockSettings } from "../../app/types";
import { Button, Field, Notice, Panel, Pill, Toggle } from "../../components/ui";
import { defaultStockSettings } from "../../domain/labels";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("conf", conf);
hljs.registerLanguage("go", go);
hljs.registerLanguage("json", json);

interface ProxyTestResult {
  source: string;
  ok: boolean;
  latencyMs?: number;
  status?: number;
  error?: string;
}

// --------- normalize / diff ----------
function normalize(s: StockSettings): StockSettings {
  return {
    ...s,
    id: s.id || "default",
    proxyEnabled: Boolean(s.proxyEnabled),
    proxyType: (s.proxyType || "http").trim().toLowerCase(),
    proxyAddress: (s.proxyAddress || "").trim(),
    proxyUseForEastmoney: Boolean(s.proxyUseForEastmoney),
    proxyUseForSina: Boolean(s.proxyUseForSina),
    proxyUseForTencent: Boolean(s.proxyUseForTencent),
    quoteTtlSeconds: Number(s.quoteTtlSeconds ?? 60),
    autoRefreshEnabled: Boolean(s.autoRefreshEnabled),
    refreshIntervalSecs: Math.max(300, Number(s.refreshIntervalSecs ?? 14400)),
    defaultDataSource: (s.defaultDataSource || "eastmoney").trim().toLowerCase(),
  };
}
function same(a: StockSettings, b: StockSettings) {
  return JSON.stringify(normalize(a)) === JSON.stringify(normalize(b));
}
function changedPatch(next: StockSettings, current: Required<StockSettings>): Partial<StockSettings> {
  const patch: Partial<StockSettings> = {};
  const keys: Array<keyof StockSettings> = [
    "proxyEnabled",
    "proxyType",
    "proxyAddress",
    "proxyUseForEastmoney",
    "proxyUseForSina",
    "proxyUseForTencent",
    "quoteTtlSeconds",
    "autoRefreshEnabled",
    "refreshIntervalSecs",
    "defaultDataSource",
  ];
  const n = normalize(next) as Required<StockSettings>;
  const c = normalize(current as StockSettings) as Required<StockSettings>;
  for (const k of keys) {
    if ((n as Record<string, unknown>)[k] !== (c as Record<string, unknown>)[k]) {
      (patch as Record<string, unknown>)[k] = (n as Record<string, unknown>)[k];
    }
  }
  return patch;
}

// --------- Markdown 组件（教程弹窗用）----------
function languageFromClassName(className?: string) {
  if (!className) return "";
  const m = /language-([\w+-]+)/i.exec(className);
  return m ? m[1] : "";
}
function setupMarkdownComponents(base: string | null): Components {
  return {
    a({ children, href, node: _node, ...props }) {
      if (!href) return <>{children}</>;
      try {
        const u = new URL(href, base || undefined);
        if (!/^https?:$/.test(u.protocol)) return <>{children}</>;
        return (
          <a {...props} href={u.toString()} rel="noreferrer" target="_blank">
            {children}
          </a>
        );
      } catch {
        return <>{children}</>;
      }
    },
    img() {
      return null;
    },
    code({ children, className, node: _node, ...props }) {
      const source = String(children).replace(/\n$/, "");
      const lang = languageFromClassName(className);
      const isBlock = Boolean(lang || className?.includes("language-") || source.includes("\n"));
      if (!isBlock) {
        return (
          <code {...props} className={className}>
            {children}
          </code>
        );
      }
      let html = source;
      try {
        if (lang && hljs.getLanguage(lang)) {
          html = hljs.highlight(source, { language: lang, ignoreIllegals: true }).value;
        } else {
          html = hljs.highlightAuto(source).value;
        }
      } catch {
        /* ignore */
      }
      return (
        <pre className="message-code-block">
          <code className={`hljs language-${lang || "text"}`} dangerouslySetInnerHTML={{ __html: html }} />
        </pre>
      );
    },
    pre({ children }) {
      return <>{children}</>;
    },
  };
}

// ---------- 主组件 ----------
export function StockSettingsPanel({ actions, data }: { actions: AppActions; data: AppData }) {
  // 从 snapshot 读 settings（App.tsx 里 refreshStock 已塞进去），没有就自己拉
  const snapshotSettings = data.stock.settings;
  const settings: Required<StockSettings> = useMemo(
    () => ({ ...defaultStockSettings(), ...(snapshotSettings || {}) }),
    [snapshotSettings],
  );
  const [draft, setDraft] = useState<StockSettings>(settings);
  const [busy, setBusy] = useState("");
  const [proxyResults, setProxyResults] = useState<ProxyTestResult[] | null>(null);
  const [setupOpen, setSetupOpen] = useState(false);
  const [setupMd, setSetupMd] = useState("");

  const dirty = !same(draft, settings);

  useEffect(() => {
    setDraft(settings);
  }, [settings]);

  // snapshot 里没 settings 的话自己补一次
  useEffect(() => {
    if (snapshotSettings) return;
    let alive = true;
    (async () => {
      try {
        const resp = await actions.api<{ settings: StockSettings }>("/api/stock/settings");
        if (alive && resp?.settings) {
          setDraft({ ...defaultStockSettings(), ...resp.settings });
        }
      } catch {
        /* 忽略，用默认值 */
      }
    })();
    return () => {
      alive = false;
    };
  }, [actions, snapshotSettings]);

  function update<K extends keyof StockSettings>(k: K, v: StockSettings[K]) {
    setDraft((cur) => ({ ...cur, [k]: v }));
  }

  async function save() {
    setBusy("save");
    try {
      const patch = changedPatch(draft, settings);
      if (Object.keys(patch).length === 0) {
        actions.setToast("没有需要保存的修改", "warn");
        return;
      }
      await actions.api("/api/stock/settings", {
        method: "PUT",
        csrf: actions.csrf,
        body: patch,
      });
      await actions.refreshStock();
      actions.setToast("股票模块设置已保存", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setBusy("");
    }
  }

  async function testProxy() {
    setBusy("proxy");
    setProxyResults(null);
    try {
      const resp = await actions.api<{ results: ProxyTestResult[] }>("/api/stock/settings/proxy-test", {
        method: "POST",
        csrf: actions.csrf,
        body: { sources: ["eastmoney", "sina", "tencent"] },
      });
      setProxyResults(resp?.results || []);
      const ok = resp?.results?.every((r) => r.ok);
      actions.setToast(ok ? "全部源连通正常" : "部分源连通失败", ok ? "good" : "warn");
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setBusy("");
    }
  }

  async function openSetup() {
    if (!setupMd) {
      try {
        const resp = await fetch("/api/stock/setup-guide");
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        setSetupMd(await resp.text());
      } catch (e) {
        actions.setToast(friendlyError(e), "danger");
        return;
      }
    }
    setSetupOpen(true);
  }

  const proxyEnabled = Boolean(draft.proxyEnabled);

  return (
    <>
      <div className="grid gap-4">
        <Panel
          title="代理与数据源"
          subtitle="海外服务器访问国内行情接口时常被墙；可在国内搭一台 Squid 正向代理并按源开启。"
          actions={
            <div className="flex gap-2 flex-wrap">
              <Button onClick={() => void openSetup()}>搭建教程</Button>
              <Button disabled={busy === "proxy" || !proxyEnabled} onClick={() => void testProxy()}>
                {busy === "proxy" ? "测试中…" : "测试代理连通性"}
              </Button>
              <Button tone="primary" disabled={busy === "save" || !dirty} onClick={() => void save()}>
                {busy === "save" ? "保存中…" : "保存设置"}
              </Button>
            </div>
          }
        >
          <div className="grid gap-4">
            {dirty ? <Notice tone="warn">当前有未保存的修改。</Notice> : null}

            <Toggle
              label="启用代理（仅作用于股票模块，不影响系统其它能力）"
              checked={proxyEnabled}
              onChange={(v) => update("proxyEnabled", v)}
            />

            <div className="grid grid-cols-[160px_minmax(0,1fr)] gap-3 max-md:grid-cols-1">
              <Field label="代理类型" help="HTTP 正向代理最常用；SOCKS5 可搭配 SSH -D 使用">
                <select
                  className="select"
                  disabled={!proxyEnabled}
                  value={draft.proxyType || "http"}
                  onChange={(e) => update("proxyType", e.target.value)}
                >
                  <option value="http">HTTP</option>
                  <option value="https">HTTPS</option>
                  <option value="socks5">SOCKS5</option>
                </select>
              </Field>
              <Field label="代理地址 (host:port)" help="不带 scheme；例如 12.34.56.78:31280">
                <input
                  className="input mono"
                  autoComplete="off"
                  disabled={!proxyEnabled}
                  placeholder="例如 12.34.56.78:31280"
                  spellCheck={false}
                  value={draft.proxyAddress || ""}
                  onChange={(e) => update("proxyAddress", e.target.value)}
                />
              </Field>
            </div>

            <Field label="对以下数据源使用代理" help="至少要开启一个，否则代理总开关不生效">
              <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
                <Toggle
                  variant="row"
                  disabled={!proxyEnabled}
                  label="东方财富 (eastmoney) 行情 & 主数据"
                  checked={Boolean(draft.proxyUseForEastmoney)}
                  onChange={(v) => update("proxyUseForEastmoney", v)}
                />
                <Toggle
                  variant="row"
                  disabled={!proxyEnabled}
                  label="新浪财经 (sina) 行情 & 主数据"
                  checked={Boolean(draft.proxyUseForSina)}
                  onChange={(v) => update("proxyUseForSina", v)}
                />
                <Toggle
                  variant="row"
                  disabled={!proxyEnabled}
                  label="腾讯证券 (tencent) 行情"
                  checked={Boolean(draft.proxyUseForTencent)}
                  onChange={(v) => update("proxyUseForTencent", v)}
                />
              </div>
            </Field>

            {proxyResults ? (
              <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
                {proxyResults.map((r) => (
                  <div
                    key={r.source}
                    className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <strong>{r.source}</strong>
                      <Pill tone={r.ok ? "good" : "danger"}>{r.ok ? "OK" : "失败"}</Pill>
                    </div>
                    <div className="muted mt-2 text-xs break-words">
                      {r.error
                        ? r.error
                        : r.latencyMs
                        ? `延迟 ${r.latencyMs}ms · HTTP ${r.status || 200}`
                        : "-"}
                    </div>
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        </Panel>

        <Panel title="数据获取策略" subtitle="行情 TTL、默认数据源、主数据自动刷新。">
          <div className="grid gap-4">
            <Field label="默认首选数据源" help="UI 展示和自动刷新时的优先源；失败后会自动降级到其它源">
              <select
                className="select"
                value={draft.defaultDataSource || "eastmoney"}
                onChange={(e) => update("defaultDataSource", e.target.value)}
              >
                <option value="eastmoney">东方财富</option>
                <option value="sina">新浪财经</option>
                <option value="tencent">腾讯证券</option>
              </select>
            </Field>

            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="行情缓存 TTL (秒)" help="同一只股票多久内不重复请求行情">
                <input
                  className="input mono"
                  min={0}
                  type="number"
                  step={1}
                  value={String(draft.quoteTtlSeconds ?? 60)}
                  onChange={(e) => update("quoteTtlSeconds", Number(e.target.value))}
                />
              </Field>
              <Field label="主数据自动刷新间隔 (秒)" help="最小 300 秒 (5 分钟)">
                <input
                  className="input mono"
                  min={300}
                  type="number"
                  step={1}
                  value={String(draft.refreshIntervalSecs ?? 14400)}
                  onChange={(e) => update("refreshIntervalSecs", Number(e.target.value))}
                />
              </Field>
            </div>

            <Toggle
              label="启用主数据自动刷新（按上面的间隔）"
              checked={Boolean(draft.autoRefreshEnabled)}
              onChange={(v) => update("autoRefreshEnabled", v)}
            />
          </div>
        </Panel>
      </div>

      {setupOpen ? (
        <div
          className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm"
          onClick={() => setSetupOpen(false)}
        >
          <div
            role="dialog"
            aria-modal="true"
            aria-label="代理搭建教程"
            className="panel max-w-4xl w-[94%] max-h-[90vh] overflow-hidden flex flex-col"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="panel-header flex items-center justify-between sticky top-0 bg-[var(--surface)] z-10">
              <h3 className="m-0">在国内服务器搭建 Squid 正向代理</h3>
              <button
                type="button"
                className="text-lg muted hover:text-neutral-12"
                onClick={() => setSetupOpen(false)}
                aria-label="关闭"
              >
                关闭
              </button>
            </div>
            <div className="panel-body message-rich overflow-y-auto">
              {setupMd ? (
                <ReactMarkdown components={setupMarkdownComponents(null)} remarkPlugins={[remarkGfm]} skipHtml>
                  {setupMd}
                </ReactMarkdown>
              ) : (
                <div className="muted">加载中…</div>
              )}
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
}
