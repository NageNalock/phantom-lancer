import React, { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AuditEvent, EventRecord, Tone } from "../../app/types";
import { Button, EmptyState, Field, Panel, Pill } from "../../components/ui";
import { auditEvents, eventHistory, friendlyError } from "../../api/client";
import { formatDate } from "../../domain/labels";

type EventKindFilter = "all" | "audit" | "events";
type RiskFilter = "all" | "high" | "medium" | "low";
type FocusFilter = "all" | "emergency" | "config" | "queue" | "delivery";
type TimeFilter = "all" | "1h" | "24h" | "7d";

export function MailEventsTab({ actions }: { actions: AppActions }) {
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [kindFilter, setKindFilter] = useState<EventKindFilter>("all");
  const [riskFilter, setRiskFilter] = useState<RiskFilter>("all");
  const [focusFilter, setFocusFilter] = useState<FocusFilter>("all");
  const [timeFilter, setTimeFilter] = useState<TimeFilter>("24h");
  const [objectQuery, setObjectQuery] = useState("");
  const [expandedId, setExpandedId] = useState("");

  async function load() {
    setLoading(true);
    try {
      const [auditResp, eventResp] = await Promise.all([auditEvents(), eventHistory("mail", "mail", 200).catch(() => ({ items: [] as EventRecord[] }))]);
      setAudit((auditResp.items || []).filter((item) => (item.eventType || "").startsWith("mail.")));
      setEvents((eventResp.items || []).filter((item) => (item.type || "").startsWith("mail.")));
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const rows = useMemo(() => {
    const auditRows = audit.map((item) => ({
      id: `audit:${item.id || item.createdAt || item.eventType}`,
      kind: "audit" as const,
      type: item.eventType || "-",
      summary: item.summary || "-",
      createdAt: item.createdAt,
      tone: riskTone(item.riskLevel),
      badge: item.riskLevel || "audit",
      risk: item.riskLevel || "",
      payload: item.payload,
    }));
    const eventRows = events.map((item) => ({
      id: `event:${item.id || item.sequence || item.createdAt || item.type}`,
      kind: "events" as const,
      type: item.type || "-",
      summary: payloadSummary(item.payload),
      createdAt: item.createdAt,
      tone: "neutral" as Tone,
      badge: "event",
      risk: "",
      payload: item.payload,
    }));
    return [...auditRows, ...eventRows]
      .filter((item) => kindFilter === "all" || item.kind === kindFilter)
      .filter((item) => riskFilter === "all" || item.risk === riskFilter)
      .filter((item) => focusFilter === "all" || matchesFocus(item.type, focusFilter))
      .filter((item) => matchesTime(item.createdAt, timeFilter))
      .filter((item) => matchesObjectQuery(item, objectQuery))
      .sort((a, b) => String(b.createdAt || "").localeCompare(String(a.createdAt || "")));
  }, [audit, events, kindFilter, riskFilter, focusFilter, timeFilter, objectQuery]);

  return (
    <Panel actions={<Button disabled={loading} onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>} subtitle="Mail 模块事件与审计的过滤视图，用于追踪配置变更、证书、队列、Webhook 和运行期状态。" title="事件与审计">
      <div className="mb-3 grid gap-2 text-xs">
        <div className="flex flex-wrap gap-2">
          {(["all", "audit", "events"] as const).map((item) => (
            <button className={`min-h-8 rounded-md border px-3 ${kindFilter === item ? "border-[var(--accent)] bg-[var(--surface)] text-[var(--text)]" : "border-[var(--line)] text-[var(--muted-strong)] hover:bg-[var(--surface)]"}`} key={item} onClick={() => setKindFilter(item)} type="button">
              {item === "all" ? "全部" : item === "audit" ? "审计" : "事件"}
            </button>
          ))}
          {(["emergency", "config", "queue", "delivery"] as const).map((item) => (
            <button className={`min-h-8 rounded-md border px-3 ${focusFilter === item ? "border-[var(--accent)] bg-[var(--surface)] text-[var(--text)]" : "border-[var(--line)] text-[var(--muted-strong)] hover:bg-[var(--surface)]"}`} key={item} onClick={() => setFocusFilter(focusFilter === item ? "all" : item)} type="button">
              {item === "emergency" ? "入站保护" : item === "config" ? "Config apply" : item === "queue" ? "Queue" : "Delivery"}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <Field label="风险">
            <select className="input w-[130px]" value={riskFilter} onChange={(e) => setRiskFilter(e.target.value as RiskFilter)}>
              <option value="all">全部风险</option>
              <option value="high">High</option>
              <option value="medium">Medium</option>
              <option value="low">Low</option>
            </select>
          </Field>
          <Field label="时间">
            <select className="input w-[130px]" value={timeFilter} onChange={(e) => setTimeFilter(e.target.value as TimeFilter)}>
              <option value="1h">最近 1 小时</option>
              <option value="24h">最近 24 小时</option>
              <option value="7d">最近 7 天</option>
              <option value="all">全部</option>
            </select>
          </Field>
          <Field label="对象 ID / 类型">
            <input className="input w-[260px]" value={objectQuery} onChange={(e) => setObjectQuery(e.target.value)} placeholder="job、queue_msg_id、account、domain" />
          </Field>
        </div>
      </div>
      {rows.length ? (
        <div className="overflow-hidden rounded-lg border border-[var(--line)]">
          <table className="w-full text-left text-sm">
            <thead className="bg-[var(--surface-soft)] text-xs text-[var(--muted-strong)]">
              <tr>
                <th className="px-3 py-2 font-medium">类型</th>
                <th className="px-3 py-2 font-medium">摘要</th>
                <th className="px-3 py-2 font-medium">级别</th>
                <th className="px-3 py-2 font-medium">时间</th>
                <th className="px-3 py-2 font-medium">详情</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--line)]">
              {rows.map((row) => (
                <React.Fragment key={row.id}>
                  <tr className="align-top">
                    <td className="mono max-w-[220px] truncate px-3 py-2 text-xs">{row.type}</td>
                    <td className="px-3 py-2 text-sm">{row.summary}</td>
                    <td className="px-3 py-2"><Pill tone={row.tone}>{row.badge}</Pill></td>
                    <td className="whitespace-nowrap px-3 py-2 text-xs text-[var(--muted-strong)]">{formatDate(row.createdAt) || "-"}</td>
                    <td className="px-3 py-2">
                      <Button onClick={() => setExpandedId(expandedId === row.id ? "" : row.id)}>
                        {expandedId === row.id ? "收起" : "展开"}
                      </Button>
                    </td>
                  </tr>
                  {expandedId === row.id ? (
                    <tr key={`${row.id}:payload`}>
                      <td colSpan={5} className="bg-[var(--surface-soft)] px-3 py-3">
                        <pre className="m-0 max-h-[360px] overflow-auto whitespace-pre-wrap break-words rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-xs leading-relaxed">
                          {formatPayload(row.payload)}
                        </pre>
                      </td>
                    </tr>
                  ) : null}
                </React.Fragment>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState title="暂无 Mail 事件" body="Mail 运行、配置、证书和队列操作产生事件后会出现在这里。" />
      )}
    </Panel>
  );
}

function riskTone(risk?: string): Tone {
  if (risk === "high") return "danger";
  if (risk === "medium") return "warn";
  if (risk === "low") return "good";
  return "neutral";
}

function payloadSummary(payload?: Record<string, unknown>): string {
  if (!payload) return "-";
  const keys = Object.keys(payload).slice(0, 4);
  if (!keys.length) return "-";
  return keys.map((key) => `${key}=${summarizePayloadValue(payload[key])}`).join(" · ");
}

function formatPayload(payload?: Record<string, unknown>): string {
  if (!payload) return "{}";
  return JSON.stringify(redactPayloadForDisplay(payload), null, 2);
}

function redactPayloadForDisplay(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactPayloadForDisplay);
  if (!value || typeof value !== "object") {
    if (typeof value === "string") return redactText(value);
    return value;
  }
  const out: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    if (/token|secret|password|cookie|authorization|csrf|api[_-]?key/i.test(key)) {
      out[key] = "[redacted]";
    } else {
      out[key] = redactPayloadForDisplay(item);
    }
  }
  return out;
}

function redactText(value: string): string {
  return value
    .replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [redacted]")
    .replace(/(api[_-]?key|token|secret|password)=([^&\s]+)/gi, "$1=[redacted]");
}

function matchesFocus(type: string, focus: FocusFilter): boolean {
  if (focus === "all") return true;
  if (focus === "emergency") return type.includes(".emergency.");
  if (focus === "config") return type.includes(".config.") || type.includes("configapply");
  if (focus === "queue") return type.includes(".queue.");
  if (focus === "delivery") return type.includes(".delivery.");
  return true;
}

function matchesTime(createdAt: string | undefined, filter: TimeFilter): boolean {
  if (filter === "all" || !createdAt) return true;
  const ts = Date.parse(createdAt);
  if (!Number.isFinite(ts)) return true;
  const age = Date.now() - ts;
  if (filter === "1h") return age <= 60 * 60 * 1000;
  if (filter === "24h") return age <= 24 * 60 * 60 * 1000;
  if (filter === "7d") return age <= 7 * 24 * 60 * 60 * 1000;
  return true;
}

function matchesObjectQuery(item: { type: string; summary: string; payload?: Record<string, unknown> }, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const payload = item.payload ? JSON.stringify(item.payload).toLowerCase() : "";
  return item.type.toLowerCase().includes(q) || item.summary.toLowerCase().includes(q) || payload.includes(q);
}

function summarizePayloadValue(value: unknown): string {
  if (value === null || value === undefined) return "-";
  if (typeof value === "string") return truncateMiddle(value, 160);
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `[${value.length} items]`;
  if (typeof value === "object") return "{...}";
  return truncateMiddle(String(value), 160);
}

function truncateMiddle(value: string, max: number): string {
  if (value.length <= max) return value;
  const keep = Math.max(16, Math.floor((max - 1) / 2));
  return `${value.slice(0, keep)}…${value.slice(value.length - keep)}`;
}
