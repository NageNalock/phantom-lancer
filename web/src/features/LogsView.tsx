import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import type { AppActions } from "../app/App";
import type { LogLine, LogSource, LogTailPayload, Tone } from "../app/types";
import { Button, CheckLabel, ContextList, EmptyState, Panel, Pill } from "../components/ui";
import { formatBytesZero } from "../utils/format";
import { formatDate } from "../domain/labels";

const LEVEL_OPTIONS = [
  { value: "all", label: "全部" },
  { value: "error", label: "Error" },
  { value: "warn", label: "Warn" },
  { value: "info", label: "Info" },
];

export function LogsView({ actions }: { actions: AppActions }) {
  const [sources, setSources] = useState<LogSource[]>([]);
  const [openSourceIds, setOpenSourceIds] = useState<Set<string>>(() => new Set());
  const [activeSourceId, setActiveSourceId] = useState("");
  const [tail, setTail] = useState<LogTailPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [sourcesLoading, setSourcesLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState("all");
  const [limit, setLimit] = useState(200);
  const [wrapLines, setWrapLines] = useState(false);
  const [live, setLive] = useState(false);

  const activeSource = useMemo(() => tail?.source || sources.find((item) => item.id === activeSourceId) || null, [activeSourceId, sources, tail]);
  const activeLines = tail?.lines || [];

  const loadSources = useCallback(async () => {
    setSourcesLoading(true);
    try {
      const response = await actions.api<{ items?: LogSource[] }>("/api/logs/sources");
      setSources(response.items || []);
    } catch (error) {
      actions.setToast(error instanceof Error ? error.message : "日志源加载失败", "danger");
    } finally {
      setSourcesLoading(false);
    }
  }, [actions]);

  const loadTail = useCallback(
    async (sourceId = activeSourceId, showLoading = true) => {
      if (!sourceId) return;
      if (showLoading) setLoading(true);
      try {
        const params = new URLSearchParams({
          limit: String(limit),
          maxBytes: String(256 * 1024),
          level,
        });
        if (query.trim()) params.set("q", query.trim());
        const response = await actions.api<LogTailPayload>(`/api/logs/sources/${encodeURIComponent(sourceId)}/tail?${params.toString()}`);
        setTail(response);
        if (response.source) {
          setSources((current) => current.map((item) => (item.id === response.source?.id ? { ...item, ...response.source } : item)));
        }
      } catch (error) {
        actions.setToast(error instanceof Error ? error.message : "日志读取失败", "danger");
      } finally {
        if (showLoading) setLoading(false);
      }
    },
    [actions, activeSourceId, level, limit, query],
  );

  useEffect(() => {
    void loadSources();
  }, [loadSources]);

  useEffect(() => {
    if (activeSourceId) void loadTail(activeSourceId);
  }, [activeSourceId, level, limit, loadTail]);

  useEffect(() => {
    if (!live || !activeSourceId) return;
    const id = window.setInterval(() => void loadTail(activeSourceId, false), 2500);
    return () => window.clearInterval(id);
  }, [activeSourceId, live, loadTail]);

  function openSource(source: LogSource) {
    setOpenSourceIds((current) => new Set(current).add(source.id));
    setActiveSourceId(source.id);
    setTail(null);
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void loadTail(activeSourceId);
  }

  return (
    <section className="grid gap-4 p-4">
      <div className="grid grid-cols-[300px_minmax(0,1fr)_280px] gap-4 max-xl:grid-cols-[280px_minmax(0,1fr)] max-lg:grid-cols-1">
        <Panel
          actions={<Button onClick={() => void loadSources()}>{sourcesLoading ? "加载中" : "刷新"}</Button>}
          subtitle="默认只显示摘要，点击后读取最近日志内容。"
          title="日志源"
        >
          <div className="grid gap-2">
            {sources.length ? (
              sources.map((source) => {
                const active = source.id === activeSourceId;
                const opened = openSourceIds.has(source.id);
                return (
                  <button className={`log-source-row ${active ? "log-source-row-active" : ""}`} key={source.id} onClick={() => openSource(source)} type="button">
                    <span className="flex min-w-0 items-start justify-between gap-2">
                      <strong className="truncate text-sm">{source.name || source.id}</strong>
                      <Pill tone={sourceTone(source)}>{statusLabel(source.status)}</Pill>
                    </span>
                    <span className="muted mt-1 block truncate text-xs">{source.description || source.path || source.module}</span>
                    <span className="mt-2 flex flex-wrap items-center gap-2 text-xs">
                      <span className="mono text-[var(--muted-strong)]">{source.module || "log"}</span>
                      {source.sizeBytes !== undefined ? <span className="muted">{formatBytesZero(source.sizeBytes)}</span> : null}
                      {source.errorCount ? <span className="text-[var(--danger)]">{source.errorCount} error</span> : null}
                      {source.warningCount ? <span className="text-[var(--warn)]">{source.warningCount} warn</span> : null}
                      {opened ? <span className="muted">已打开</span> : null}
                    </span>
                  </button>
                );
              })
            ) : (
              <EmptyState body={sourcesLoading ? "正在读取服务日志和运行事件。" : "当前没有可用日志源。"} title="暂无日志源" />
            )}
          </div>
        </Panel>

        <Panel
          actions={
            activeSource ? (
              <>
                <Button disabled={loading} onClick={() => void loadTail(activeSource.id)}>
                  {loading ? "读取中" : "刷新"}
                </Button>
                <Button className={live ? "button-primary" : ""} onClick={() => setLive((value) => !value)}>
                  {live ? "停止实时跟随" : "实时跟随"}
                </Button>
              </>
            ) : null
          }
          subtitle={activeSource ? `${activeSource.name || activeSource.id} / ${activeSource.kind || "log"}` : "选择左侧日志源后加载内容。"}
          title="日志工作区"
        >
          {activeSource ? (
            <div className="grid gap-3">
              <form className="grid grid-cols-[minmax(0,1fr)_120px_100px_auto] gap-2 max-lg:grid-cols-1" onSubmit={submitSearch}>
                <input aria-label="搜索日志" autoComplete="off" className="input" name="logs_query" onChange={(event) => setQuery(event.target.value)} placeholder="搜索当前加载范围…" value={query} />
                <select aria-label="日志等级" className="select" name="logs_level" onChange={(event) => setLevel(event.target.value)} value={level}>
                  {LEVEL_OPTIONS.map((item) => (
                    <option key={item.value} value={item.value}>
                      {item.label}
                    </option>
                  ))}
                </select>
                <select aria-label="日志行数" className="select" name="logs_limit" onChange={(event) => setLimit(Number(event.target.value))} value={limit}>
                  {[200, 500, 1000].map((value) => (
                    <option key={value} value={value}>
                      {value} 行
                    </option>
                  ))}
                </select>
                <Button type="submit">搜索</Button>
              </form>
              <CheckLabel
                checked={wrapLines}
                onChange={(checked) => setWrapLines(checked)}
                size="xs"
              >
                自动换行
              </CheckLabel>
              {tail?.truncated ? <div className="notice-warn">日志已按读取上限截断，仅展示最近 {formatBytesZero(tail.maxBytes || 0)}。</div> : null}
              <div className="log-lines" role="log">
                {activeLines.length ? (
                  activeLines.map((line) => <LogLineRow key={`${line.sourceId}-${line.offset}-${line.time || ""}`} line={line} query={query} wrap={wrapLines} />)
                ) : (
                  <EmptyState body={loading ? "正在读取最近日志。" : "当前过滤条件下没有日志行。"} title="暂无日志内容" />
                )}
              </div>
            </div>
          ) : (
            <EmptyState body="每个日志默认折叠，不会在首屏读取大文件。点击左侧任一日志源开始查看。" title="选择日志源" />
          )}
        </Panel>

        <Panel className="max-xl:col-span-2 max-lg:col-span-1" subtitle="读取范围、轮转和风险摘要。" title="检查器">
          {activeSource ? (
            <ContextList
              items={[
                ["来源", activeSource.name || activeSource.id],
                ["模块", <span className="mono">{activeSource.module || "-"}</span>],
                ["状态", statusLabel(activeSource.status)],
                ["路径", activeSource.path ? <span className="mono">{activeSource.path}</span> : "-"],
                ["大小", activeSource.sizeBytes !== undefined ? formatBytesZero(activeSource.sizeBytes) : "-"],
                ["更新", formatDate(activeSource.updatedAt) || "-"],
                ["轮转", activeSource.rotationSummary || (activeSource.managed ? "事件源由系统管理" : "外部管理")],
                ["读取", `${activeLines.length} / ${tail?.limit || limit} 行`],
                ["告警", `${activeSource.errorCount || 0} error / ${activeSource.warningCount || 0} warn`],
                ["游标", tail?.cursor ? <span className="mono">{tail.cursor}</span> : "-"],
              ]}
            />
          ) : (
            <EmptyState body="打开日志后会显示路径、轮转策略、读取范围和错误摘要。" title="等待选择" />
          )}
        </Panel>
      </div>
    </section>
  );
}

function LogLineRow({ line, query, wrap }: { line: LogLine; query: string; wrap: boolean }) {
  const level = normalizeLevel(line.level);
  return (
    <article className={`log-line log-line-${level}`}>
      <div className="log-line-meta">
        <span>{formatDate(line.time) || "-"}</span>
        <span className={`log-level log-level-${level}`}>{level.toUpperCase()}</span>
        <span>#{line.offset}</span>
      </div>
      <div className={`log-line-message ${wrap ? "whitespace-pre-wrap" : "whitespace-pre"}`}>{highlightText(line.message || line.raw || "", query)}</div>
      {line.fields ? (
        <details className="log-line-fields">
          <summary>fields</summary>
          <pre className="whitespace-pre-wrap break-words text-xs">{JSON.stringify(line.fields, null, 2)}</pre>
        </details>
      ) : null}
    </article>
  );
}

function highlightText(value: string, query: string) {
  const text = String(value || "");
  const needle = query.trim();
  if (!needle) return text;
  const lowerText = text.toLowerCase();
  const lowerNeedle = needle.toLowerCase();
  const parts: ReactNode[] = [];
  let cursor = 0;
  while (cursor < text.length) {
    const index = lowerText.indexOf(lowerNeedle, cursor);
    if (index < 0) {
      parts.push(text.slice(cursor));
      break;
    }
    if (index > cursor) parts.push(text.slice(cursor, index));
    parts.push(
      <mark className="log-highlight" key={`${index}-${needle}`}>
        {text.slice(index, index + needle.length)}
      </mark>,
    );
    cursor = index + needle.length;
  }
  return parts;
}

function sourceTone(source: LogSource): Tone {
  if (source.status === "failed" || source.status === "unreadable" || (source.errorCount || 0) > 0) return "danger";
  if (source.status === "missing" || source.status === "stale" || (source.warningCount || 0) > 0) return "warn";
  if (source.status === "available" || source.status === "active") return "good";
  return "neutral";
}

function statusLabel(value?: string): string {
  return (
    {
      available: "可用",
      active: "运行中",
      missing: "未生成",
      unreadable: "不可读",
      failed: "失败",
      stale: "过期",
      empty: "暂无事件",
    }[value || ""] ||
    value ||
    "未知"
  );
}

function normalizeLevel(value?: string): "info" | "warn" | "error" {
  if (value === "error") return "error";
  if (value === "warn" || value === "warning") return "warn";
  return "info";
}
