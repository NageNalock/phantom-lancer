import { useEffect, useMemo, useState } from "react";
import { ArrowClockwise, GearSix, Newspaper, PlayCircle, Trash, X } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2NewsEvent,
  StockV2NewsLinkCandidate,
  StockV2NewsPipelineRunResult,
  StockV2NewsSourceOverview,
  StockV2PagedResponse,
  StockV2RawNews,
  StockV2RawNewsTruncateResult,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Field, Notice, Panel, Pill } from "../../components/ui";
import { hasMeaningfulTime } from "./time";

type NewsAssetKind = "raw" | "events" | "candidates";
type DetailItem = StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate | null;

const PAGE_SIZE = 20;
const ASSET_TABS: Array<{ id: NewsAssetKind; label: string }> = [
  { id: "raw", label: "RawNews" },
  { id: "events", label: "NewsEvent" },
  { id: "candidates", label: "Candidate" },
];

export function StockV2NewsWorkbench({ actions }: { actions: AppActions }) {
  const [sources, setSources] = useState<StockV2NewsSourceOverview[]>([]);
  const [assetKind, setAssetKind] = useState<NewsAssetKind>("raw");
  const [sourceFilter, setSourceFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<Array<StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate>>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [sourceLoading, setSourceLoading] = useState(false);
  const [detail, setDetail] = useState<DetailItem>(null);
  const [configSource, setConfigSource] = useState<StockV2NewsSourceOverview | null>(null);
  const [truncateOpen, setTruncateOpen] = useState(false);
  const [lastRun, setLastRun] = useState<StockV2NewsPipelineRunResult | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const sourceOptions = useMemo(() => sources.map((item) => item.state.source), [sources]);

  async function loadSources() {
    setSourceLoading(true);
    try {
      const res = await actions.api<{ items?: StockV2NewsSourceOverview[] }>("/api/stockv2/news/sources");
      setSources(res.items ?? []);
    } catch (error) {
      actions.setToast(`加载消息源失败：${friendlyError(error)}`, "danger");
    } finally {
      setSourceLoading(false);
    }
  }

  async function loadAssets(nextPage = page) {
    setLoading(true);
    try {
      const safePage = Math.max(1, nextPage);
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((safePage - 1) * PAGE_SIZE),
      });
      if (sourceFilter) params.set("source", sourceFilter);
      if (query.trim()) params.set("q", query.trim());
      if (statusFilter) {
        const key = assetKind === "raw" ? "status" : assetKind === "events" ? "linkStatus" : "monitorStatus";
        params.set(key, statusFilter);
      }
      const path =
        assetKind === "raw"
          ? "/api/stockv2/news/raw"
          : assetKind === "events"
            ? "/api/stockv2/news/events"
            : "/api/stockv2/news/link-candidates";
      const res = await actions.api<StockV2PagedResponse<StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate>>(
        `${path}?${params.toString()}`,
      );
      setItems(res.items ?? []);
      setTotal(res.total ?? 0);
      setPage(safePage);
    } catch (error) {
      actions.setToast(`加载消息面数据失败：${friendlyError(error)}`, "danger");
    } finally {
      setLoading(false);
    }
  }

  async function openDetail(item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
    setDetail(item);
    if (assetKind !== "raw") return;
    try {
      const full = await actions.api<StockV2RawNews>(`/api/stockv2/news/raw/${encodeURIComponent(item.id)}`);
      setDetail(full);
    } catch (error) {
      actions.setToast(`加载 Raw payload 失败：${friendlyError(error)}`, "danger");
    }
  }

  useEffect(() => {
    void loadSources();
    void loadAssets(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadAssets(1);
    }, query.trim() ? 250 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetKind, sourceFilter, statusFilter, query]);

  async function runSource(source: string) {
    try {
      const result = await actions.api<StockV2NewsPipelineRunResult>(`/api/stockv2/news/sources/${source}/run-once`, {
        method: "POST",
        csrf: actions.csrf,
      });
      setLastRun(result);
      actions.setToast(`消息源 ${source} 已执行：${sourceStatusLabel(result.status)}`, "good");
      await loadSources();
      await loadAssets(1);
    } catch (error) {
      actions.setToast(`执行失败：${friendlyError(error)}`, "danger");
    }
  }

  return (
    <div className="grid gap-4">
      <Panel
        title="消息面源"
        subtitle="按 source 独立维护抓取、归一化、关联和调度状态"
        actions={
          <Button onClick={() => void loadSources()} disabled={sourceLoading}>
            <ArrowClockwise size={14} className="mr-1.5" />
            {sourceLoading ? "刷新中" : "刷新源"}
          </Button>
        }
      >
        <div className="grid gap-3 md:grid-cols-2">
          {sources.map((item) => (
            <SourceCard
              item={item}
              key={item.state.source}
              onConfig={() => setConfigSource(item)}
              onRun={() => void runSource(item.state.source)}
            />
          ))}
          {sources.length === 0 ? (
            <div className="rounded-lg border border-dashed border-[var(--line)] p-4 text-sm text-[var(--muted)]">
              暂无消息源状态。
            </div>
          ) : null}
        </div>
        {lastRun ? (
          <Notice tone={lastRun.status === "failed" ? "danger" : "warn"}>
            <span className="text-xs">
              最近执行：{lastRun.source} · {sourceStatusLabel(lastRun.status)} · 抓取 {lastRun.fetchedCount} 条，
              新增 Raw {lastRun.rawInsertedCount}，归一化 {lastRun.normalizedCount}，候选 {lastRun.linkCandidateCount}
              {lastRun.errorMessage ? ` · ${lastRun.errorMessage}` : ""}
            </span>
          </Notice>
        ) : null}
      </Panel>

      <Panel
        title="消息面数据资产"
        subtitle="RawNews -> NewsEvent -> NewsLinkCandidate 的可检查列表"
        actions={
          <>
            {assetKind === "raw" ? (
              <Button onClick={() => setTruncateOpen(true)} tone="danger">
                <Trash size={14} className="mr-1.5" />
                截断 RawNews
              </Button>
            ) : null}
            <Button onClick={() => void loadAssets(page)} disabled={loading}>
              <ArrowClockwise size={14} className="mr-1.5" />
              {loading ? "加载中" : "刷新列表"}
            </Button>
          </>
        }
      >
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <div className="flex rounded-md border border-[var(--line)] p-0.5">
            {ASSET_TABS.map((tab) => (
              <button
                className={`rounded px-3 py-1.5 text-xs ${assetKind === tab.id ? "bg-[var(--surface-strong)] text-[var(--text)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`}
                key={tab.id}
                onClick={() => {
                  setAssetKind(tab.id);
                  setStatusFilter("");
                  setItems([]);
                  setTotal(0);
                  setPage(1);
                }}
                type="button"
              >
                {tab.label}
              </button>
            ))}
          </div>
          <select className="select h-9 w-40 text-xs" onChange={(event) => setSourceFilter(event.target.value)} value={sourceFilter}>
            <option value="">全部 source</option>
            {sourceOptions.map((source) => (
              <option key={source} value={source}>{source}</option>
            ))}
          </select>
          <select className="select h-9 w-40 text-xs" onChange={(event) => setStatusFilter(event.target.value)} value={statusFilter}>
            <option value="">全部状态</option>
            {statusOptions(assetKind).map((status) => (
              <option key={status} value={status}>{status}</option>
            ))}
          </select>
          <input
            className="input h-9 min-w-[260px] flex-1 text-xs"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索标题、摘要、symbol 或 reason"
            type="search"
            value={query}
          />
        </div>

        <NewsAssetTable assetKind={assetKind} items={items} loading={loading} onOpen={(item) => void openDetail(item)} />
        <Pager page={page} total={total} totalPages={totalPages} onPage={(next) => void loadAssets(next)} />
      </Panel>

      {detail ? <NewsDetailDrawer item={detail} onClose={() => setDetail(null)} /> : null}
      {truncateOpen ? (
        <RawNewsTruncateDrawer
          actions={actions}
          onClose={() => setTruncateOpen(false)}
          onDone={async () => {
            setTruncateOpen(false);
            await loadSources();
            await loadAssets(1);
          }}
        />
      ) : null}
      {configSource ? (
        <NewsSourceConfigDrawer
          actions={actions}
          item={configSource}
          onClose={() => setConfigSource(null)}
          onSaved={async () => {
            setConfigSource(null);
            await loadSources();
          }}
        />
      ) : null}
    </div>
  );
}

function RawNewsTruncateDrawer({
  actions,
  onClose,
  onDone,
}: {
  actions: AppActions;
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const [before, setBefore] = useState("");
  const [confirm, setConfirm] = useState("");
  const [saving, setSaving] = useState(false);
  const canSubmit = before.trim() !== "" && confirm.trim() === "DELETE" && !saving;

  async function submit() {
    const beforeISO = datetimeLocalToISOString(before);
    if (!beforeISO) {
      actions.setToast("请选择有效的截止时间", "danger");
      return;
    }
    setSaving(true);
    try {
      const result = await actions.api<StockV2RawNewsTruncateResult>("/api/stockv2/news/raw/truncate", {
        method: "POST",
        body: { before: beforeISO },
        csrf: actions.csrf,
      });
      actions.setToast(`已删除 ${result.deletedCount} 条 RawNews`, "good");
      await onDone();
    } catch (error) {
      actions.setToast(`截断 RawNews 失败：${friendlyError(error)}`, "danger");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50" role="presentation" onClick={saving ? undefined : onClose}>
      <div className="absolute inset-0 bg-[rgba(16,18,22,0.56)]" />
      <aside className="absolute right-0 top-0 flex h-full w-[min(520px,100vw)] flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby="raw-news-truncate-title">
        <header className="flex items-start gap-3 border-b border-[var(--line)] p-4">
          <Trash size={18} className="mt-0.5 text-[var(--danger)]" />
          <div className="min-w-0 flex-1">
            <h3 className="m-0 text-base font-semibold" id="raw-news-truncate-title">截断 RawNews</h3>
            <p className="muted mt-1 mb-0 text-xs">删除有效时间早于截止点的 RawNews；NewsEvent 和 Candidate 会保留。</p>
          </div>
          <Button aria-label="关闭" className="px-2 py-1 text-xs" disabled={saving} onClick={onClose}><X size={16} /></Button>
        </header>
        <div className="flex-1 overflow-y-auto p-4">
          <div className="grid gap-4">
            <Notice tone="warn">
              <span className="text-xs">这是不可恢复的批量删除。有效时间为 published_at，缺失时使用 fetched_at。</span>
            </Notice>
            <Field label="删除此时间之前的 RawNews">
              <input
                className="input"
                max={datetimeLocalValue(new Date())}
                onChange={(event) => setBefore(event.target.value)}
                type="datetime-local"
                value={before}
              />
            </Field>
            <Field label="输入 DELETE 确认">
              <input
                className="input font-mono"
                onChange={(event) => setConfirm(event.target.value)}
                placeholder="DELETE"
                value={confirm}
              />
            </Field>
          </div>
        </div>
        <footer className="flex justify-end gap-2 border-t border-[var(--line)] p-4">
          <Button disabled={saving} onClick={onClose}>取消</Button>
          <Button disabled={!canSubmit} onClick={() => void submit()} tone="danger">{saving ? "删除中" : "确认删除"}</Button>
        </footer>
      </aside>
    </div>
  );
}

function SourceCard({ item, onConfig, onRun }: { item: StockV2NewsSourceOverview; onConfig: () => void; onRun: () => void }) {
  const state = item.state;
  const enabledTone = state.enabled && item.configured ? "good" : state.enabled ? "warn" : "neutral";
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <strong className="font-mono text-sm">{state.source}</strong>
            <Pill tone={enabledTone}>{state.enabled ? (item.configured ? "启用" : "待配置") : "关闭"}</Pill>
            <Pill tone={sourceStatusTone(state.status)}>{sourceStatusLabel(state.status)}</Pill>
          </div>
          {item.reason ? <p className="muted mt-1 mb-0 text-xs">{item.reason}</p> : null}
        </div>
        <div className="flex shrink-0 gap-2">
          <Button className="px-2 py-1 text-xs" onClick={onConfig} title="配置 source">
            <GearSix size={14} />
          </Button>
          <Button className="px-2 py-1 text-xs" disabled={!state.enabled || !item.configured || state.status === "running"} onClick={onRun} title="执行一次">
            <PlayCircle size={14} />
          </Button>
        </div>
      </div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <Metric label="Raw" value={state.rawNewsCount} />
        <Metric label="Event" value={state.newsEventCount} />
        <Metric label="Candidate" value={state.linkCandidateCount} />
      </div>
      <div className="mt-3 grid gap-1 text-[11px] text-[var(--muted-strong)]">
        <div>下次：{formatTime(state.nextRunAt)}</div>
        <div>上次：{formatTime(state.lastRunAt || state.lastFetchAt)}</div>
        {state.backoffUntil ? <div className="text-[var(--warn)]">退避至：{formatTime(state.backoffUntil)}</div> : null}
        {state.lastRunError || state.lastError ? <div className="truncate text-[var(--danger)]">{state.lastRunError || state.lastError}</div> : null}
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5">
      <div className="text-[var(--muted)]">{label}</div>
      <div className="font-mono text-sm text-[var(--text)]">{value ?? 0}</div>
    </div>
  );
}

function NewsAssetTable({
  assetKind,
  items,
  loading,
  onOpen,
}: {
  assetKind: NewsAssetKind;
  items: Array<StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate>;
  loading: boolean;
  onOpen: (item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) => void;
}) {
  if (loading && items.length === 0) {
    return <div className="rounded-lg border border-dashed border-[var(--line)] p-6 text-center text-sm text-[var(--muted)]">加载中...</div>;
  }
  if (items.length === 0) {
    return <div className="rounded-lg border border-dashed border-[var(--line)] p-6 text-center text-sm text-[var(--muted)]">暂无消息面数据。</div>;
  }
  return (
    <div className="overflow-x-auto rounded-lg border border-[var(--line)]">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-[var(--line)] bg-[var(--surface-soft)] text-left text-xs text-[var(--muted)]">
            <th className="px-3 py-2 font-medium">时间</th>
            <th className="px-3 py-2 font-medium">Source</th>
            <th className="px-3 py-2 font-medium">标题 / 对象</th>
            <th className="px-3 py-2 font-medium">状态</th>
            <th className="px-3 py-2 text-right font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr className="border-b border-[var(--line-soft)] last:border-b-0 hover:bg-[var(--surface-soft)]" key={item.id}>
              <td className="px-3 py-2 text-xs text-[var(--muted)]">{formatTime(rowTime(assetKind, item))}</td>
              <td className="px-3 py-2"><Pill tone="neutral">{rowSource(assetKind, item)}</Pill></td>
              <td className="max-w-[620px] px-3 py-2">
                <div className="truncate font-medium">{rowTitle(assetKind, item)}</div>
                <div className="truncate text-xs text-[var(--muted)]">{rowSubtitle(assetKind, item)}</div>
              </td>
              <td className="px-3 py-2"><Pill tone="neutral">{rowStatus(assetKind, item)}</Pill></td>
              <td className="px-3 py-2 text-right">
                <Button className="px-2 py-1 text-xs" onClick={() => onOpen(item)}>详情</Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Pager({ page, total, totalPages, onPage }: { page: number; total: number; totalPages: number; onPage: (page: number) => void }) {
  if (total <= PAGE_SIZE) return null;
  const pages = paginationWindow(page, totalPages);
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-3 text-xs">
      <span className="text-[var(--muted)]">共 {total} 条 · 第 {page} / {totalPages} 页</span>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button disabled={page <= 1} onClick={() => onPage(page - 1)}>上一页</Button>
        {pages.map((item, index) =>
          item === "..." ? (
            <span className="px-2 text-[var(--muted)]" key={`ellipsis-${index}`}>...</span>
          ) : (
            <Button className={item === page ? "border-[var(--accent)] text-[var(--accent)]" : ""} key={item} onClick={() => onPage(item)}>
              {item}
            </Button>
          ),
        )}
        <Button disabled={page >= totalPages} onClick={() => onPage(page + 1)}>下一页</Button>
        <select aria-label="选择消息面页码" className="select h-9 w-24 text-xs" onChange={(event) => onPage(Number(event.target.value))} value={page}>
          {Array.from({ length: totalPages }, (_, idx) => idx + 1).map((item) => (
            <option key={item} value={item}>第 {item} 页</option>
          ))}
        </select>
      </div>
    </div>
  );
}

function NewsSourceConfigDrawer({
  actions,
  item,
  onClose,
  onSaved,
}: {
  actions: AppActions;
  item: StockV2NewsSourceOverview;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const state = item.state;
  const [form, setForm] = useState({
    enabled: state.enabled,
    pollIntervalSeconds: state.pollIntervalSeconds || 600,
    jitterSeconds: state.jitterSeconds || 60,
    batchLimit: state.batchLimit || 50,
    processLimit: state.processLimit || 50,
    backoffBaseSeconds: state.backoffBaseSeconds || 30,
    backoffMaxSeconds: state.backoffMaxSeconds || 900,
  });
  const [credentialInput, setCredentialInput] = useState("");
  const [clearCredential, setClearCredential] = useState(false);
  const [saving, setSaving] = useState(false);

  async function save() {
    setSaving(true);
    try {
      const body: Record<string, unknown> = { ...form };
      if (credentialInput.trim()) body.credentialInput = credentialInput;
      if (clearCredential) body.clearCredential = true;
      await actions.api(`/api/stockv2/news/sources/${state.source}/config`, {
        method: "PUT",
        body,
        csrf: actions.csrf,
      });
      actions.setToast(`已保存 ${state.source} 配置`, "good");
      await onSaved();
    } catch (error) {
      actions.setToast(`保存失败：${friendlyError(error)}`, "danger");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50" role="presentation" onClick={onClose}>
      <div className="absolute inset-0 bg-[rgba(16,18,22,0.56)]" />
      <aside className="absolute right-0 top-0 flex h-full w-[min(520px,100vw)] flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
        <header className="flex items-start gap-3 border-b border-[var(--line)] p-4">
          <div className="min-w-0 flex-1">
            <h3 className="m-0 text-base font-semibold">配置消息源</h3>
            <p className="muted mt-1 mb-0 text-xs"><span className="font-mono">{state.source}</span> · 启用状态、后台调度和必要凭据集中在这里维护。</p>
          </div>
          <Button aria-label="关闭" className="px-2 py-1 text-xs" onClick={onClose}><X size={16} /></Button>
        </header>
        <div className="flex-1 overflow-y-auto p-4">
          <div className="grid gap-4">
            {!item.configured ? <Notice tone="warn"><span className="text-xs">{item.reason || "该 source 还没有完成本机配置。"}</span></Notice> : null}
            <label className="flex items-center justify-between rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
              <span>
                <span className="block">启用消息源</span>
                <span className="muted mt-0.5 block text-xs">开启后可手动执行，并按下方周期进入后台调度。</span>
              </span>
              <input checked={form.enabled} onChange={(event) => setForm((prev) => ({ ...prev, enabled: event.target.checked }))} type="checkbox" />
            </label>
            {isCredentialSource(state.source) ? (
              <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <strong className="text-sm">{sourceCredentialTitle(state.source)}</strong>
                    <p className="muted mt-1 mb-0 text-xs">{sourceCredentialDescription(state.source)}</p>
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    <Pill tone={item.credentialSet ? "good" : "neutral"}>{item.credentialSet ? "凭据已保存" : "凭据未配置"}</Pill>
                  </div>
                </div>
                {clearCredential ? <Notice tone="warn"><span className="text-xs">保存后会清除当前保存的凭据。</span></Notice> : null}
                <Field label={sourceCredentialFieldLabel(state.source)} help={sourceCredentialHelp(state.source)}>
                  <textarea
                    className="input min-h-28"
                    onChange={(event) => setCredentialInput(event.target.value)}
                    placeholder={sourceCredentialPlaceholder(state.source)}
                    rows={5}
                    value={credentialInput}
                  />
                </Field>
                <div className="flex justify-end">
                  <Button disabled={!item.credentialSet} onClick={() => setClearCredential(true)}>
                    保存时清除凭据
                  </Button>
                </div>
              </div>
            ) : null}
            <div className="grid grid-cols-2 gap-3">
              <NumberField label="抓取周期 (秒)" min={60} value={form.pollIntervalSeconds} onChange={(value) => setForm((prev) => ({ ...prev, pollIntervalSeconds: value }))} />
              <NumberField label="随机抖动 (秒)" min={0} value={form.jitterSeconds} onChange={(value) => setForm((prev) => ({ ...prev, jitterSeconds: value }))} />
              <NumberField label="抓取批量" min={1} max={200} value={form.batchLimit} onChange={(value) => setForm((prev) => ({ ...prev, batchLimit: value }))} />
              <NumberField label="处理批量" min={1} max={200} value={form.processLimit} onChange={(value) => setForm((prev) => ({ ...prev, processLimit: value }))} />
              <NumberField label="退避基准 (秒)" min={30} value={form.backoffBaseSeconds} onChange={(value) => setForm((prev) => ({ ...prev, backoffBaseSeconds: value }))} />
              <NumberField label="最大退避 (秒)" min={30} value={form.backoffMaxSeconds} onChange={(value) => setForm((prev) => ({ ...prev, backoffMaxSeconds: value }))} />
            </div>
            <Notice tone="warn">
              <span className="text-xs">启用后会按 next_run_at 到期执行；手动“执行一次”不改变配置，但会更新状态和下一次运行时间。</span>
            </Notice>
          </div>
        </div>
        <footer className="flex justify-end gap-2 border-t border-[var(--line)] p-4">
          <Button onClick={onClose}>取消</Button>
          <Button disabled={saving} onClick={() => void save()} tone="primary">{saving ? "保存中" : "保存"}</Button>
        </footer>
      </aside>
    </div>
  );
}

function NumberField({ label, max, min, onChange, value }: { label: string; max?: number; min?: number; onChange: (value: number) => void; value: number }) {
  return (
    <Field label={label}>
      <input className="input" max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} type="number" value={value} />
    </Field>
  );
}

function isCredentialSource(source: string): boolean {
  return source === "financialjuice";
}

function sourceCredentialTitle(source: string): string {
  return source === "financialjuice" ? "FinancialJuice 凭据" : "凭据";
}

function sourceCredentialDescription(source: string): string {
  return source === "financialjuice" ? "保存浏览器复制的 Startup 请求片段、info URL 或 Cookie header。" : "";
}

function sourceCredentialFieldLabel(source: string): string {
  return source === "financialjuice" ? "FinancialJuice 请求片段" : "请求片段";
}

function sourceCredentialHelp(source: string): string {
  return source === "financialjuice" ? "支持 Startup curl、含 info 的请求 URL 或 Cookie header。敏感值保存后不会回显。" : "";
}

function sourceCredentialPlaceholder(source: string): string {
  return source === "financialjuice" ? "curl 'https://live.financialjuice.com/FJService.asmx/Startup?info=...' 或 Cookie: ..." : "";
}

function NewsDetailDrawer({ item, onClose }: { item: DetailItem; onClose: () => void }) {
  if (!item) return null;
  const payload = "rawPayload" in item ? item.rawPayload : undefined;
  return (
    <div className="fixed inset-0 z-50" role="presentation" onClick={onClose}>
      <div className="absolute inset-0 bg-[rgba(16,18,22,0.56)]" />
      <aside className="absolute right-0 top-0 flex h-full w-[min(680px,100vw)] flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
        <header className="flex items-start gap-3 border-b border-[var(--line)] p-4">
          <Newspaper size={18} className="mt-0.5 text-[var(--accent)]" />
          <div className="min-w-0 flex-1">
            <h3 className="m-0 truncate text-base font-semibold">{detailTitle(item)}</h3>
            <p className="muted mt-1 mb-0 text-xs font-mono">{item.id}</p>
          </div>
          <Button aria-label="关闭" className="px-2 py-1 text-xs" onClick={onClose}><X size={16} /></Button>
        </header>
        <div className="flex-1 overflow-y-auto p-4">
          <pre className="max-h-[42vh] overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed text-[var(--muted-strong)]">
            {safeJSONString(item)}
          </pre>
          {payload ? (
            <details className="mt-4 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <summary className="cursor-pointer text-sm font-medium">Raw payload（已裁剪脱敏）</summary>
              <pre className="mt-3 max-h-[36vh] overflow-auto rounded border border-[var(--line)] bg-[var(--surface)] p-3 text-xs text-[var(--muted-strong)]">
                {safeJSONString(payload)}
              </pre>
            </details>
          ) : null}
        </div>
      </aside>
    </div>
  );
}

function statusOptions(kind: NewsAssetKind) {
  if (kind === "raw") return ["new", "processed", "failed", "ignored"];
  if (kind === "events") return ["pending", "linked", "no_candidate", "failed"];
  return ["pending", "hit", "skipped", "failed"];
}

function rowSource(kind: NewsAssetKind, item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
  if (kind === "candidates") return (item as StockV2NewsLinkCandidate).newsEventSource || "-";
  return (item as StockV2RawNews | StockV2NewsEvent).source || "-";
}

function rowTime(kind: NewsAssetKind, item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
  if (kind === "raw") {
    const raw = item as StockV2RawNews;
    return boundedDisplayTime(raw.publishedAt, raw.fetchedAt);
  }
  if (kind === "events") {
    const event = item as StockV2NewsEvent;
    return boundedDisplayTime(event.eventAt, event.createdAt);
  }
  const candidate = item as StockV2NewsLinkCandidate;
  return boundedDisplayTime(candidate.newsEventAt, candidate.createdAt);
}

function rowTitle(kind: NewsAssetKind, item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
  if (kind === "raw") return (item as StockV2RawNews).title;
  if (kind === "events") return (item as StockV2NewsEvent).title;
  const candidate = item as StockV2NewsLinkCandidate;
  return `${candidate.symbol || "-"} ${candidate.instrumentName || ""}`.trim();
}

function rowSubtitle(kind: NewsAssetKind, item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
  if (kind === "raw") return (item as StockV2RawNews).snippet || (item as StockV2RawNews).sourceId || "";
  if (kind === "events") return (item as StockV2NewsEvent).summary || (item as StockV2NewsEvent).rawNewsId || "";
  const candidate = item as StockV2NewsLinkCandidate;
  const score = typeof candidate.score === "number" ? candidate.score.toFixed(1) : "-";
  return `${candidate.matchMethod || "-"} · ${score} · ${candidate.newsEventTitle || candidate.reason || ""}`;
}

function rowStatus(kind: NewsAssetKind, item: StockV2RawNews | StockV2NewsEvent | StockV2NewsLinkCandidate) {
  if (kind === "raw") return (item as StockV2RawNews).status;
  if (kind === "events") return (item as StockV2NewsEvent).linkStatus;
  return (item as StockV2NewsLinkCandidate).monitorStatus || "pending";
}

function detailTitle(item: DetailItem) {
  if (!item) return "";
  if ("title" in item) return item.title;
  return `${item.symbol} ${item.instrumentName || ""}`.trim();
}

function sourceStatusLabel(status?: string) {
  const labels: Record<string, string> = {
    idle: "空闲",
    running: "运行中",
    backoff: "退避中",
    failed: "失败",
    disabled: "已禁用",
    rate_limited: "限流",
  };
  return labels[status || ""] || status || "-";
}

function sourceStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  if (status === "idle") return "good";
  if (status === "running" || status === "backoff" || status === "rate_limited") return "warn";
  if (status === "failed") return "danger";
  return "neutral";
}

function paginationWindow(page: number, totalPages: number): Array<number | "..."> {
  if (totalPages <= 7) return Array.from({ length: totalPages }, (_, idx) => idx + 1);
  const pages = new Set<number>([1, totalPages, page, page - 1, page + 1]);
  const sorted = [...pages].filter((item) => item >= 1 && item <= totalPages).sort((a, b) => a - b);
  const out: Array<number | "..."> = [];
  for (const item of sorted) {
    const previous = out[out.length - 1];
    if (typeof previous === "number" && item - previous > 1) out.push("...");
    out.push(item);
  }
  return out;
}

function formatTime(iso?: string): string {
  if (!hasMeaningfulTime(iso)) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function datetimeLocalValue(date: Date): string {
  const offsetMs = date.getTimezoneOffset() * 60 * 1000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function datetimeLocalToISOString(value: string): string {
  const date = new Date(value);
  if (isNaN(date.getTime())) return "";
  return date.toISOString();
}

function boundedDisplayTime(primary?: string, fallback?: string): string | undefined {
  if (!hasMeaningfulTime(primary)) return fallback;
  if (!hasMeaningfulTime(fallback)) return primary;
  const primaryDate = new Date(primary);
  const fallbackDate = new Date(fallback);
  if (isNaN(primaryDate.getTime()) || isNaN(fallbackDate.getTime())) return primary;
  return primaryDate.getTime() > fallbackDate.getTime() + 2 * 60 * 1000 ? fallback : primary;
}

function safeJSONString(value: unknown): string {
  let text = "";
  try {
    text = JSON.stringify(value, null, 2);
  } catch {
    text = String(value);
  }
  text = text
    .replace(/(authorization|cookie|token|secret|password)(["']?\s*[:=]\s*["']?)[^"',\n\r]+/gi, "$1$2[REDACTED]")
    .replace(/(api[_-]?key=)[^&"'\s]+/gi, "$1[REDACTED]");
  if (text.length > 6000) {
    return `${text.slice(0, 6000)}\n... truncated ${text.length - 6000} chars`;
  }
  return text;
}
