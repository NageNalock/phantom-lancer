import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, Tone } from "../../app/types";
import { Button, CheckLabel, EmptyState, Field, Notice, Panel, Pill } from "../../components/ui";
import {
  friendlyError,
  mailLogsList,
  mailLogsTail,
  openMailLogsStream,
  mailLogsRedactionSummary,
  type LogFileInfo,
  type LogsTailReq,
  type LogsTailResp,
  type RedactionSummaryResp,
} from "../../api/client";
import { formatBytesZero } from "../../utils/format";
import { formatDate } from "../../domain/labels";

const SEVERITY_OPTIONS: Array<{ value: LogsTailReq["severity"]; label: string; tone: Tone }> = [
  { value: "", label: "全部", tone: "neutral" },
  { value: "debug", label: "Debug", tone: "neutral" },
  { value: "info", label: "Info", tone: "good" },
  { value: "warn", label: "Warn", tone: "warn" },
  { value: "error", label: "Error", tone: "danger" },
  { value: "critical", label: "Critical", tone: "danger" },
];

const SAMPLE_RATES = [
  { value: "high", label: "高频 (最大采样)" },
  { value: "normal", label: "正常" },
  { value: "low", label: "低频 (去抖动)" },
] as const;

const RECENT_SEARCHES_KEY = "pl_mail_logs_recent_searches";
const MAX_RECENT_SEARCHES = 10;

interface ParsedLogLine {
  raw: string;
  severity: "debug" | "info" | "warn" | "error" | "critical" | "unknown";
  timestamp: string;
  message: string;
}

export function MailLogsTab({ actions, reload, data }: { actions: AppActions; reload: () => Promise<void>; data: AppData }) {
  // ---- Core state ----
  const [logFiles, setLogFiles] = useState<LogFileInfo[]>(data.mail.logFiles || []);
  const [activePath, setActivePath] = useState<string>("");
  const [sampleRate, setSampleRate] = useState<"high" | "normal" | "low">("normal");
  const [search, setSearch] = useState("");
  const [severity, setSeverity] = useState<LogsTailReq["severity"]>("");
  const [limit, setLimit] = useState(500);

  const [tailResp, setTailResp] = useState<LogsTailResp | null>(null);
  const [streamingLines, setStreamingLines] = useState<string[]>([]);
  const [autoScroll, setAutoScroll] = useState(true);
  const [loading, setLoading] = useState(false);
  const [streamConnected, setStreamConnected] = useState(false);
  const [lastHeartbeat, setLastHeartbeat] = useState<string>("");
  const [skippedCount, setSkippedCount] = useState(0);
  const [streaming, setStreaming] = useState(false);

  const [redaction, setRedaction] = useState<RedactionSummaryResp | null>(null);
  const [showRedactionModal, setShowRedactionModal] = useState(false);
  const [offset, setOffset] = useState(0);

  const logContainerRef = useRef<HTMLDivElement | null>(null);
  const streamCloseRef = useRef<{ close: () => void } | null>(null);

  // ---- Derived: combined display lines ----
  const allLines = useMemo(() => {
    const base = tailResp?.lines || [];
    return streaming ? [...base, ...streamingLines] : base;
  }, [tailResp, streamingLines, streaming]);

  const parsedLines = useMemo<ParsedLogLine[]>(() => allLines.map(parseLogLine), [allLines]);

  const severityDistribution = useMemo(() => {
    const counts: Record<string, number> = { debug: 0, info: 0, warn: 0, error: 0, critical: 0, unknown: 0 };
    for (const line of parsedLines) counts[line.severity] = (counts[line.severity] || 0) + 1;
    return counts;
  }, [parsedLines]);

  // ---- Recent searches ----
  const [recentSearches, setRecentSearches] = useState<string[]>(() => {
    try {
      const raw = localStorage.getItem(RECENT_SEARCHES_KEY);
      return raw ? (JSON.parse(raw) as string[]) : [];
    } catch {
      return [];
    }
  });

  const saveRecentSearch = useCallback((term: string) => {
    const t = term.trim();
    if (!t) return;
    setRecentSearches((prev) => {
      const filtered = prev.filter((item) => item !== t);
      const next = [t, ...filtered].slice(0, MAX_RECENT_SEARCHES);
      try {
        localStorage.setItem(RECENT_SEARCHES_KEY, JSON.stringify(next));
      } catch {
        /* noop */
      }
      return next;
    });
  }, []);

  // ---- Loaders ----
  const loadFiles = useCallback(async () => {
    try {
      const list = await mailLogsList();
      setLogFiles(list);
      if (!activePath && list.length) setActivePath(list[0].path);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }, [actions, activePath]);

  const loadRedaction = useCallback(async () => {
    try {
      const r = await mailLogsRedactionSummary();
      setRedaction(r);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }, [actions]);

  const runTail = useCallback(
    async (append = false) => {
      if (!activePath) return;
      setLoading(true);
      try {
        const resp = await mailLogsTail({
          path: activePath,
          limit,
          search: search.trim() || undefined,
          severity: severity || undefined,
        });
        saveRecentSearch(search);
        if (append) {
          setTailResp((prev) => {
            const prevLines = prev?.lines || [];
            return {
              lines: [...prevLines, ...resp.lines],
              truncated: resp.truncated,
              scanned_bytes: (prev?.scanned_bytes || 0) + resp.scanned_bytes,
              matched_count: (prev?.matched_count || 0) + resp.matched_count,
            };
          });
          setOffset((o) => o + resp.lines.length);
        } else {
          setTailResp(resp);
          setOffset(resp.lines.length);
        }
        if (streaming) setStreamingLines([]);
      } catch (e) {
        actions.setToast(friendlyError(e), "danger");
      } finally {
        setLoading(false);
      }
    },
    [activePath, limit, search, severity, saveRecentSearch, streaming, actions],
  );

  // ---- Lifecycle ----
  useEffect(() => {
    void loadFiles();
    void loadRedaction();
  }, [loadFiles, loadRedaction]);

  useEffect(() => {
    if (activePath) void runTail(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath]);

  // Auto scroll to bottom
  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      const el = logContainerRef.current;
      el.scrollTop = el.scrollHeight;
    }
  }, [parsedLines.length, autoScroll]);

  // ---- Stream control ----
  const startStream = useCallback(() => {
    if (!activePath) return;
    stopStream();
    const handle = openMailLogsStream(
      { path: activePath, sample_rate: sampleRate },
      (line) => {
        setStreamConnected(true);
        setStreamingLines((prev) => {
          const next = [...prev, line];
          // Cap in-memory stream buffer to 2000 lines so the page stays responsive.
          if (next.length > 2000) return next.slice(next.length - 2000);
          return next;
        });
      },
      (count) => setSkippedCount((s) => s + count),
      () => setLastHeartbeat(new Date().toLocaleTimeString("zh-CN")),
    );
    streamCloseRef.current = handle;
    setStreaming(true);
    setStreamConnected(true);
    setSkippedCount(0);
  }, [activePath, sampleRate]);

  const stopStream = useCallback(() => {
    streamCloseRef.current?.close();
    streamCloseRef.current = null;
    setStreaming(false);
    setStreamConnected(false);
  }, []);

  useEffect(() => () => stopStream(), [stopStream]);

  // ---- Handlers ----
  function handleSearchSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void runTail(false);
  }

  function handleLoadMore() {
    void runTail(true);
  }

  function selectFile(file: LogFileInfo) {
    if (streaming) stopStream();
    setActivePath(file.path);
  }

  const activeFile = logFiles.find((f) => f.path === activePath);

  // ---- Render ----
  return (
    <section className="grid gap-3">
      {/* ---- Top tool bar ---- */}
      <Panel>
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto_auto] gap-2 items-end max-lg:grid-cols-1">
          <Field label="日志文件" help={activeFile ? `${formatBytesZero(activeFile.size_bytes)} / ~${activeFile.lines_estimated.toLocaleString()} 行` : "选择日志文件"}>
            <select
              className="select"
              value={activePath}
              onChange={(e) => setActivePath(e.target.value)}
              disabled={loading || streaming}
            >
              {logFiles.length === 0 && <option value="">(暂无日志文件)</option>}
              {logFiles.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>

              ))}
            </select>
          </Field>
          <form className="grid grid-cols-[minmax(0,1fr)_120px_100px_auto] gap-2 max-sm:grid-cols-1" onSubmit={handleSearchSubmit}>
            <input
              className="input"
              placeholder="搜索关键字…支持子字符串匹配"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              name="mail_logs_search"
            />
            <select
              className="select"
              value={severity ?? ""}
              onChange={(e) => setSeverity(e.target.value as LogsTailReq["severity"])}
              name="mail_logs_severity"
            >
              {SEVERITY_OPTIONS.map((opt) => (
                <option key={String(opt.value)} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <select
              className="select"
              value={String(limit)}
              onChange={(e) => setLimit(Number(e.target.value))}
              name="mail_logs_limit"
            >
              {[200, 500, 1000, 2000].map((n) => (
                <option key={n} value={String(n)}>
                  {n} 行
                </option>
              ))}
            </select>
            <Button disabled={!activePath || loading} type="submit">
              {loading ? "读取中" : "Tail 刷新"}
            </Button>
          </form>
          <div className="flex items-center gap-2">
            <select
              className="select"
              value={sampleRate}
              onChange={(e) => setSampleRate(e.target.value as "high" | "normal" | "low")}
              disabled={streaming}
              name="mail_logs_sample_rate"
            >
              {SAMPLE_RATES.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </select>
            <Button className={streaming ? "button-primary" : ""} onClick={() => (streaming ? stopStream() : startStream())} disabled={!activePath}>
              {streaming ? "停止流式" : "实时流式"}
            </Button>
            <Button onClick={() => setAutoScroll((v) => !v)} tone={autoScroll ? "primary" : "neutral"}>
              {autoScroll ? "自动滚动 ✓" : "固定顶部"}
            </Button>
            {redaction ? (
              <button
                className="button"
                onClick={() => setShowRedactionModal(true)}
                type="button"
                title="查看脱敏规则"
              >
                <Pill tone="neutral">{redaction.rules_count} 脱敏</Pill>
              </button>
            ) : null}
          </div>
        </div>
      </Panel>

      {/* ---- Three columns ---- */}
      <div className="grid grid-cols-[260px_minmax(0,1fr)_280px] gap-3 max-xl:grid-cols-[240px_minmax(0,1fr)] max-lg:grid-cols-1">
        {/* ---- Left column: Log Files ---- */}
        <Panel subtitle="可选日志文件" title="Files">
          <div className="grid gap-2">
            {logFiles.length ? (
              logFiles.map((file) => {
                const active = file.path === activePath;
                return (
                  <button
                    className={`log-source-row ${active ? "log-source-row-active" : ""}`}
                    key={file.path}
                    onClick={() => selectFile(file)}
                    type="button"
                  >
                    <span className="flex min-w-0 items-start justify-between gap-2">
                      <strong className="truncate text-sm" title={file.path}>
                        {file.path.split(/[\\/]/).pop() || file.path}
                      </strong>
                      <Pill tone="neutral">{formatBytesZero(file.size_bytes)}</Pill>
                    </span>
                    <span className="muted mt-1 block truncate text-xs" title={file.path}>
                      {file.path}
                    </span>
                    <span className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
                      <span className="muted">{formatDate(file.modified_at) || "-"}</span>
                      <span className="muted mono">~{file.lines_estimated.toLocaleString()} 行</span>
                    </span>
                  </button>
                );
              })
            ) : (
              <EmptyState body="尚未发现 Mox 日志文件。确认 Mox 已启动或刷新列表。" title="暂无日志文件" />
            )}
            <Button onClick={() => void loadFiles()}>刷新文件列表</Button>
          </div>
          <div className="mt-3">
            <Notice tone="warn">
              <strong>脱敏已启用：</strong> 返回的所有日志行都经过服务端 safelog 处理（邮箱、密码、密钥、内部 IP 等），共计 {redaction?.rules_count ?? 7} 条规则。
              <button className="ml-1 underline" onClick={() => setShowRedactionModal(true)} type="button">
                查看规则列表
              </button>
            </Notice>
          </div>
        </Panel>

        {/* ---- Middle column: Log lines ---- */}
        <Panel
          actions={
            <div className="flex items-center gap-2">
              {streaming ? (
                <Pill tone="good">Streaming · {streamingLines.length}</Pill>
              ) : null}
              {tailResp?.truncated && !streaming ? <Pill tone="warn">已截断</Pill> : null}
              <span className="muted text-xs">
                {parsedLines.length} 行 · 扫描 {formatBytesZero(tailResp?.scanned_bytes || 0)}
              </span>
            </div>
          }
          subtitle={activeFile ? `${activeFile.path}  ${formatBytesZero(activeFile.size_bytes)}` : "选择左侧日志文件后加载内容"}
          title={activeFile ? (activeFile.path.split(/[\\/]/).pop() || activeFile.path) : "日志内容"}
        >
          <div className="grid gap-2">
            <CheckLabel checked={autoScroll} onChange={setAutoScroll} size="xs">
              追加新行时自动滚动到底部
            </CheckLabel>
            {tailResp?.truncated && !streaming ? (
              <Notice tone="warn">
                读取已被截断，剩余行可能未加载。可点击下方「加载更多」或切换为流式模式查看后续内容。
              </Notice>
            ) : null}
            <div
              ref={logContainerRef}
              className="log-lines"
              role="log"
              style={{ maxHeight: "100000px" }}
            >
              {parsedLines.length ? (
                parsedLines.map((line, idx) => (
                  <LogLineItem key={idx} line={line} query={search} />
                ))
              ) : (
                <EmptyState
                  body={loading ? "正在读取日志行…" : activePath ? "当前过滤条件下无匹配行。" : "选择左侧日志文件开始查看。"}
                  title="暂无日志内容"
                />
              )}
            </div>
            <div className="flex justify-center gap-2">
              {tailResp?.truncated && !streaming ? (
                <Button onClick={handleLoadMore} disabled={loading}>
                  {loading ? "加载中" : `加载更多 (累计 ${offset})`}
                </Button>
              ) : null}
              <Button onClick={() => reload()} tone="primary">
                全局重载
              </Button>
            </div>
          </div>
        </Panel>

        {/* ---- Right column: Stats + Filters ---- */}
        <Panel className="max-xl:col-span-2 max-lg:col-span-1" subtitle="等级分布、历史搜索、连接状态" title="检查器">
          <div className="grid gap-3">
            <section>
              <strong className="text-xs text-[var(--muted-strong)]">等级分布</strong>
              <div className="mt-2 grid grid-cols-2 gap-1.5">
                {SEVERITY_OPTIONS.filter((s) => s.value !== "").map((s) => {
                  const count = severityDistribution[s.value || ""] || 0;
                  return (
                    <button
                      className={`button text-left ${severity === s.value ? "button-primary" : ""}`}
                      key={String(s.value)}
                      onClick={() => setSeverity(s.value)}
                      type="button"
                    >
                      <Pill tone={s.tone}>{s.label}</Pill>
                      <span className="ml-2 mono">{count.toLocaleString()}</span>
                    </button>
                  );
                })}
                <button
                  className={`button text-left ${severity === "" ? "button-primary" : ""}`}
                  onClick={() => setSeverity("")}
                  type="button"
                >
                  <Pill tone="neutral">全部</Pill>
                  <span className="ml-2 mono">{parsedLines.length.toLocaleString()}</span>
                </button>
              </div>
            </section>

            <section>
              <strong className="text-xs text-[var(--muted-strong)]">历史搜索</strong>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {recentSearches.length ? (
                  recentSearches.map((term) => (
                    <button
                      className="button"
                      key={term}
                      onClick={() => {
                        setSearch(term);
                        void runTail(false);
                      }}
                      type="button"
                    >
                      <span className="mono text-xs">{term}</span>
                    </button>
                  ))
                ) : (
                  <span className="muted text-xs">暂无历史搜索</span>
                )}
                {recentSearches.length ? (
                  <button
                    className="button"
                    onClick={() => {
                      setRecentSearches([]);
                      try {
                        localStorage.removeItem(RECENT_SEARCHES_KEY);
                      } catch {
                        /* noop */
                      }
                    }}
                    type="button"
                  >
                    清空
                  </button>
                ) : null}
              </div>
            </section>

            <section>
              <strong className="text-xs text-[var(--muted-strong)]">实时流状态</strong>
              <div className="mt-2 grid gap-1.5 text-sm">
                <div className="flex items-center justify-between">
                  <span className="muted text-xs">连接状态</span>
                  {streaming ? (
                    <Pill tone={streamConnected ? "good" : "warn"}>
                      {streamConnected ? "已连接" : "连接中…"}
                    </Pill>
                  ) : (
                    <Pill tone="neutral">未连接</Pill>
                  )}
                </div>
                <div className="flex items-center justify-between">
                  <span className="muted text-xs">心跳</span>
                  <span className="mono text-xs">{lastHeartbeat || "-"}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="muted text-xs">丢弃非错误行</span>
                  <span className={`mono text-xs ${skippedCount > 0 ? "text-[var(--warn)]" : ""}`}>
                    {skippedCount.toLocaleString()}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="muted text-xs">匹配计数</span>
                  <span className="mono text-xs">{tailResp?.matched_count?.toLocaleString() || parsedLines.length.toLocaleString()}</span>
                </div>
              </div>
            </section>

            <section>
              <strong className="text-xs text-[var(--muted-strong)]">采样率</strong>
              <div className="mt-2 grid gap-1.5">
                {SAMPLE_RATES.map((r) => (
                  <button
                    className={`button text-left ${sampleRate === r.value ? "button-primary" : ""}`}
                    key={r.value}
                    onClick={() => setSampleRate(r.value)}
                    type="button"
                  >
                    {r.label}
                  </button>
                ))}
              </div>
              <p className="muted mt-1 mb-0 text-xs leading-relaxed">
                采样率影响流式推送密度：高频用于详细排障，低频用于长时间监视。
              </p>
            </section>
          </div>
        </Panel>
      </div>

      {/* ---- Redaction rules modal ---- */}
      {showRedactionModal && redaction ? (
        <RedactionModal rules={redaction} onClose={() => setShowRedactionModal(false)} />
      ) : null}
    </section>
  );
}

// ============================================================================
// ---- Sub components --------------------------------------------------------
// ============================================================================

function LogLineItem({ line, query }: { line: ParsedLogLine; query: string }) {
  const tone: Tone =
    line.severity === "error" || line.severity === "critical"
      ? "danger"
      : line.severity === "warn"
        ? "warn"
        : line.severity === "info"
          ? "good"
          : "neutral";
  return (
    <article className={`log-line log-line-${line.severity === "unknown" ? "info" : line.severity}`}>
      <div className="log-line-meta">
        <span>{line.timestamp || "-"}</span>
        <span className={`log-level log-level-${line.severity === "unknown" ? "info" : line.severity}`}>
          <Pill tone={tone}>{line.severity.toUpperCase()}</Pill>
        </span>
      </div>
      <div className="log-line-message whitespace-pre-wrap break-words">
        {highlightText(line.message, query)}
      </div>
    </article>
  );
}

function RedactionModal({ rules, onClose }: { rules: RedactionSummaryResp; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-[rgba(16,18,22,0.56)] p-4" onClick={onClose}>
      <section
        aria-modal="true"
        className="w-full max-w-lg overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="border-b border-[var(--line)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">服务端脱敏规则（共 {rules.rules_count} 条）</h2>
          <p className="muted mt-1 mb-0 text-xs">以下规则对所有返回的日志行永久生效，包括 Tail 和 Stream 模式。</p>
        </div>
        <div className="grid gap-2 p-4 max-h-[60vh] overflow-auto">
          {rules.descriptions.length ? (
            rules.descriptions.map((r, idx) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs" key={idx}>
                <div className="flex items-start justify-between gap-2">
                  <strong className="mono break-all">{r.pattern}</strong>
                  <span className="muted shrink-0">#{idx + 1}</span>
                </div>
                <p className="muted mt-1 mb-0 leading-relaxed">{r.description}</p>
              </div>
            ))
          ) : (
            <EmptyState body="规则描述尚未加载。" title="暂无规则描述" />
          )}
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-4 py-3">
          <Button onClick={onClose}>关闭</Button>
        </div>
      </section>
    </div>
  );
}

// ============================================================================
// ---- Helpers ---------------------------------------------------------------
// ============================================================================

function parseLogLine(raw: string): ParsedLogLine {
  // Best-effort parse. Matches common Go log formats like:
  //   `2024-01-02T15:04:05Z LEVEL msg` or `[LEVEL] ts msg` or `level=LEVEL ts=... msg=...`
  const trimmed = raw.trimEnd();
  if (!trimmed) return { raw, severity: "unknown", timestamp: "", message: raw };

  // RFC3339 + severity
  let m = trimmed.match(/^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\s+(\w+)\s*(.*)$/);
  if (m) {
    return {
      raw,
      timestamp: m[1],
      severity: normalizeSeverity(m[2]),
      message: m[3] || "",
    };
  }

  // [LEVEL] ts msg
  m = trimmed.match(/^\[(\w+)\]\s*(?:(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[^\s]*)\s+)?(.*)$/);
  if (m) {
    return {
      raw,
      severity: normalizeSeverity(m[1]),
      timestamp: m[2] || "",
      message: m[3] || "",
    };
  }

  // level=... key=value
  m = trimmed.match(/level=(\w+)/i);
  const severity = m ? normalizeSeverity(m[1]) : "unknown";
  const tm = trimmed.match(/\b(?:ts|time)=(\S+)/i);
  return { raw, severity, timestamp: tm?.[1] || "", message: trimmed };
}

function normalizeSeverity(raw: string): ParsedLogLine["severity"] {
  const s = raw.toLowerCase();
  if (s === "dbg" || s === "debug") return "debug";
  if (s === "inf" || s === "info") return "info";
  if (s === "warn" || s === "warning") return "warn";
  if (s === "err" || s === "error") return "error";
  if (s === "crit" || s === "critical" || s === "fatal" || s === "panic") return "critical";
  return "unknown";
}

function highlightText(value: string, query: string): ReactNode {
  const text = String(value || "");
  const needle = query.trim();
  if (!needle) return text;
  const lowerText = text.toLowerCase();
  const lowerNeedle = needle.toLowerCase();
  const parts: ReactNode[] = [];
  let cursor = 0;
  let keyIdx = 0;
  while (cursor < text.length) {
    const index = lowerText.indexOf(lowerNeedle, cursor);
    if (index < 0) {
      parts.push(text.slice(cursor));
      break;
    }
    if (index > cursor) parts.push(text.slice(cursor, index));
    parts.push(
      <mark className="log-highlight" key={`hl-${keyIdx++}`}>
        {text.slice(index, index + needle.length)}
      </mark>,
    );
    cursor = index + needle.length;
  }
  return parts;
}
