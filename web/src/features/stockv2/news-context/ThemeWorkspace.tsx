import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ArrowClockwise, CaretLeft, CaretRight, GitBranch, MagnifyingGlass } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type {
  StockV2NewsContextEvidence,
  StockV2NewsContextNamedObject,
  StockV2NewsContextRelation,
  StockV2NewsContextTheme,
  StockV2NewsContextThemeDetail,
  StockV2NewsContextThemeVersion,
  StockV2PagedResponse,
} from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, CollapsibleSection, EmptyState, Notice, Pill } from "../../../components/ui";
import { useQueryParamState } from "../../../hooks/useQueryParamState";
import {
  confidenceLabel,
  confirmationLabel,
  confirmationTone,
  evidenceRoleLabel,
  formatNewsContextTime,
  indexStatusLabel,
  indexStatusTone,
  namedObjectLabel,
  relationTypeLabel,
  researchStatusLabel,
  researchStatusTone,
  reviewStatusLabel,
  reviewStatusTone,
  themeStageLabel,
  themeStageTone,
  themeTitle,
  windowTypeLabel,
} from "./model";

type StageFilter = "all" | "emerging" | "spreading" | "accelerating" | "overheated" | "diverging" | "retreating" | "dormant" | "restarting";
type ReviewFilter = "all" | "not_required" | "pending" | "running" | "completed" | "failed";
type IndexFilter = "all" | "ready" | "pending" | "stale" | "failed";

const PAGE_SIZE = 30;
const STAGE_FILTERS: readonly StageFilter[] = ["all", "emerging", "spreading", "accelerating", "overheated", "diverging", "retreating", "dormant", "restarting"];
const REVIEW_FILTERS: readonly ReviewFilter[] = ["all", "not_required", "pending", "running", "completed", "failed"];
const INDEX_FILTERS: readonly IndexFilter[] = ["all", "ready", "pending", "stale", "failed"];

export function ThemeWorkspace({
  actions,
  refreshKey,
  selectedId,
  onSelect,
}: {
  actions: AppActions;
  refreshKey: number;
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  const [stage, setStage] = useQueryParamState<StageFilter>("stockv2ThemeStage", STAGE_FILTERS, "all");
  const [review, setReview] = useQueryParamState<ReviewFilter>("stockv2ThemeReview", REVIEW_FILTERS, "all");
  const [index, setIndex] = useQueryParamState<IndexFilter>("stockv2ThemeIndex", INDEX_FILTERS, "all");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<StockV2NewsContextTheme[]>([]);
  const [total, setTotal] = useState(0);
  const [listLoading, setListLoading] = useState(true);
  const [listRefreshing, setListRefreshing] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [detail, setDetail] = useState<StockV2NewsContextThemeDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  async function loadThemes() {
    if (items.length > 0) setListRefreshing(true);
    else setListLoading(true);
    setListError(null);
    try {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((page - 1) * PAGE_SIZE),
      });
      if (stage !== "all") params.set("stage", stage);
      if (review !== "all") params.set("reviewStatus", review);
      if (index !== "all") params.set("indexStatus", index);
      if (query.trim()) params.set("q", query.trim());
      const result = await actions.api<StockV2PagedResponse<StockV2NewsContextTheme>>(
        `/api/stockv2/news-context/themes?${params.toString()}`,
      );
      const nextItems = result.items || [];
      setItems(nextItems);
      setTotal(result.total ?? nextItems.length);
      if (!selectedId && nextItems[0]) onSelect(nextItems[0].id);
    } catch (error) {
      setListError(friendlyError(error));
    } finally {
      setListLoading(false);
      setListRefreshing(false);
    }
  }

  async function loadDetail(themeId: string) {
    setDetailLoading(true);
    setDetailError(null);
    setDetail(null);
    try {
      const result = await actions.api<StockV2NewsContextThemeDetail>(
        `/api/stockv2/news-context/themes/${encodeURIComponent(themeId)}`,
      );
      setDetail(result);
    } catch (error) {
      setDetailError(friendlyError(error));
    } finally {
      setDetailLoading(false);
    }
  }

  useEffect(() => {
    setPage(1);
  }, [stage, review, index, query]);

  useEffect(() => {
    const timer = window.setTimeout(() => void loadThemes(), query.trim() ? 250 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage, review, index, query, page, refreshKey]);

  useEffect(() => {
    if (!selectedId) {
      setDetail(null);
      setDetailError(null);
      return;
    }
    void loadDetail(selectedId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId, refreshKey]);

  return (
    <div className="grid min-h-[680px] grid-cols-[260px_minmax(360px,1fr)_280px] gap-4 max-xl:grid-cols-[250px_minmax(0,1fr)] max-lg:grid-cols-1">
      <aside className="min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <div className="border-b border-[var(--line)] p-3">
          <div className="flex items-center justify-between gap-2">
            <div>
              <h3 className="m-0 text-sm font-semibold">市场主题</h3>
              <p className="mt-1 mb-0 text-xs text-[var(--muted)]">共 {total} 个匹配主题</p>
            </div>
            <Button aria-label="刷新主题" className="px-2" disabled={listRefreshing} onClick={() => void loadThemes()}>
              <ArrowClockwise size={14} />
            </Button>
          </div>
          <label className="relative mt-3 block">
            <MagnifyingGlass aria-hidden className="pointer-events-none absolute top-2.5 left-2.5 text-[var(--muted)]" size={14} />
            <span className="sr-only">搜索主题</span>
            <input
              className="input pl-8 text-xs"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索主题、板块或股票"
              type="search"
              value={query}
            />
          </label>
          <div className="mt-2 grid grid-cols-3 gap-1.5">
            <FilterSelect label="阶段" value={stage} onChange={(value) => setStage(value as StageFilter)} options={STAGE_FILTERS.map((value) => [value, value === "all" ? "全部阶段" : themeStageLabel(value)])} />
            <FilterSelect label="复核" value={review} onChange={(value) => setReview(value as ReviewFilter)} options={REVIEW_FILTERS.map((value) => [value, value === "all" ? "全部复核" : reviewStatusLabel(value)])} />
            <FilterSelect label="索引" value={index} onChange={(value) => setIndex(value as IndexFilter)} options={INDEX_FILTERS.map((value) => [value, value === "all" ? "全部索引" : indexStatusLabel(value)])} />
          </div>
        </div>

        {listError ? (
          <div className="p-3">
            <Notice tone="danger">
              <div className="grid gap-2">
                <span>主题列表加载失败：{listError}</span>
                <Button onClick={() => void loadThemes()}>重试</Button>
              </div>
            </Notice>
          </div>
        ) : null}

        <div className="max-h-[calc(100dvh-390px)] min-h-80 overflow-y-auto">
          {listLoading && items.length === 0 ? <ThemeListSkeleton /> : null}
          {!listLoading && items.length === 0 && !listError ? (
            <div className="p-3">
              <EmptyState title="暂无匹配主题" body="归纳任务发现有效主题后会显示在这里，也可以调整筛选条件。" />
            </div>
          ) : null}
          {items.length > 0 ? (
            <div className="divide-y divide-[var(--line)]">
              {items.map((theme) => (
                <ThemeListRow active={theme.id === selectedId} key={theme.id} onClick={() => onSelect(theme.id)} theme={theme} />
              ))}
            </div>
          ) : null}
        </div>

        {totalPages > 1 ? (
          <div className="flex items-center justify-between border-t border-[var(--line)] p-2 text-xs text-[var(--muted)]">
            <Button aria-label="上一页" className="px-2" disabled={page <= 1 || listRefreshing} onClick={() => setPage((value) => Math.max(1, value - 1))}>
              <CaretLeft size={14} />
            </Button>
            <span>{page} / {totalPages}</span>
            <Button aria-label="下一页" className="px-2" disabled={page >= totalPages || listRefreshing} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>
              <CaretRight size={14} />
            </Button>
          </div>
        ) : null}
      </aside>

      <main className="min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <ThemeDetailBody detail={detail} error={detailError} loading={detailLoading} onRetry={() => selectedId && void loadDetail(selectedId)} />
      </main>

      <aside className="min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] max-xl:col-span-2 max-lg:col-span-1">
        <ThemeInspector detail={detail} error={detailError} loading={detailLoading} />
      </aside>
    </div>
  );
}

function FilterSelect({ label, value, options, onChange }: { label: string; value: string; options: Array<readonly [string, string]>; onChange: (value: string) => void }) {
  return (
    <label>
      <span className="sr-only">{label}</span>
      <select aria-label={label} className="select h-8 min-w-0 px-1.5 text-[11px]" onChange={(event) => onChange(event.target.value)} value={value}>
        {options.map(([optionValue, optionLabel]) => <option key={optionValue} value={optionValue}>{optionLabel}</option>)}
      </select>
    </label>
  );
}

function ThemeListSkeleton() {
  return (
    <div aria-label="主题加载中" className="divide-y divide-[var(--line)]">
      {[0, 1, 2, 3, 4].map((item) => (
        <div className="grid gap-2 p-3" key={item}>
          <div className="h-3 w-3/5 rounded bg-[var(--surface-strong)]" />
          <div className="h-2.5 w-full rounded bg-[var(--surface-soft)]" />
          <div className="h-2.5 w-4/5 rounded bg-[var(--surface-soft)]" />
        </div>
      ))}
    </div>
  );
}

function ThemeListRow({ theme, active, onClick }: { theme: StockV2NewsContextTheme; active: boolean; onClick: () => void }) {
  const objects = [...(theme.industries || []), ...(theme.symbols || []).map(namedObjectLabel)].slice(0, 3);
  return (
    <button
      aria-current={active ? "true" : undefined}
      className={`grid w-full gap-2 border-l-2 p-3 text-left transition ${active ? "border-l-[var(--accent)] bg-[var(--accent-soft)]" : "border-l-transparent hover:bg-[var(--surface-soft)]"}`}
      onClick={onClick}
      type="button"
    >
      <span className="flex items-start justify-between gap-2">
        <strong className="line-clamp-2 text-sm leading-5">{themeTitle(theme)}</strong>
        <Pill className="shrink-0" tone={themeStageTone(theme.stage)}>{themeStageLabel(theme.stage)}</Pill>
      </span>
      <span className="line-clamp-2 text-xs leading-5 text-[var(--muted-strong)]">{theme.latestChange || theme.coreThesis || theme.coreLogic || "等待形成主题结论"}</span>
      <span className="flex flex-wrap items-center gap-1.5 text-[11px] text-[var(--muted)]">
        {objects.map((item) => <span className="truncate" key={item}>{item}</span>)}
        <span className="ml-auto">{formatNewsContextTime(theme.lastChangedAt || theme.updatedAt)}</span>
      </span>
    </button>
  );
}

function ThemeDetailBody({ detail, error, loading, onRetry }: { detail: StockV2NewsContextThemeDetail | null; error: string | null; loading: boolean; onRetry: () => void }) {
  if (loading) return <ThemeDetailSkeleton />;
  if (error) {
    return (
      <div className="p-4">
        <Notice tone="danger">
          <div className="grid gap-2">
            <span>主题详情加载失败：{error}</span>
            <Button onClick={onRetry}>重新加载</Button>
          </div>
        </Notice>
      </div>
    );
  }
  if (!detail?.theme) {
    return <div className="p-4"><EmptyState title="选择一个主题" body="从左侧选择主题，查看当前结论、变化、证据和历史版本。" /></div>;
  }
  const theme = detail.theme;
  const affected = [
    ...(theme.industries || []),
    ...(theme.sectors || []),
    ...(theme.symbols || []).map(namedObjectLabel),
    ...(theme.funds || []).map(namedObjectLabel),
  ];
  return (
    <div className="min-w-0">
      <header className="border-b border-[var(--line)] p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <GitBranch size={16} className="text-[var(--accent)]" />
              <h2 className="m-0 text-base font-semibold">{themeTitle(theme)}</h2>
              <Pill tone={themeStageTone(theme.stage)}>{themeStageLabel(theme.stage)}</Pill>
            </div>
            <p className="mt-2 mb-0 text-sm leading-6 text-[var(--muted-strong)]">{theme.coreThesis || theme.coreLogic || "尚未形成稳定核心逻辑。"}</p>
          </div>
          <span className="text-xs text-[var(--muted)]">更新 {formatNewsContextTime(theme.updatedAt)}</span>
        </div>
      </header>

      <div className="grid gap-4 p-4">
        <section>
          <h3 className="m-0 text-xs font-semibold text-[var(--muted-strong)]">当前结论</h3>
          <p className="mt-2 mb-0 whitespace-pre-wrap text-sm leading-6">{theme.currentConclusion || theme.coreThesis || "等待后续归纳形成结论。"}</p>
        </section>

        {theme.latestChange ? (
          <Notice tone="warn">
            <strong className="mr-2">最近变化</strong>{theme.latestChange}
          </Notice>
        ) : null}

        {affected.length > 0 ? (
          <section className="border-t border-[var(--line)] pt-3">
            <h3 className="m-0 text-xs font-semibold text-[var(--muted-strong)]">影响对象</h3>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {uniqueStrings(affected).map((item) => <Pill key={item} tone="neutral">{item}</Pill>)}
            </div>
          </section>
        ) : null}

        <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
          <TextList title="已确认事实" items={theme.confirmedFacts} empty="暂无已确认事实" />
          <TextList title="推断与影响假设" items={theme.inferences} empty="暂无推断" />
          <TextList title="反面证据与冲突" items={[...(theme.counterEvidence || []), ...(theme.conflicts || [])]} empty="暂无反面证据" tone="warn" />
          <TextList title="未决问题" items={theme.openQuestions} empty="暂无未决问题" tone="warn" />
        </div>

        <CollapsibleSection defaultOpen title="对象角色与主题关系" subtitle="龙头、跟随、滞涨、潜在接力和有证据的主题关系">
          <ObjectRoles theme={theme} />
          <RelationList relations={theme.relations || []} />
        </CollapsibleSection>

        <CollapsibleSection title="来源证据" subtitle={`${detail.evidence?.length || 0} 条精简证据，标明原文是否已经清理`}>
          <EvidenceList items={detail.evidence || []} />
        </CollapsibleSection>

        <CollapsibleSection title="版本时间线" subtitle={`${detail.versions?.length || 0} 个主题版本`}>
          <VersionTimeline items={detail.versions || []} />
        </CollapsibleSection>
      </div>
    </div>
  );
}

function ThemeDetailSkeleton() {
  return (
    <div aria-label="主题详情加载中" className="grid gap-4 p-4">
      <div className="h-5 w-2/5 rounded bg-[var(--surface-strong)]" />
      <div className="h-3 w-full rounded bg-[var(--surface-soft)]" />
      <div className="h-3 w-4/5 rounded bg-[var(--surface-soft)]" />
      <div className="grid grid-cols-2 gap-3">
        {[0, 1, 2, 3].map((item) => <div className="h-28 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]" key={item} />)}
      </div>
    </div>
  );
}

function TextList({ title, items = [], empty, tone = "neutral" }: { title: string; items?: string[]; empty: string; tone?: "neutral" | "warn" }) {
  return (
    <section className={`rounded-lg border p-3 ${tone === "warn" ? "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]" : "border-[var(--line)] bg-[var(--surface-soft)]"}`}>
      <h3 className="m-0 text-xs font-semibold">{title}</h3>
      {items.length > 0 ? (
        <ul className="mt-2 mb-0 grid gap-1.5 pl-4 text-xs leading-5 text-[var(--muted-strong)]">
          {items.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}
        </ul>
      ) : <p className="mt-2 mb-0 text-xs text-[var(--muted)]">{empty}</p>}
    </section>
  );
}

function ObjectRoles({ theme }: { theme: StockV2NewsContextTheme }) {
  const groups: Array<[string, Array<string | StockV2NewsContextNamedObject>]> = [
    ["龙头", theme.leaders || []],
    ["跟随", theme.followers || []],
    ["滞涨", theme.laggards || []],
    ["潜在接力", theme.relayCandidates || []],
  ];
  return (
    <div className="grid grid-cols-2 gap-2 max-lg:grid-cols-1">
      {groups.map(([label, values]) => (
        <div className="rounded border border-[var(--line)] bg-[var(--surface)] p-2.5" key={label}>
          <div className="text-xs text-[var(--muted)]">{label}</div>
          <div className="mt-1.5 text-sm">{values.length ? values.map(namedObjectLabel).join("、") : "暂无"}</div>
        </div>
      ))}
    </div>
  );
}

function RelationList({ relations }: { relations: StockV2NewsContextRelation[] }) {
  if (!relations.length) return <p className="m-0 text-xs text-[var(--muted)]">暂无有证据的主题关系。</p>;
  return (
    <div className="grid gap-2">
      {relations.map((relation, index) => (
        <div className="border-l-2 border-[var(--line-strong)] pl-3" key={relation.id || `${relation.targetThemeId}-${index}`}>
          <div className="flex flex-wrap items-center gap-2">
            <strong className="text-sm">{relation.targetThemeTitle || relation.targetThemeId || "相关主题"}</strong>
            <Pill tone="neutral">{relationTypeLabel(relation.relationType)}</Pill>
            {typeof relation.confidence === "number" ? <span className="text-xs text-[var(--muted)]">可信程度 {confidenceLabel(relation.confidence)}</span> : null}
          </div>
          {relation.summary || relation.evidenceSummary ? <p className="mt-1 mb-0 text-xs leading-5 text-[var(--muted-strong)]">{relation.summary || relation.evidenceSummary}</p> : null}
        </div>
      ))}
    </div>
  );
}

function EvidenceList({ items }: { items: StockV2NewsContextEvidence[] }) {
  if (!items.length) return <p className="m-0 text-xs text-[var(--muted)]">暂无精简证据。</p>;
  return (
    <div className="divide-y divide-[var(--line)] rounded-lg border border-[var(--line)] bg-[var(--surface)]">
      {items.map((item) => {
        const safeURL = sanitizedEvidenceURL(item.url);
        return (
          <article className="p-3" key={item.id}>
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div className="min-w-0">
                <strong className="block text-sm">{item.title || item.source || "未命名证据"}</strong>
                <span className="mt-1 block text-xs text-[var(--muted)]">{item.source || "未知来源"} / {formatNewsContextTime(item.publishedAt || item.createdAt)}</span>
              </div>
              <div className="flex flex-wrap gap-1.5">
                <Pill tone={item.evidenceRole === "contradict" || item.evidenceRole === "weaken" ? "warn" : "neutral"}>{evidenceRoleLabel(item.evidenceRole)}</Pill>
                <Pill tone={item.originalNewsDeleted ? "neutral" : "good"}>{item.originalNewsDeleted ? "原文已清理" : "原文保留"}</Pill>
                {item.protected ? <Pill tone="warn">受保护</Pill> : null}
              </div>
            </div>
            {item.summary ? <p className="mt-2 mb-0 text-xs leading-5 text-[var(--muted-strong)]">{item.summary}</p> : null}
            {item.protectedReason ? <p className="mt-2 mb-0 text-xs text-[var(--warn)]">保护原因：{item.protectedReason}</p> : null}
            {safeURL ? <a className="mt-2 inline-block text-xs text-[var(--accent)]" href={safeURL} rel="noreferrer" target="_blank">查看公开来源</a> : null}
          </article>
        );
      })}
    </div>
  );
}

function VersionTimeline({ items }: { items: StockV2NewsContextThemeVersion[] }) {
  if (!items.length) return <p className="m-0 text-xs text-[var(--muted)]">暂无历史版本。</p>;
  return (
    <ol className="m-0 grid list-none gap-0 p-0">
      {items.map((item, index) => (
        <li className="relative grid grid-cols-[18px_minmax(0,1fr)] gap-2 pb-4 last:pb-0" key={item.id}>
          <span className="relative mt-1 h-2.5 w-2.5 rounded-full border-2 border-[var(--surface)] bg-[var(--accent)] shadow-[0_0_0_1px_var(--line-strong)]">
            {index < items.length - 1 ? <span className="absolute top-3 left-[3px] h-[calc(100%+44px)] w-px bg-[var(--line)]" /> : null}
          </span>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-sm">版本 {item.versionNo ?? items.length - index}</strong>
              <Pill tone={themeStageTone(item.stage)}>{themeStageLabel(item.stage)}</Pill>
              {item.windowType ? <Pill tone="neutral">{windowTypeLabel(item.windowType)}</Pill> : null}
              <span className="text-xs text-[var(--muted)]">{formatNewsContextTime(item.createdAt)}</span>
            </div>
            <p className="mt-1 mb-0 text-xs leading-5 text-[var(--muted-strong)]">{item.changeSummary || item.conclusion || "没有变化摘要"}</p>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {item.researchStatus ? <Pill tone={researchStatusTone(item.researchStatus)}>{researchStatusLabel(item.researchStatus)}</Pill> : null}
              {item.reviewStatus ? <Pill tone={reviewStatusTone(item.reviewStatus)}>{reviewStatusLabel(item.reviewStatus)}</Pill> : null}
              {item.indexStatus ? <Pill tone={indexStatusTone(item.indexStatus)}>{indexStatusLabel(item.indexStatus)}</Pill> : null}
            </div>
            {item.indexError ? <p className="mt-1 mb-0 text-xs text-[var(--danger)]">{item.indexError}</p> : null}
          </div>
        </li>
      ))}
    </ol>
  );
}

function ThemeInspector({ detail, error, loading }: { detail: StockV2NewsContextThemeDetail | null; error: string | null; loading: boolean }) {
  return (
    <div className="p-4">
      <h3 className="m-0 text-sm font-semibold">主题检查</h3>
      <p className="mt-1 mb-3 text-xs text-[var(--muted)]">持续显示安全性、连续性和检索状态</p>
      {loading ? <p className="text-xs text-[var(--muted)]">加载检查状态...</p> : null}
      {!loading && error ? <Notice tone="danger">详情不可用，暂时无法确认主题安全状态。</Notice> : null}
      {!loading && !error && !detail?.theme ? <p className="text-xs text-[var(--muted)]">选择主题后显示检查状态。</p> : null}
      {!loading && detail?.theme ? (
        <div className="grid gap-0">
          <InspectorRow label="当前阶段"><Pill tone={themeStageTone(detail.theme.stage)}>{themeStageLabel(detail.theme.stage)}</Pill></InspectorRow>
          <InspectorRow label="可信程度"><strong>{confidenceLabel(detail.theme.confidence)}</strong></InspectorRow>
          <InspectorRow label="数据确认"><Pill tone={confirmationTone(detail.theme.dataConfirmation)}>{confirmationLabel(detail.theme.dataConfirmation)}</Pill></InspectorRow>
          <InspectorRow label="最近变化"><span>{formatNewsContextTime(detail.theme.lastChangedAt || detail.theme.updatedAt)}</span></InspectorRow>
          <InspectorRow label="影响复核"><Pill tone={reviewStatusTone(detail.theme.reviewStatus)}>{reviewStatusLabel(detail.theme.reviewStatus)}</Pill></InspectorRow>
          <InspectorRow label="主题索引"><Pill tone={indexStatusTone(detail.indexStatus || detail.theme.indexStatus)}>{indexStatusLabel(detail.indexStatus || detail.theme.indexStatus)}</Pill></InspectorRow>
          <InspectorRow label="索引更新"><span>{formatNewsContextTime(detail.indexUpdatedAt || detail.theme.indexUpdatedAt)}</span></InspectorRow>
          <InspectorRow label="CLI 检索"><Pill tone={detail.mcpReadable ? "good" : "danger"}>{detail.mcpReadable ? "可读取" : "不可读取"}</Pill></InspectorRow>
          <InspectorRow label="下游引用"><span>{downstreamReferenceCount(detail)} 项</span></InspectorRow>
          {detail.indexError || detail.mcpError ? (
            <div className="py-3"><Notice tone="danger">{[detail.indexError, detail.mcpError].filter(Boolean).join("；")}</Notice></div>
          ) : null}
          {detail.protectedReasons?.length ? (
            <div className="py-3">
              <Notice tone="warn">
                <strong className="block">原新闻暂不可清理</strong>
                <ul className="mt-2 mb-0 grid gap-1 pl-4 text-xs">
                  {detail.protectedReasons.map((reason, index) => <li key={`${reason}-${index}`}>{reason}</li>)}
                </ul>
              </Notice>
            </div>
          ) : (
            <p className="border-t border-[var(--line)] py-3 text-xs text-[var(--muted)]">当前没有返回保护原因，是否清理由后端安全门最终决定。</p>
          )}
          {detail.theme.lastUsedBy?.length ? (
            <div className="border-t border-[var(--line)] pt-3">
              <div className="text-xs text-[var(--muted)]">最近使用方</div>
              <div className="mt-2 flex flex-wrap gap-1.5">{detail.theme.lastUsedBy.map((item) => <Pill key={item}>{item}</Pill>)}</div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function InspectorRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] items-start gap-2 border-b border-[var(--line)] py-2.5 text-xs last:border-b-0">
      <span className="text-[var(--muted)]">{label}</span>
      <span className="min-w-0 break-words text-right text-[var(--muted-strong)]">{children}</span>
    </div>
  );
}

function downstreamReferenceCount(detail: StockV2NewsContextThemeDetail): number {
  return (detail.relatedAlerts?.length || 0)
    + (detail.relatedReviews?.length || 0)
    + (detail.relatedOpportunities?.length || 0)
    + (detail.relatedStrategies?.length || 0);
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.map((item) => item.trim()).filter(Boolean))];
}

function sanitizedEvidenceURL(raw?: string): string | undefined {
  if (!raw) return undefined;
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    url.search = "";
    url.hash = "";
    return url.toString();
  } catch {
    return undefined;
  }
}
