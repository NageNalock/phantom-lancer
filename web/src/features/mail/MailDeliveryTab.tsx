import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData } from "../../app/types";
import type { Tone } from "../../app/types";
import {
  friendlyError,
  mailDeliveryList,
  mailDeliveryRetry,
  mailDeliveryDelete,
  mailDeliveryPrune,
  mailQueueSummary,
  mailQueueItems,
  mailQueueAction,
  mailSuppressionList,
  mailSuppressionUpsert,
  mailSuppressionDelete,
  mailSuppressionImport,
  mailSuppressionPrune,
  mailWebhookRegister,
  mailWebhookList,
  mailWebhookDelete,
  mailWebhookRotateSecret,
  mailWebhookEvents,
  mailOutboundRate,
  mailOutboundThresholdsList,
  mailOutboundThresholdsUpdate,
  mailDNSBLProbe,
  type MailDeliveryEvent,
  type DeliveryStatus,
  type DeliveryDirection,
  type DeliveryListResp,
  type QueueBucket,
  type MailQueueSummary,
  type MailQueueItem,
  type QueueAction,
  type MailSuppression,
  type SuppressionUpsertReq,
  type MailWebhookRegistration,
  type MailWebhookEvent,
  type WebhookRegisterReq,
  type WebhookRegisterResp,
  type OutboundRateSnapshot,
  type MailOutboundThreshold,
  type DNSBLProbeResp,
  type DNSBLResult,
  type MailRuntimeStatus,
} from "../../api/client";
import {
  Button,
  CheckLabel,
  CollapsibleSection,
  ContextList,
  EmptyState,
  Field,
  Metric,
  Notice,
  Panel,
  Pill,
  SubTabs,
  Toggle,
  useDangerConfirm,
} from "../../components/ui";
import { buildQueryHref } from "../../hooks/useQueryParamState";

type OuterSubtab = "deliveries" | "queue" | "suppression" | "webhooks" | "outbound";

const OUTER_SUBTABS: Array<{ id: OuterSubtab; label: string }> = [
  { id: "deliveries", label: "投递事件" },
  { id: "queue", label: "队列" },
  { id: "suppression", label: "抑制列表" },
  { id: "webhooks", label: "Webhook" },
  { id: "outbound", label: "出站速率" },
];

const DIRECTION_OPTIONS: Array<{ value: DeliveryDirection | ""; label: string }> = [
  { value: "", label: "全部方向" },
  { value: "out", label: "出站" },
  { value: "in", label: "入站" },
  { value: "local", label: "本地" },
];

const STATUS_OPTIONS: Array<{ value: DeliveryStatus | ""; label: string }> = [
  { value: "", label: "全部状态" },
  { value: "sent", label: "已发送" },
  { value: "deferred", label: "延迟重试" },
  { value: "bounced", label: "退信" },
  { value: "suppressed", label: "被抑制" },
  { value: "dropped", label: "已丢弃" },
  { value: "pending", label: "待处理" },
  { value: "queued", label: "已排队" },
];

const BUCKET_LABELS: Record<QueueBucket, string> = {
  hold: "挂起",
  active: "活跃",
  schedule: "计划",
  deferred: "延迟",
  fail: "失败",
  drop: "丢弃",
};

const BUCKET_TONES: Record<QueueBucket, Tone> = {
  hold: "warn",
  active: "good",
  schedule: "neutral",
  deferred: "warn",
  fail: "danger",
  drop: "neutral",
};

const SUPPRESSION_REASONS = ["bounce", "complaint", "unsubscribe", "manual"] as const;
const REASON_LABELS: Record<(typeof SUPPRESSION_REASONS)[number] | "all", string> = {
  all: "全部原因",
  bounce: "退信",
  complaint: "垃圾邮件投诉",
  unsubscribe: "用户退订",
  manual: "手工添加",
};

// ============================================================================
// Main component
// ============================================================================

export function MailDeliveryTab({
  actions,
  reload,
  data,
  status,
  defaultSub = "deliveries",
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  data: AppData;
  status?: MailRuntimeStatus | null;
  defaultSub?: OuterSubtab;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const [outerTab, setOuterTab] = useState<OuterSubtab>(defaultSub);

  // Keep the outer tab in sync if defaultSub changes (e.g. user clicks queue
  // from the top-level MailView tabs).
  useEffect(() => {
    setOuterTab(defaultSub);
  }, [defaultSub]);

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}
      {status?.emergency_inbound_reject?.enabled ? (
        <Notice tone="danger">
          <strong>域禁用降级保护已启用。</strong> 队列处置仍需在本页单独执行；完整状态和恢复操作集中在入站保护页。
          <a className="button ml-3 min-h-8 px-2 text-xs" href={buildQueryHref({ mail: "emergency" }, ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings"])}>
            打开入站保护
          </a>
        </Notice>
      ) : (
        <Notice tone="warn">
          <strong>应急入口。</strong> 如果队列爆量或入站投递异常，可从入站保护页启用 Domain.Disabled 降级保护；正式 early SMTP reject 尚未完成。
          <a className="button ml-3 min-h-8 px-2 text-xs" href={buildQueryHref({ mail: "emergency" }, ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings"])}>
            查看入站保护
          </a>
        </Notice>
      )}
      <SubTabs
        ariaLabel="投递与队列二级导航"
        activeId={outerTab}
        onChange={(id) => setOuterTab(id as OuterSubtab)}
        tabs={OUTER_SUBTABS.map((t) => ({ id: t.id, label: t.label }))}
      />
      {outerTab === "deliveries" ? (
        <DeliveriesPanel actions={actions} reload={reload} confirmDanger={confirmDanger} data={data} />
      ) : null}
      {outerTab === "queue" ? (
        <QueuePanel actions={actions} reload={reload} confirmDanger={confirmDanger} data={data} />
      ) : null}
      {outerTab === "suppression" ? (
        <SuppressionPanel actions={actions} reload={reload} confirmDanger={confirmDanger} data={data} />
      ) : null}
      {outerTab === "webhooks" ? (
        <WebhooksPanel actions={actions} reload={reload} confirmDanger={confirmDanger} />
      ) : null}
      {outerTab === "outbound" ? (
        <OutboundPanel actions={actions} reload={reload} confirmDanger={confirmDanger} />
      ) : null}
    </div>
  );
}

// ============================================================================
// 1. Deliveries sub-tab
// ============================================================================

function DeliveriesPanel({
  actions,
  reload,
  confirmDanger,
  data,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  confirmDanger: ReturnType<typeof useDangerConfirm>["confirmDanger"];
  data: AppData;
}) {
  const [items, setItems] = useState<MailDeliveryEvent[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [nextCursor, setNextCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [pruneOpen, setPruneOpen] = useState(false);

  // Filters
  const [direction, setDirection] = useState<DeliveryDirection | "">("");
  const [status, setStatus] = useState<DeliveryStatus | "">("");
  const [fromDomain, setFromDomain] = useState("");
  const [toDomain, setToDomain] = useState("");
  const [subjectSearch, setSubjectSearch] = useState("");
  const [limit, setLimit] = useState<50 | 100 | 500>(100);

  const agg = useMemo(() => computeDeliveryAggs(items), [items]);
  const sent24h = data.mail.deliverySummary?.sent_24h ?? agg.sent24h;
  const bounced24h = data.mail.deliverySummary?.bounced_24h ?? agg.bounced24h;
  const deferredCount = data.mail.deliverySummary?.deferred_count ?? agg.deferred;
  const pendingCount = data.mail.deliverySummary?.pending_count ?? agg.pending;

  async function load(reset = true) {
    setLoading(true);
    try {
      const q: Parameters<typeof mailDeliveryList>[0] = { limit };
      if (direction) q.direction = direction;
      if (status) q.status = status;
      if (fromDomain.trim()) q.from_domain = fromDomain.trim();
      if (toDomain.trim()) q.to_domain = toDomain.trim();
      if (subjectSearch.trim()) q.subject_contains = subjectSearch.trim();
      if (!reset && nextCursor) q.cursor = nextCursor;
      const resp: DeliveryListResp = await mailDeliveryList(q);
      setItems((prev) => (reset ? resp.items || [] : [...prev, ...(resp.items || [])]));
      setTotalCount(resp.count || 0);
      setNextCursor(resp.next_cursor);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [direction, status, fromDomain, toDomain, subjectSearch, limit]);

  function handleRetryUnavailable() {
    actions.setToast("该投递事件缺少 Mox queue message id，无法自动重新排队。", "warn");
  }

  async function handleRetry(d: MailDeliveryEvent) {
    if (!d.queue_msg_id) {
      handleRetryUnavailable();
      return;
    }
    try {
      const r = await mailDeliveryRetry(d.id, actions.csrf);
      actions.setToast(r.requeued ? `已重新排队：${d.id.slice(0, 8)}…` : "重试请求被忽略", r.requeued ? "good" : "warn");
      await Promise.all([load(true), reload()]);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleDelete(d: MailDeliveryEvent) {
    const ok = await confirmDanger({
      title: "删除投递记录？",
      body: `将删除 ID 以 ${truncateMiddle(d.id, 16)} 标记的投递事件。这不会影响已送达的邮件，仅移除审计追踪。`,
      confirmLabel: "确认删除",
      objectName: d.id,
    });
    if (!ok) return;
    try {
      await mailDeliveryDelete(d.id, actions.csrf);
      actions.setToast("已删除投递记录", "good");
      setItems((old) => old.filter((x) => x.id !== d.id));
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  const detailItem = detailId ? items.find((d) => d.id === detailId) : undefined;

  return (
    <>
      {/* Summary cards */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Metric label="24h 已发送" tone="good" value={sent24h} detail={`共 ${totalCount} 条历史记录`} />
        <Metric label="24h 退信" tone="danger" value={bounced24h} detail="邮件被远端永久拒绝" />
        <Metric label="延迟重试中" tone="warn" value={deferredCount} detail="仍在指数退避窗口内" />
        <Metric label="待处理 / 排队" tone="neutral" value={pendingCount} detail="尚未发出的出站投递" />
      </div>

      {/* Filters + actions row */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <div className="w-[140px]">
            <select
              className="input w-full"
              value={direction}
              onChange={(e) => setDirection(e.target.value as DeliveryDirection | "")}
            >
              {DIRECTION_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          <div className="w-[140px]">
            <select
              className="input w-full"
              value={status}
              onChange={(e) => setStatus(e.target.value as DeliveryStatus | "")}
            >
              {STATUS_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          <input
            className="input w-[180px]"
            placeholder="From 域名包含…"
            value={fromDomain}
            onChange={(e) => setFromDomain(e.target.value)}
          />
          <input
            className="input w-[180px]"
            placeholder="To 域名包含…"
            value={toDomain}
            onChange={(e) => setToDomain(e.target.value)}
          />
          <input
            className="input w-[220px]"
            placeholder="主题片段搜索…"
            value={subjectSearch}
            onChange={(e) => setSubjectSearch(e.target.value)}
          />
          <div className="w-[110px]">
            <select
              className="input w-full"
              value={limit}
              onChange={(e) => setLimit(Number(e.target.value) as 50 | 100 | 500)}
            >
              <option value={50}>每页 50</option>
              <option value={100}>每页 100</option>
              <option value={500}>每页 500</option>
            </select>
          </div>
        </div>
        <div className="flex gap-2">
          <Button tone="neutral" onClick={() => void load(true)}>
            刷新
          </Button>
          <Button tone="danger" onClick={() => setPruneOpen(true)}>
            清理旧数据
          </Button>
        </div>
      </div>

      {/* Table */}
      <Panel
        title="投递事件"
        subtitle="每次入站 / 出站投递都会产生一条事件。退信与延迟条目会随重试自动更新。"
      >
        {loading && items.length === 0 ? (
          <EmptyState title="加载中…" body="" />
        ) : items.length === 0 ? (
          <EmptyState title="暂无投递事件" body="切换筛选条件，或等待 Mox 处理邮件后刷新。" />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)]">
                    <th className="muted px-2 py-2 text-xs font-medium">时间</th>
                    <th className="muted px-2 py-2 text-xs font-medium">方向</th>
                    <th className="muted px-2 py-2 text-xs font-medium">发件域名</th>
                    <th className="muted px-2 py-2 text-xs font-medium">收件域名</th>
                    <th className="muted px-2 py-2 text-xs font-medium">主题</th>
                    <th className="muted px-2 py-2 text-xs font-medium">SMTP</th>
                    <th className="muted px-2 py-2 text-xs font-medium">状态</th>
                    <th className="muted px-2 py-2 text-xs font-medium">尝试</th>
                    <th className="muted px-2 py-2 text-xs font-medium text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((d) => (
                    <tr className="border-b border-[var(--line)] last:border-b-0" key={d.id}>
                      <td className="px-2 py-2 align-top text-xs muted">{relativeTime(d.created_at)}</td>
                      <td className="px-2 py-2 align-top">
                        <Pill tone={directionTone(d.direction)}>{directionLabel(d.direction)}</Pill>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <code className="text-xs break-all">{d.from_domain || "-"}</code>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <code className="text-xs break-all">{d.to_domain || "-"}</code>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <span
                          className="inline-block max-w-[220px] truncate align-bottom"
                          title={d.subject_snippet}
                        >
                          {d.subject_snippet || "(无主题)"}
                        </span>
                      </td>
                      <td className="px-2 py-2 align-top text-xs">
                        {d.smtp_code ? (
                          <code>
                            {d.smtp_code}
                            {d.smtp_enhanced ? ` ${d.smtp_enhanced}` : ""}
                          </code>
                        ) : (
                          "-"
                        )}
                      </td>
                      <td className="px-2 py-2 align-top">
                        <Pill tone={deliveryStatusTone(d.status)}>{deliveryStatusLabel(d.status)}</Pill>
                      </td>
                      <td className="px-2 py-2 align-top text-xs">{d.attempt_count}</td>
                      <td className="px-2 py-2 align-top text-right">
                        <div className="inline-flex flex-wrap justify-end gap-1">
                          <Button tone="neutral" onClick={() => setDetailId(d.id)}>
                            详情
                          </Button>
                          <Button disabled={!d.queue_msg_id} title={d.queue_msg_id ? "重新排入 Mox 队列" : "缺少 Mox queue message id"} tone="neutral" onClick={() => void handleRetry(d)}>
                            重试
                          </Button>
                          <Button tone="danger" onClick={() => void handleDelete(d)}>
                            删除
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {nextCursor ? (
              <div className="pt-3 text-right">
                <Button
                  tone="neutral"
                  disabled={loading}
                  onClick={() => void load(false)}
                >
                  {loading ? "加载中…" : `加载更多（当前 ${items.length} / ${totalCount}）`}
                </Button>
              </div>
            ) : null}
          </>
        )}
      </Panel>

      {/* Detail drawer */}
      {detailItem ? (
        <DrawerModal title={`投递详情 — ${truncateMiddle(detailItem.id, 24)}`} onClose={() => setDetailId(null)}>
          <div className="grid gap-4">
            <ContextList
              items={[
                ["ID", <code key="id" className="break-all">{detailItem.id}</code>],
                ["方向", <Pill key="dir" tone={directionTone(detailItem.direction)}>{directionLabel(detailItem.direction)}</Pill>],
                ["状态", <Pill key="st" tone={deliveryStatusTone(detailItem.status)}>{deliveryStatusLabel(detailItem.status)}</Pill>],
                ["发件域名", <code key="fd">{detailItem.from_domain || "-"}</code>],
                ["收件域名", <code key="td">{detailItem.to_domain || "-"}</code>],
                ["Message-ID 哈希", <code key="mid" className="break-all">{truncateMiddle(detailItem.message_id_hash, 48)}</code>],
                ["Queue Msg ID", detailItem.queue_msg_id ? <code key="qid">{detailItem.queue_msg_id}</code> : "-"],
                ["主题", <span key="sbj">{detailItem.subject_snippet || "(无主题)"}</span>],
                ["收件人哈希", <code key="rh">{detailItem.recipient_hash ? truncateMiddle(detailItem.recipient_hash, 28) : "-"}</code>],
                ["SMTP 代码", detailItem.smtp_code ? (
                  <code key="sm">{`${detailItem.smtp_code}${detailItem.smtp_enhanced ? " " + detailItem.smtp_enhanced : ""}`}</code>
                ) : "-"],
                ["尝试次数", String(detailItem.attempt_count)],
                ["首次尝试", formatDateTime(detailItem.first_attempt_at)],
                ["最后尝试", formatDateTime(detailItem.last_attempt_at)],
                ["完成时间", formatDateTime(detailItem.completed_at)],
                ["创建时间", formatDateTime(detailItem.created_at)],
              ]}
            />
            {detailItem.redacted_error ? (
              <Notice tone="danger">
                <strong>错误详情（已脱敏）</strong>
                <pre className="mt-2 mb-0 whitespace-pre-wrap break-all text-xs">{detailItem.redacted_error}</pre>
              </Notice>
            ) : null}
            <div className="flex justify-end gap-2 border-t pt-3">
              <Button disabled={!detailItem.queue_msg_id} title={detailItem.queue_msg_id ? "重新排入 Mox 队列" : "缺少 Mox queue message id"} tone="neutral" onClick={() => void handleRetry(detailItem)}>
                重新投递
              </Button>
              <Button tone="danger" onClick={() => void handleDelete(detailItem)}>
                删除记录
              </Button>
              <Button tone="primary" onClick={() => setDetailId(null)}>
                关闭
              </Button>
            </div>
          </div>
        </DrawerModal>
      ) : null}

      {/* Prune modal */}
      {pruneOpen ? (
        <PruneDeliveryModal
          csrf={actions.csrf}
          setToast={actions.setToast}
          onClose={() => setPruneOpen(false)}
          onDone={() => {
            setPruneOpen(false);
            void Promise.all([load(true), reload()]);
          }}
        />
      ) : null}
    </>
  );
}

function PruneDeliveryModal({
  csrf,
  setToast,
  onClose,
  onDone,
}: {
  csrf?: string;
  setToast: AppActions["setToast"];
  onClose: () => void;
  onDone: () => void;
}) {
  const [days, setDays] = useState<number>(90);
  const [busy, setBusy] = useState(false);

  async function submit() {
    const d = Math.max(1, Number(days) || 90);
    setBusy(true);
    try {
      const r = await mailDeliveryPrune({ days: d }, csrf);
      setToast(`已清理 ${r.pruned_count} 条早于 ${d} 天的投递记录`, "good");
      onDone();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="清理投递记录" onClose={onClose}>
      <Field label="保留天数（仅保留最近 N 天）" help="建议值：90。删除后无法恢复，仅应在存储告急或合规场景下使用。">
        <input
          className="input"
          type="number"
          min={1}
          placeholder="90"
          value={days}
          onChange={(e) => setDays(Number(e.target.value))}
        />
      </Field>
      <Notice tone="danger">
        <strong>破坏性操作</strong>
        <p className="mt-1 mb-0 text-xs leading-relaxed">
          将永久删除 <code>created_at</code> 早于保留窗口的全部投递事件。
          审计追踪也会一并丢失。请在操作前完成数据备份。
        </p>
      </Notice>
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button tone="neutral" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button tone="danger" onClick={() => void submit()} disabled={busy}>
          {busy ? "清理中…" : "确认清理"}
        </Button>
      </div>
    </ModalShell>
  );
}

// ============================================================================
// 2. Queue sub-tab
// ============================================================================

function QueuePanel({
  actions,
  reload,
  confirmDanger,
  data,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  confirmDanger: ReturnType<typeof useDangerConfirm>["confirmDanger"];
  data: AppData;
}) {
  const buckets: QueueBucket[] = ["active", "deferred", "hold", "schedule", "fail", "drop"];
  const [summary, setSummary] = useState<MailQueueSummary | null>(data.mail.queueSummary || null);
  const [activeBucket, setActiveBucket] = useState<QueueBucket>("active");
  const [items, setItems] = useState<MailQueueItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [nextCursor, setNextCursor] = useState<string | undefined>();

  async function loadSummary() {
    try {
      const r = await mailQueueSummary();
      setSummary(r);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function loadItems(reset = true) {
    setLoading(true);
    try {
      const r = await mailQueueItems({
        bucket: activeBucket,
        limit: 200,
        cursor: reset ? undefined : nextCursor,
      });
      setItems((prev) => (reset ? r.items || [] : [...prev, ...(r.items || [])]));
      setNextCursor(r.next_cursor);
      setSelected(new Set());
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadSummary();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    void loadItems(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeBucket]);

  function toggleSelected(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleSelectAll() {
    if (selected.size === items.length) {
      setSelected(new Set());
    } else {
      setSelected(new Set(items.map((i) => i.id)));
    }
  }

  async function executeAction(action: QueueAction) {
    const ids = Array.from(selected);
    if (!ids.length) {
      actions.setToast("请先勾选至少一条队列项", "warn");
      return;
    }
    const actionLabel = actionLabelFor(action);
    const needsConfirm = action === "fail" || action === "drop";
    if (needsConfirm) {
      const confirmText = `I CONFIRM ${ids.length} × ${action.toUpperCase()}`;
      const ok = await confirmDanger({
        title: `${actionLabel} ${ids.length} 条队列项？`,
        body:
          action === "fail"
            ? "这些邮件将被标记为永久失败（硬退信），收件人会收到退信通知。"
            : "这些邮件将被静默丢弃，不产生任何退信。",
        confirmLabel: `确认${actionLabel}`,
        confirmationText: confirmText,
        confirmationLabel: `请输入：${confirmText}`,
        confirmationPlaceholder: confirmText,
        impact: ids.length > 20 ? [`影响 ${ids.length} 条队列项`] : ids.map((id) => truncateMiddle(id, 18)),
      });
      if (!ok) return;
    }
    try {
      const r = await mailQueueAction(action, ids, actions.csrf);
      actions.setToast(`${actionLabel}成功：${r.updated_count} 条`, "good");
      await Promise.all([loadSummary(), loadItems(true), reload()]);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  const hasSelected = selected.size > 0;

  return (
    <>
      {/* 6 metric cards */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        {buckets.map((b) => (
          <Metric
            key={b}
            label={`${BUCKET_LABELS[b]} (${b})`}
            tone={BUCKET_TONES[b]}
            value={summary?.[b] ?? 0}
            onClick={() => setActiveBucket(b)}
          />
        ))}
      </div>

      {/* Inner SubTabs */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <SubTabs
          ariaLabel="队列桶"
          activeId={activeBucket}
          onChange={(id) => setActiveBucket(id as QueueBucket)}
          tabs={buckets.map((b) => ({
            id: b,
            label: BUCKET_LABELS[b],
            badge: summary ? String(summary[b]) : undefined,
          }))}
          rightSlot={
            <Button tone="neutral" onClick={() => { void loadSummary(); void loadItems(true); }}>
              刷新
            </Button>
          }
        />
      </div>

      {/* Bulk action bar */}
      {hasSelected ? (
        <div className="flex items-center justify-between gap-2 flex-wrap rounded-lg border border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-2">
          <span className="text-sm">
            已选择 <strong>{selected.size}</strong> 条，批量操作：
          </span>
          <div className="flex flex-wrap gap-1">
            <Button tone="neutral" onClick={() => void executeAction("hold")}>
              挂起 Hold
            </Button>
            <Button tone="neutral" onClick={() => void executeAction("unhold")}>
              解除挂起 Unhold
            </Button>
            <Button tone="primary" onClick={() => void executeAction("schedule")}>
              调度 Schedule
            </Button>
            <Button tone="danger" onClick={() => void executeAction("fail")}>
              失败 Fail
            </Button>
            <Button tone="danger" onClick={() => void executeAction("drop")}>
              丢弃 Drop
            </Button>
          </div>
        </div>
      ) : null}

      <Panel
        title={`队列桶：${BUCKET_LABELS[activeBucket]}`}
        subtitle="队列项按 Mox 的桶分类。勾选后使用上方批量操作执行 hold / schedule / fail / drop。"
      >
        {loading && items.length === 0 ? (
          <EmptyState title="加载中…" body="" />
        ) : items.length === 0 ? (
          <EmptyState title={`${BUCKET_LABELS[activeBucket]} 桶为空`} body="等待 Mox 投递流水线产生队列项后刷新。" />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)]">
                    <th className="px-2 py-2 w-[32px]">
                      <input
                        type="checkbox"
                        checked={selected.size === items.length && items.length > 0}
                        onChange={toggleSelectAll}
                        aria-label="全选"
                      />
                    </th>
                    <th className="muted px-2 py-2 text-xs font-medium">ID</th>
                    <th className="muted px-2 py-2 text-xs font-medium">桶</th>
                    <th className="muted px-2 py-2 text-xs font-medium">Envelope From</th>
                    <th className="muted px-2 py-2 text-xs font-medium">Envelope To (hash)</th>
                    <th className="muted px-2 py-2 text-xs font-medium">状态</th>
                    <th className="muted px-2 py-2 text-xs font-medium">计划时间</th>
                    <th className="muted px-2 py-2 text-xs font-medium">尝试</th>
                    <th className="muted px-2 py-2 text-xs font-medium">创建</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((it) => (
                    <tr className="border-b border-[var(--line)] last:border-b-0" key={it.id}>
                      <td className="px-2 py-2">
                        <input
                          type="checkbox"
                          checked={selected.has(it.id)}
                          onChange={() => toggleSelected(it.id)}
                          aria-label={`选中 ${it.id}`}
                        />
                      </td>
                      <td className="px-2 py-2 align-top">
                        <code className="text-xs" title={it.id}>
                          {truncateMiddle(it.id, 16)}
                        </code>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <Pill tone={BUCKET_TONES[it.bucket]}>{BUCKET_LABELS[it.bucket]}</Pill>
                      </td>
                      <td className="px-2 py-2 align-top">
                        {it.envelope_from_domain ? (
                          <code className="text-xs break-all">{it.envelope_from_domain}</code>
                        ) : (
                          <span className="muted text-xs">-</span>
                        )}
                      </td>
                      <td className="px-2 py-2 align-top">
                        {it.envelope_to_hash ? (
                          <code className="text-xs" title={it.envelope_to_hash}>
                            {truncateMiddle(it.envelope_to_hash, 18)}
                          </code>
                        ) : (
                          <span className="muted text-xs">-</span>
                        )}
                      </td>
                      <td className="px-2 py-2 align-top text-xs">
                        {it.status || "-"}
                      </td>
                      <td className="px-2 py-2 align-top text-xs muted">
                        {it.scheduled_at ? relativeTime(it.scheduled_at) : "-"}
                      </td>
                      <td className="px-2 py-2 align-top text-xs">{it.attempt_count}</td>
                      <td className="px-2 py-2 align-top text-xs muted">{relativeTime(it.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {nextCursor ? (
              <div className="pt-3 text-right">
                <Button tone="neutral" disabled={loading} onClick={() => void loadItems(false)}>
                  加载更多
                </Button>
              </div>
            ) : null}
          </>
        )}
      </Panel>
    </>
  );
}

// ============================================================================
// 3. Suppression sub-tab
// ============================================================================

function SuppressionPanel({
  actions,
  reload,
  confirmDanger,
  data,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  confirmDanger: ReturnType<typeof useDangerConfirm>["confirmDanger"];
  data: AppData;
}) {
  const [items, setItems] = useState<MailSuppression[]>([]);
  const [count, setCount] = useState(0);
  const [nextCursor, setNextCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);

  // filters
  const [activeOnly, setActiveOnly] = useState(false);
  const [reasonFilter, setReasonFilter] = useState<"all" | (typeof SUPPRESSION_REASONS)[number]>("all");
  const [searchHex, setSearchHex] = useState("");

  // modals
  const [addOpen, setAddOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const domOpts = useMemo(() => (data.mail.domains || []).map((d) => ({ value: d.id, label: d.domain })), [data.mail.domains]);
  const [domainFilter, setDomainFilter] = useState("");

  const activeCount = data.mail.suppressionSummary?.active_count ?? items.filter((s) => s.active).length;
  const added7d = data.mail.suppressionSummary?.added_7d ?? items.filter((s) => withinDays(s.added_at, 7)).length;
  const expiringSoon = data.mail.suppressionSummary?.expiring_soon ?? items.filter((s) => s.expires_at && withinDays(s.expires_at, 14)).length;

  async function load(reset = true) {
    setLoading(true);
    try {
      const q: Parameters<typeof mailSuppressionList>[0] = { limit: 200 };
      if (activeOnly) q.active = "true";
      if (reasonFilter !== "all") q.reason = reasonFilter;
      if (domainFilter) q.domain_id = domainFilter;
      if (searchHex.trim()) q.recipient_prefix = searchHex.trim();
      if (!reset && nextCursor) q.cursor = nextCursor;
      const r = await mailSuppressionList(q);
      setItems((prev) => (reset ? r.items || [] : [...prev, ...(r.items || [])]));
      setCount(r.count ?? 0);
      setNextCursor(r.next_cursor);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeOnly, reasonFilter, domainFilter, searchHex]);

  async function handleDelete(s: MailSuppression) {
    const ok = await confirmDanger({
      title: "移除此收件人的抑制？",
      body: `移除后，Mox 将恢复向该收件人地址投递。如果抑制原因仍然存在，可能很快再次退信。`,
      confirmLabel: "确认移除",
      objectName: `${truncateMiddle(s.recipient_hash, 20)} · ${s.reason || "无原因"}`,
    });
    if (!ok) return;
    try {
      await mailSuppressionDelete(s.id, actions.csrf);
      actions.setToast("已移除抑制", "good");
      setItems((old) => old.filter((x) => x.id !== s.id));
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handlePrune() {
    const ok = await confirmDanger({
      title: "清理全部过期抑制条目？",
      body: "将移除所有 expires_at 早于当前时间、以及 active=false 的历史条目。这不会影响仍在生效的抑制。",
      confirmLabel: "确认清理",
    });
    if (!ok) return;
    try {
      const r = await mailSuppressionPrune(actions.csrf);
      actions.setToast(`已清理 ${r.pruned_count} 条过期抑制`, "good");
      await Promise.all([load(true), reload()]);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  return (
    <>
      <div className="grid grid-cols-3 gap-3">
        <Metric label="生效中" tone="warn" value={activeCount} detail={`共 ${count} 条记录`} />
        <Metric label="7 天内新增" tone="neutral" value={added7d} detail="近 7 天加入抑制的收件人" />
        <Metric label="2 周内到期" tone="good" value={expiringSoon} detail="可被定期清理的条目" />
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <CheckLabel checked={activeOnly} onChange={setActiveOnly}>
            仅显示生效中
          </CheckLabel>
          <div className="flex items-center gap-1">
            {(["all", ...SUPPRESSION_REASONS] as const).map((r) => (
              <Pill
                key={r}
                tone={reasonFilter === r ? "good" : "neutral"}
              >
                <button
                  type="button"
                  className="px-1 py-0.5"
                  onClick={() => setReasonFilter(r)}
                  style={{ background: "transparent", border: "none", cursor: "pointer", font: "inherit", color: "inherit" }}
                >
                  {REASON_LABELS[r]}
                </button>
              </Pill>
            ))}
          </div>
          <select
            className="input w-[180px]"
            value={domainFilter}
            onChange={(e) => setDomainFilter(e.target.value)}
          >
            <option value="">所有域名</option>
            {domOpts.map((d) => (
              <option key={d.value} value={d.value}>
                {d.label}
              </option>
            ))}
          </select>
          <input
            className="input w-[220px]"
            placeholder="收件人哈希前缀 (hex)…"
            value={searchHex}
            onChange={(e) => setSearchHex(e.target.value)}
          />
        </div>
        <div className="flex flex-wrap gap-2">
          <Button tone="primary" onClick={() => setAddOpen(true)}>
            + 添加抑制
          </Button>
          <Button tone="neutral" onClick={() => setImportOpen(true)}>
            批量导入
          </Button>
          <Button tone="danger" onClick={() => void handlePrune()}>
            清理过期
          </Button>
        </div>
      </div>

      <Panel title="抑制列表" subtitle="收件人地址（以 SHA-256 存储）命中该列表时，出站投递会被直接跳过。">
        {loading && items.length === 0 ? (
          <EmptyState title="加载中…" body="" />
        ) : items.length === 0 ? (
          <EmptyState title="抑制列表为空" body="发生退信或用户投诉后，系统会自动加入抑制；也可以手动添加。" />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)]">
                    <th className="muted px-2 py-2 text-xs font-medium">收件人哈希</th>
                    <th className="muted px-2 py-2 text-xs font-medium">原因</th>
                    <th className="muted px-2 py-2 text-xs font-medium">来源</th>
                    <th className="muted px-2 py-2 text-xs font-medium">添加时间</th>
                    <th className="muted px-2 py-2 text-xs font-medium">过期</th>
                    <th className="muted px-2 py-2 text-xs font-medium">状态</th>
                    <th className="muted px-2 py-2 text-xs font-medium text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((s) => (
                    <tr className="border-b border-[var(--line)] last:border-b-0" key={s.id}>
                      <td className="px-2 py-2 align-top">
                        <div className="flex items-center gap-1">
                          <code className="text-xs" title={s.recipient_hash}>
                            {truncateMiddle(s.recipient_hash, 28)}
                          </code>
                          <CopyButton value={s.recipient_hash} label="复制完整哈希" />
                        </div>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <Pill tone={suppressionReasonTone(s.reason)}>
                          {s.reason ? REASON_LABELS[s.reason as (typeof SUPPRESSION_REASONS)[number]] || s.reason : "未指定"}
                        </Pill>
                      </td>
                      <td className="px-2 py-2 align-top text-xs">{s.source || "-"}</td>
                      <td className="px-2 py-2 align-top text-xs muted">{relativeTime(s.added_at)}</td>
                      <td className="px-2 py-2 align-top text-xs muted">
                        {s.expires_at ? relativeTime(s.expires_at) : "永不过期"}
                      </td>
                      <td className="px-2 py-2 align-top">
                        <Pill tone={s.active ? "good" : "neutral"}>{s.active ? "生效" : "已失效"}</Pill>
                      </td>
                      <td className="px-2 py-2 align-top text-right">
                        <Button tone="danger" onClick={() => void handleDelete(s)}>
                          移除
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {nextCursor ? (
              <div className="pt-3 text-right">
                <Button tone="neutral" disabled={loading} onClick={() => void load(false)}>
                  加载更多
                </Button>
              </div>
            ) : null}
          </>
        )}
      </Panel>

      {addOpen ? (
        <AddSuppressionModal
          csrf={actions.csrf}
          setToast={actions.setToast}
          domainOptions={domOpts}
          onClose={() => setAddOpen(false)}
          onDone={() => {
            setAddOpen(false);
            void Promise.all([load(true), reload()]);
          }}
        />
      ) : null}
      {importOpen ? (
        <ImportSuppressionModal
          csrf={actions.csrf}
          setToast={actions.setToast}
          onClose={() => setImportOpen(false)}
          onDone={() => {
            setImportOpen(false);
            void Promise.all([load(true), reload()]);
          }}
        />
      ) : null}
    </>
  );
}

function AddSuppressionModal({
  csrf,
  setToast,
  domainOptions,
  onClose,
  onDone,
}: {
  csrf?: string;
  setToast: AppActions["setToast"];
  domainOptions: Array<{ value: string; label: string }>;
  onClose: () => void;
  onDone: () => void;
}) {
  const [form, setForm] = useState<SuppressionUpsertReq>({
    recipient_hash: "",
    reason: "manual",
    source: "phantom-ui",
    active: true,
  });
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!/^[0-9a-fA-F]{64}$/.test(form.recipient_hash.trim())) {
      setToast("请输入 64 位十六进制的收件人 SHA-256 哈希", "warn");
      return;
    }
    setBusy(true);
    try {
      const req: SuppressionUpsertReq = { ...form, recipient_hash: form.recipient_hash.trim().toLowerCase() };
      await mailSuppressionUpsert(req, csrf);
      setToast("已添加抑制", "good");
      onDone();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="添加抑制条目" onClose={onClose}>
      <Field label="收件人哈希 (SHA-256, 64 hex)" help="对收件人邮箱地址（全小写）执行 SHA-256 后的十六进制结果。">
        <input
          className="input mono"
          placeholder="例如：5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"
          value={form.recipient_hash}
          onChange={(e) => setForm({ ...form, recipient_hash: e.target.value })}
        />
      </Field>
      <Field label="原因">
        <select
          className="input w-full"
          value={form.reason || ""}
          onChange={(e) => setForm({ ...form, reason: e.target.value })}
        >
          {SUPPRESSION_REASONS.map((r) => (
            <option key={r} value={r}>
              {REASON_LABELS[r]}
            </option>
          ))}
        </select>
      </Field>
      <Field label="关联域名 (可选)">
        <select
          className="input w-full"
          value={form.domain_id || ""}
          onChange={(e) => setForm({ ...form, domain_id: e.target.value || undefined })}
        >
          <option value="">全局抑制</option>
          {domainOptions.map((d) => (
            <option key={d.value} value={d.value}>
              {d.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="来源标记 (可选)">
        <input
          className="input"
          placeholder="例如：complaint-feedback-loop"
          value={form.source || ""}
          onChange={(e) => setForm({ ...form, source: e.target.value })}
        />
      </Field>
      <Field label="过期时间 (可选，ISO 格式)">
        <input
          type="datetime-local"
          className="input"
          value={form.expires_at ? toLocalInput(form.expires_at) : ""}
          onChange={(e) => setForm({ ...form, expires_at: e.target.value ? fromLocalInput(e.target.value) : undefined })}
        />
      </Field>
      <Toggle checked={!!form.active} onChange={(v) => setForm({ ...form, active: v })} label="立即使该抑制生效" />
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button tone="neutral" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button tone="primary" onClick={() => void submit()} disabled={busy}>
          {busy ? "提交中…" : "添加"}
        </Button>
      </div>
    </ModalShell>
  );
}

function ImportSuppressionModal({
  csrf,
  setToast,
  onClose,
  onDone,
}: {
  csrf?: string;
  setToast: AppActions["setToast"];
  onClose: () => void;
  onDone: () => void;
}) {
  const [text, setText] = useState("");
  const [reason, setReason] = useState<(typeof SUPPRESSION_REASONS)[number]>("bounce");
  const [busy, setBusy] = useState(false);

  async function submit() {
    const hashes = text
      .split(/[\s,;]+/)
      .map((s) => s.trim().toLowerCase())
      .filter((s) => /^[0-9a-f]{64}$/.test(s));
    if (!hashes.length) {
      setToast("未找到合法的 64 位十六进制哈希（每行一个或空格分隔）", "warn");
      return;
    }
    setBusy(true);
    try {
      const r = await mailSuppressionImport(
        {
          entries: hashes.map((h) => ({
            recipient_hash: h,
            reason,
            source: "bulk-import",
            active: true,
          })),
        },
        csrf,
      );
      setToast(`成功导入 ${r.imported_count} / ${hashes.length} 条抑制`, "good");
      onDone();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="批量导入抑制" onClose={onClose}>
      <Field label="收件人哈希列表" help="每行一个 64 位 hex，或用空格 / 逗号分隔。非法行会被静默跳过。">
        <textarea
          className="input mono"
          rows={10}
          placeholder={`5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n...`}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </Field>
      <Field label="统一原因">
        <select
          className="input w-full"
          value={reason}
          onChange={(e) => setReason(e.target.value as (typeof SUPPRESSION_REASONS)[number])}
        >
          {SUPPRESSION_REASONS.map((r) => (
            <option key={r} value={r}>
              {REASON_LABELS[r]}
            </option>
          ))}
        </select>
      </Field>
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button tone="neutral" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button tone="primary" onClick={() => void submit()} disabled={busy}>
          {busy ? "导入中…" : "确认导入"}
        </Button>
      </div>
    </ModalShell>
  );
}

// ============================================================================
// 4. Webhooks sub-tab
// ============================================================================

function WebhooksPanel({
  actions,
  confirmDanger,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  confirmDanger: ReturnType<typeof useDangerConfirm>["confirmDanger"];
}) {
  const [items, setItems] = useState<MailWebhookRegistration[]>([]);
  const [events, setEvents] = useState<MailWebhookEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [registerOpen, setRegisterOpen] = useState(false);
  const [secretResult, setSecretResult] = useState<WebhookRegisterResp | { one_time_secret: string; registration?: MailWebhookRegistration } | null>(null);

  async function load() {
    setLoading(true);
    try {
      const [regs, evts] = await Promise.all([mailWebhookList(), mailWebhookEvents(100)]);
      setItems(regs || []);
      setEvents(evts || []);
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

  async function handleDelete(r: MailWebhookRegistration) {
    const ok = await confirmDanger({
      title: "删除 Webhook 注册？",
      body: `名称「${r.name}」的注册会被移除，相关 HMAC 共享密钥立即失效。`,
      confirmLabel: "确认删除",
      objectName: r.id,
    });
    if (!ok) return;
    try {
      await mailWebhookDelete(r.id, actions.csrf);
      actions.setToast(`已删除 Webhook：${r.name}`, "good");
      setItems((old) => old.filter((x) => x.id !== r.id));
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleRotate(r: MailWebhookRegistration) {
    try {
      const s = await mailWebhookRotateSecret(r.id, actions.csrf);
      setSecretResult({ one_time_secret: s.one_time_secret, registration: r });
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function onRegistered(resp: WebhookRegisterResp) {
    setRegisterOpen(false);
    setSecretResult(resp);
    await load();
  }

  return (
    <>
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <strong>Webhook 注册</strong>
          <div className="muted text-xs mt-1">
            共 <strong>{items.length}</strong> 个注册。
            最近 <strong>{events.length}</strong> 条事件。
          </div>
        </div>
        <Button tone="primary" onClick={() => setRegisterOpen(true)}>
          + 注册 Webhook
        </Button>
      </div>

      <Panel title="Webhook 列表" subtitle="配置 Mox 出/入站 Webhook 以及 HMAC 共享密钥。">
        {loading && items.length === 0 ? (
          <EmptyState title="加载中…" body="" />
        ) : items.length === 0 ? (
          <EmptyState title="尚未注册 Webhook" body="注册后即可接收投递状态推送，或允许外部服务向 Mox 提交入站 Webhook。" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-[var(--line)]">
                  <th className="muted px-2 py-2 text-xs font-medium">名称</th>
                  <th className="muted px-2 py-2 text-xs font-medium">方向</th>
                  <th className="muted px-2 py-2 text-xs font-medium">签名算法</th>
                  <th className="muted px-2 py-2 text-xs font-medium">来源 CIDR</th>
                  <th className="muted px-2 py-2 text-xs font-medium">Body 上限</th>
                  <th className="muted px-2 py-2 text-xs font-medium">启用</th>
                  <th className="muted px-2 py-2 text-xs font-medium">创建时间</th>
                  <th className="muted px-2 py-2 text-xs font-medium text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((r) => (
                  <tr className="border-b border-[var(--line)] last:border-b-0" key={r.id}>
                    <td className="px-2 py-2 align-top font-medium">{r.name}</td>
                    <td className="px-2 py-2 align-top">
                      <Pill tone={r.direction === "in" ? "good" : "warn"}>
                        {r.direction === "in" ? "入站 (from Mox 上游)" : "出站 (to 下游)"}
                      </Pill>
                    </td>
                    <td className="px-2 py-2 align-top text-xs">
                      <code>{r.signing_alg || "-"}</code>
                    </td>
                    <td className="px-2 py-2 align-top text-xs">
                      <code>{r.source_cidr || "-"}</code>
                    </td>
                    <td className="px-2 py-2 align-top text-xs">{humanSize(r.max_body_bytes)}</td>
                    <td className="px-2 py-2 align-top">
                      <Pill tone={r.enabled ? "good" : "neutral"}>{r.enabled ? "启用" : "禁用"}</Pill>
                    </td>
                    <td className="px-2 py-2 align-top text-xs muted">{relativeTime(r.created_at)}</td>
                    <td className="px-2 py-2 align-top text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1">
                        <Button tone="neutral" onClick={() => void handleRotate(r)}>
                          轮换密钥
                        </Button>
                        <Button tone="danger" onClick={() => void handleDelete(r)}>
                          删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <CollapsibleSection
        defaultOpen={events.length > 0}
        subtitle="最近的 100 条 Webhook 事件，用于排错。"
        title="最近 Webhook 事件"
      >
        {events.length === 0 ? (
          <EmptyState title="暂无事件" body="等待 Webhook 注册产生入站或出站事件。" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-[var(--line)]">
                  <th className="muted px-2 py-2 text-xs font-medium">时间</th>
                  <th className="muted px-2 py-2 text-xs font-medium">方向</th>
                  <th className="muted px-2 py-2 text-xs font-medium">事件类型</th>
                  <th className="muted px-2 py-2 text-xs font-medium">状态</th>
                  <th className="muted px-2 py-2 text-xs font-medium">HMAC</th>
                  <th className="muted px-2 py-2 text-xs font-medium">来源 IP</th>
                  <th className="muted px-2 py-2 text-xs font-medium">Payload</th>
                </tr>
              </thead>
              <tbody>
                {events.map((e) => (
                  <tr className="border-b border-[var(--line)] last:border-b-0" key={e.id}>
                    <td className="px-2 py-2 align-top text-xs muted">{relativeTime(e.created_at)}</td>
                    <td className="px-2 py-2 align-top">
                      <Pill tone={e.direction === "in" ? "good" : "warn"}>{e.direction}</Pill>
                    </td>
                    <td className="px-2 py-2 align-top text-xs">{e.event_type}</td>
                    <td className="px-2 py-2 align-top">
                      <Pill tone={webhookEventStatusTone(e.status)}>{e.status}</Pill>
                    </td>
                    <td className="px-2 py-2 align-top">
                      <Pill tone={e.hmac_valid ? "good" : "danger"}>
                        {e.hmac_valid ? "签名合法" : "签名无效"}
                      </Pill>
                    </td>
                    <td className="px-2 py-2 align-top text-xs" title={e.source_addr || ""}>
                      {e.source_addr ? truncateMiddle(e.source_addr, 20) : "-"}
                    </td>
                    <td className="px-2 py-2 align-top text-xs">
                      <code>{e.payload_hash ? truncateMiddle(e.payload_hash, 12) : "-"}</code>
                      <span className="ml-1 muted">({humanSize(e.payload_size)})</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CollapsibleSection>

      {registerOpen ? (
        <RegisterWebhookModal
          csrf={actions.csrf}
          setToast={actions.setToast}
          onClose={() => setRegisterOpen(false)}
          onDone={onRegistered}
        />
      ) : null}

      {secretResult ? (
        <SecretResultModal
          name={secretResult.registration?.name || "(轮换)"}
          secret={secretResult.one_time_secret}
          onClose={() => setSecretResult(null)}
        />
      ) : null}
    </>
  );
}

function RegisterWebhookModal({
  csrf,
  setToast,
  onClose,
  onDone,
}: {
  csrf?: string;
  setToast: AppActions["setToast"];
  onClose: () => void;
  onDone: (resp: WebhookRegisterResp) => void;
}) {
  const [form, setForm] = useState<Required<WebhookRegisterReq>>({
    name: "",
    direction: "out",
    url: "",
    source_cidr: "127.0.0.1/32",
    signing_alg: "hmac-sha256",
    max_body_bytes: 1048576,
    event_mask: "*",
  });
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!form.name.trim()) {
      setToast("名称不能为空", "warn");
      return;
    }
    if (form.direction === "out" && !form.url.trim()) {
      setToast("出站 Webhook 需要填写推送 URL", "warn");
      return;
    }
    setBusy(true);
    try {
      const payload: WebhookRegisterReq = { ...form, name: form.name.trim(), url: form.url.trim() || undefined };
      const r = await mailWebhookRegister(payload, csrf);
      setToast("已创建 Webhook 注册", "good");
      onDone(r);
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="注册 Webhook" onClose={onClose}>
      <Field label="名称">
        <input
          className="input"
          placeholder="例如：delivery-status-to-crm"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
      </Field>
      <Field label="方向" help="入站 = 允许外部服务通过 Webhook 向 Mox 提交事件；出站 = Mox 向远端 URL 投递事件。">
        <div className="flex gap-3">
          <CheckLabel
            checked={form.direction === "in"}
            onChange={(v) => {
              if (v) setForm({ ...form, direction: "in" });
            }}
          >
            入站（from Mox 上游）
          </CheckLabel>
          <CheckLabel
            checked={form.direction === "out"}
            onChange={(v) => {
              if (v) setForm({ ...form, direction: "out" });
            }}
          >
            出站（to 下游系统）
          </CheckLabel>
        </div>
      </Field>
      {form.direction === "out" ? (
        <Field label="推送 URL">
          <input
            className="input"
            placeholder="https://example.com/api/mox/events"
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.target.value })}
          />
        </Field>
      ) : null}
      <Field label="来源 CIDR (入站生效)" help="仅允许该 CIDR 范围内的 IP 提交入站 Webhook。">
        <input
          className="input mono"
          placeholder="127.0.0.1/32"
          value={form.source_cidr}
          onChange={(e) => setForm({ ...form, source_cidr: e.target.value })}
        />
      </Field>
      <Field label="签名算法">
        <select
          className="input w-full"
          value={form.signing_alg}
          onChange={(e) => setForm({ ...form, signing_alg: e.target.value })}
        >
          <option value="hmac-sha256">HMAC-SHA256</option>
          <option value="hmac-sha1">HMAC-SHA1 (不推荐)</option>
          <option value="none">不签名 (不推荐)</option>
        </select>
      </Field>
      <Field label="事件掩码" help="使用通配符匹配事件类型，* 表示接受全部事件。">
        <input
          className="input mono"
          placeholder="*"
          value={form.event_mask}
          onChange={(e) => setForm({ ...form, event_mask: e.target.value })}
        />
      </Field>
      <Field label="最大 Body 字节">
        <input
          type="number"
          className="input"
          min={1024}
          value={form.max_body_bytes}
          onChange={(e) => setForm({ ...form, max_body_bytes: Number(e.target.value) || 1048576 })}
        />
      </Field>
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button tone="neutral" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button tone="primary" onClick={() => void submit()} disabled={busy}>
          {busy ? "注册中…" : "创建注册"}
        </Button>
      </div>
    </ModalShell>
  );
}

function SecretResultModal({
  name,
  secret,
  onClose,
}: {
  name: string;
  secret: string;
  onClose: () => void;
}) {
  const [saved, setSaved] = useState(false);

  return (
    <ModalShell title={`Webhook 共享密钥 — ${name}`} onClose={() => saved && onClose()} hideCloseButton={!saved}>
      <Notice tone="warn">
        <strong>SHARED HMAC SECRET — 仅展示一次</strong>
        <p className="mt-1 mb-0 text-xs leading-relaxed">
          该密钥不会被再次显示。请立即复制并存入您的密钥管理系统，
          然后将其填入 Mox 配置中对应的 Webhook 段。
        </p>
      </Notice>
      <div className="grid gap-2 rounded-lg border bg-[var(--surface-strong)] p-3">
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs muted">HMAC 共享密钥</span>
          <CopyButton value={secret} label="复制密钥" />
        </div>
        <code className="mono break-all text-sm">{secret}</code>
      </div>
      <CheckLabel checked={saved} onChange={setSaved}>
        我已将该共享密钥保存到 Mox 配置中
      </CheckLabel>
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button tone="primary" disabled={!saved} onClick={onClose}>
          关闭
        </Button>
      </div>
    </ModalShell>
  );
}

// ============================================================================
// 5. Outbound rate & reputation sub-tab
// ============================================================================

function OutboundPanel({
  actions,
  confirmDanger,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  confirmDanger: ReturnType<typeof useDangerConfirm>["confirmDanger"];
}) {
  const [scope, setScope] = useState("global");
  const [snap, setSnap] = useState<OutboundRateSnapshot | null>(null);
  const [thresholds, setThresholds] = useState<MailOutboundThreshold[]>([]);
  const [thresholdsLoading, setThresholdsLoading] = useState(true);
  const [dnsbl, setDnsbl] = useState<DNSBLProbeResp | null>(null);
  const [dnsblLoading, setDnsblLoading] = useState(false);

  // Threshold patch form
  const [patch, setPatch] = useState<Partial<MailOutboundThreshold>>({
    send_1m_warn: 600,
    send_1m_crit: 1200,
    send_1h_warn: 18000,
    send_1h_crit: 36000,
    bounce_rate_pct_warn: 3,
    bounce_rate_pct_crit: 8,
  });
  const [saving, setSaving] = useState(false);

  async function loadRate() {
    try {
      const r = await mailOutboundRate(scope);
      setSnap(r);
      // Hydrate form with existing thresholds
      if (r.thresholds) {
        setPatch({
          send_1m_warn: r.thresholds.send_1m_warn,
          send_1m_crit: r.thresholds.send_1m_crit,
          send_1h_warn: r.thresholds.send_1h_warn,
          send_1h_crit: r.thresholds.send_1h_crit,
          bounce_rate_pct_warn: r.thresholds.bounce_rate_pct_warn,
          bounce_rate_pct_crit: r.thresholds.bounce_rate_pct_crit,
        });
      }
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function loadThresholds() {
    setThresholdsLoading(true);
    try {
      const r = await mailOutboundThresholdsList();
      setThresholds(r || []);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setThresholdsLoading(false);
    }
  }

  async function saveThresholds() {
    setSaving(true);
    try {
      const r = await mailOutboundThresholdsUpdate(scope, patch, actions.csrf);
      setThresholds((old) => {
        const idx = old.findIndex((t) => t.scope === scope);
        if (idx >= 0) {
          const copy = old.slice();
          copy[idx] = r;
          return copy;
        }
        return [...old, r];
      });
      actions.setToast(`已更新 ${scope} 作用域的阈值`, "good");
      await loadRate();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setSaving(false);
    }
  }

  async function runDNSBL() {
    setDnsblLoading(true);
    try {
      const r = await mailDNSBLProbe();
      setDnsbl(r);
      const tone = r.summary.critical_count > 0 ? "danger" : r.summary.warn_count > 0 ? "warn" : "good";
      actions.setToast(
        `DNSBL 完成：${r.summary.critical_count} 严重 / ${r.summary.warn_count} 警告 / 共 ${r.summary.total_ips} IP`,
        tone,
      );
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setDnsblLoading(false);
    }
  }

  useEffect(() => {
    void loadRate();
    void loadThresholds();
    void confirmDanger; // no-op reference
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope]);

  const send1m = snap?.counts?.["1m"] ?? 0;
  const send1h = snap?.counts?.["1h"] ?? 0;
  const send24h = snap?.counts?.["24h"] ?? 0;
  const bouncePct = snap?.bounce_rate_pct ?? 0;

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="muted text-xs">作用域：</span>
          <select
            className="input w-[180px]"
            value={scope}
            onChange={(e) => setScope(e.target.value)}
          >
            <option value="global">global (全局)</option>
            {thresholds
              .filter((t) => t.scope !== "global")
              .map((t) => (
                <option key={t.scope} value={t.scope}>
                  {t.scope}
                </option>
              ))}
            <option value="__new__">+ 新建作用域…</option>
          </select>
        </div>
        <Button tone="neutral" onClick={() => void loadRate()}>
          刷新速率
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Metric label="1 分钟发送" tone="neutral" value={send1m} detail="Phantom delivery_events 聚合快照" />
        <Metric label="1 小时发送" tone="good" value={send1h} detail="Phantom delivery_events 聚合快照" />
        <Metric label="24 小时发送" tone="good" value={send24h} detail="Phantom delivery_events 聚合快照" />
        <Metric
          label="退信率 (24h)"
          tone={bouncePct >= (patch.bounce_rate_pct_crit ?? 8) ? "danger" : bouncePct >= (patch.bounce_rate_pct_warn ?? 3) ? "warn" : "good"}
          value={`${bouncePct.toFixed(2)}%`}
          detail={
            bouncePct >= (patch.bounce_rate_pct_warn ?? 3)
              ? "已达到告警线，请检查收件人质量"
              : "低于告警线"
          }
        />
      </div>

      <Notice tone="warn">
        <strong>数据来源说明。</strong> 当前出站速率来自 Phantom 持久化 delivery_events 的窗口聚合，不代表完整 Mox 实时限流或时序监控链路。
      </Notice>

      <Panel title="出站速率时间序列" subtitle="真实发送流水线接入后展示按时间桶聚合的出站速率。">
        <EmptyState title="暂无真实时间序列" body="当前只展示最新窗口快照和阈值配置；不会渲染 synthetic 曲线。" />
      </Panel>

      <CollapsibleSection
        defaultOpen
        subtitle={`作用域：${scope}。设置发送速率与退信率告警 / 熔断阈值。`}
        title="阈值配置"
      >
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="1 分钟发送 - 告警值" help="超过后记录告警日志，不熔断。">
            <input
              type="number"
              className="input"
              value={patch.send_1m_warn ?? 0}
              onChange={(e) => setPatch({ ...patch, send_1m_warn: Number(e.target.value) })}
            />
          </Field>
          <Field label="1 分钟发送 - 熔断值" help="超过后临时暂停出站直至下一个窗口。">
            <input
              type="number"
              className="input"
              value={patch.send_1m_crit ?? 0}
              onChange={(e) => setPatch({ ...patch, send_1m_crit: Number(e.target.value) })}
            />
          </Field>
          <Field label="1 小时发送 - 告警值">
            <input
              type="number"
              className="input"
              value={patch.send_1h_warn ?? 0}
              onChange={(e) => setPatch({ ...patch, send_1h_warn: Number(e.target.value) })}
            />
          </Field>
          <Field label="1 小时发送 - 熔断值">
            <input
              type="number"
              className="input"
              value={patch.send_1h_crit ?? 0}
              onChange={(e) => setPatch({ ...patch, send_1h_crit: Number(e.target.value) })}
            />
          </Field>
          <Field label="退信率 % - 告警值">
            <input
              type="number"
              className="input"
              step="0.1"
              value={patch.bounce_rate_pct_warn ?? 0}
              onChange={(e) => setPatch({ ...patch, bounce_rate_pct_warn: Number(e.target.value) })}
            />
          </Field>
          <Field label="退信率 % - 熔断值">
            <input
              type="number"
              className="input"
              step="0.1"
              value={patch.bounce_rate_pct_crit ?? 0}
              onChange={(e) => setPatch({ ...patch, bounce_rate_pct_crit: Number(e.target.value) })}
            />
          </Field>
        </div>
        {!thresholdsLoading && thresholds.length > 1 ? (
          <div className="rounded-lg border p-3 bg-[var(--surface-soft)] text-xs muted">
            所有已定义作用域：
            <ul className="mt-1 mb-0 grid gap-1 pl-4">
              {thresholds.map((t) => (
                <li key={t.scope}>
                  <code>{t.scope}</code>
                  <span className="ml-2">
                    1m {t.send_1m_warn}/{t.send_1m_crit} · 1h {t.send_1h_warn}/{t.send_1h_crit} · bounce % {t.bounce_rate_pct_warn}/{t.bounce_rate_pct_crit}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        <div className="flex justify-end">
          <Button tone="primary" disabled={saving} onClick={() => void saveThresholds()}>
            {saving ? "保存中…" : "保存阈值"}
          </Button>
        </div>
      </CollapsibleSection>

      <CollapsibleSection
        defaultOpen
        subtitle="对出站 IP 执行公共 DNSBL 查询，检测是否被反垃圾邮件联盟列入。"
        title="DNSBL 信誉探测"
      >
        <div className="flex items-center justify-between gap-2 flex-wrap">
          <div className="muted text-xs">
            {dnsbl ? (
              <span>
                上次执行：<code>{formatDateTime(dnsbl.last_run_at)}</code>
              </span>
            ) : (
              <span>尚未执行过探测。</span>
            )}
          </div>
          <Button tone="primary" onClick={() => void runDNSBL()} disabled={dnsblLoading}>
            {dnsblLoading ? "探测中…" : "运行 DNSBL 探测"}
          </Button>
        </div>
        {dnsbl ? (
          <div className="grid gap-3">
            <div className="grid grid-cols-4 gap-3">
              <Metric label="扫描 IP" tone="neutral" value={dnsbl.summary.total_ips} detail="查询到的出站地址" />
              <Metric label="命中列表" tone="warn" value={dnsbl.summary.listed_count} detail="至少被 1 个 DNSBL 标记" />
              <Metric label="警告级" tone="warn" value={dnsbl.summary.warn_count} detail="建议排查的 DNSBL" />
              <Metric label="严重级" tone="danger" value={dnsbl.summary.critical_count} detail="会立刻影响投递的 DNSBL" />
            </div>
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)]">
                    <th className="muted px-2 py-2 text-xs font-medium">IP</th>
                    <th className="muted px-2 py-2 text-xs font-medium">DNSBL 源</th>
                    <th className="muted px-2 py-2 text-xs font-medium">列入</th>
                    <th className="muted px-2 py-2 text-xs font-medium">代码</th>
                    <th className="muted px-2 py-2 text-xs font-medium">严重程度</th>
                  </tr>
                </thead>
                <tbody>
                  {dnsbl.results.length === 0 ? (
                    <tr>
                      <td className="px-2 py-3 text-center muted text-xs" colSpan={5}>
                        暂无结果
                      </td>
                    </tr>
                  ) : (
                    dnsbl.results.map((r: DNSBLResult, idx: number) => (
                      <tr className="border-b border-[var(--line)] last:border-b-0" key={`${r.ip}-${r.source}-${idx}`}>
                        <td className="px-2 py-2 align-top text-xs">
                          <code>{r.ip}</code>
                        </td>
                        <td className="px-2 py-2 align-top text-xs">{r.source}</td>
                        <td className="px-2 py-2 align-top">
                          <Pill tone={r.listed ? "danger" : "good"}>{r.listed ? "命中" : "未命中"}</Pill>
                        </td>
                        <td className="px-2 py-2 align-top text-xs">
                          {r.code ? <code>{r.code}</code> : "-"}
                        </td>
                        <td className="px-2 py-2 align-top">
                          <Pill
                            tone={r.severity === "critical" ? "danger" : r.severity === "warn" ? "warn" : "good"}
                          >
                            {r.severity === "critical" ? "严重" : r.severity === "warn" ? "警告" : "良好"}
                          </Pill>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        ) : null}
      </CollapsibleSection>
    </>
  );
}

// ============================================================================
// Shared modal / drawer shells
// ============================================================================

function ModalShell({
  title,
  children,
  onClose,
  hideCloseButton = false,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
  hideCloseButton?: boolean;
}) {
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.56)] p-4"
      onClick={onClose}
    >
      <div
        aria-modal="true"
        className="w-full max-w-2xl max-h-[90dvh] overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] flex flex-col"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">{title}</h2>
          {!hideCloseButton ? (
            <button
              type="button"
              onClick={onClose}
              className="text-lg muted hover:text-neutral-12"
              aria-label="关闭"
            >关闭</button>
          ) : null}
        </div>
        <div className="grid gap-4 p-4 overflow-y-auto text-sm">{children}</div>
      </div>
    </div>
  );
}

function DrawerModal({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.56)] p-4"
      onClick={onClose}
    >
      <div
        aria-modal="true"
        className="w-full max-w-3xl max-h-[92dvh] overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] flex flex-col"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-lg muted hover:text-neutral-12"
            aria-label="关闭"
          >关闭</button>
        </div>
        <div className="grid gap-4 p-4 overflow-y-auto text-sm">{children}</div>
      </div>
    </div>
  );
}

function CopyButton({ value, label }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard?.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore
    }
  }
  return (
    <button
      type="button"
      title={label || "复制"}
      className="inline-flex items-center text-xs muted hover:text-neutral-12"
      onClick={() => void copy()}
    >
      {copied ? "完成" : "复制"}
    </button>
  );
}

// ============================================================================
// Helpers
// ============================================================================

function directionLabel(d: DeliveryDirection): string {
  return d === "out" ? "出站" : d === "in" ? "入站" : "本地";
}

function directionTone(d: DeliveryDirection): Tone {
  return d === "out" ? "good" : d === "in" ? "neutral" : "warn";
}

function deliveryStatusTone(s: DeliveryStatus): Tone {
  switch (s) {
    case "sent":
      return "good";
    case "deferred":
      return "warn";
    case "bounced":
    case "suppressed":
      return "danger";
    case "dropped":
      return "neutral";
    case "pending":
    case "queued":
    default:
      return "neutral";
  }
}

function deliveryStatusLabel(s: DeliveryStatus): string {
  switch (s) {
    case "sent":
      return "已发送";
    case "deferred":
      return "延迟重试";
    case "bounced":
      return "退信";
    case "suppressed":
      return "被抑制";
    case "dropped":
      return "已丢弃";
    case "pending":
      return "待处理";
    case "queued":
      return "已排队";
    default:
      return s;
  }
}

function actionLabelFor(a: QueueAction): string {
  switch (a) {
    case "hold":
      return "挂起";
    case "unhold":
      return "解除挂起";
    case "schedule":
      return "调度";
    case "fail":
      return "标记失败";
    case "drop":
      return "丢弃";
  }
}

function suppressionReasonTone(reason?: string): Tone {
  switch (reason) {
    case "bounce":
      return "danger";
    case "complaint":
      return "warn";
    case "unsubscribe":
      return "neutral";
    case "manual":
      return "good";
    default:
      return "neutral";
  }
}

function webhookEventStatusTone(status: string): Tone {
  const s = status.toLowerCase();
  if (s.includes("ok") || s.includes("success") || s.includes("delivered")) return "good";
  if (s.includes("fail") || s.includes("error") || s.includes("drop")) return "danger";
  if (s.includes("retry") || s.includes("deferred")) return "warn";
  return "neutral";
}

function relativeTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diff = Date.now() - d.getTime();
  const abs = Math.abs(diff);
  const min = 60 * 1000;
  const hour = 60 * min;
  const day = 24 * hour;
  const suffix = diff >= 0 ? "前" : "后";
  if (abs < min) return `${Math.max(1, Math.floor(abs / 1000))} 秒${suffix}`;
  if (abs < hour) return `${Math.floor(abs / min)} 分钟${suffix}`;
  if (abs < day) return `${Math.floor(abs / hour)} 小时${suffix}`;
  if (abs < 30 * day) return `${Math.floor(abs / day)} 天${suffix}`;
  return formatDateShort(iso);
}

function formatDateShort(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function formatDateTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
}

function truncateMiddle(s: string, max: number): string {
  if (!s) return "";
  if (s.length <= max) return s;
  const head = Math.floor((max - 1) / 2);
  const tail = max - 1 - head;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
}

function humanSize(bytes: number): string {
  if (!bytes || bytes < 1024) return `${bytes || 0} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function withinDays(iso: string, days: number): boolean {
  const d = new Date(iso).getTime();
  if (Number.isNaN(d)) return false;
  return Math.abs(Date.now() - d) <= days * 86400_000;
}

function toLocalInput(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(v: string): string {
  // Append local timezone offset as ISO
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toISOString();
}

function computeDeliveryAggs(items: MailDeliveryEvent[]) {
  const now = Date.now();
  const dayMs = 86400_000;
  let sent24h = 0;
  let bounced24h = 0;
  let deferred = 0;
  let pending = 0;
  for (const d of items) {
    const t = new Date(d.created_at).getTime();
    const within24h = now - t <= dayMs;
    if (d.status === "sent" && within24h) sent24h++;
    if (d.status === "bounced" && within24h) bounced24h++;
    if (d.status === "deferred") deferred++;
    if (d.status === "pending" || d.status === "queued") pending++;
  }
  return { sent24h, bounced24h, deferred, pending };
}
