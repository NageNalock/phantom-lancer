import { ArrowsClockwise, Lightning, ShieldCheck } from "@phosphor-icons/react";
import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2OperationReview,
  StockV2PortfolioSentinelConfig,
  StockV2PortfolioSentinelConfigInput,
  StockV2PortfolioSentinelRun,
  StockV2PortfolioSentinelRunDetail,
  StockV2PortfolioSentinelRunListResponse,
  StockV2RequestRunPortfolioSentinel,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Pill } from "../../components/ui";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";
import { StockV2ReviewDrawer } from "./StockV2ReviewDrawer";
import {
  formatDate,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunStatusTone,
  stockV2ReviewOutputTypeLabel,
  stockV2ReviewStatusLabel,
  stockV2ReviewStatusTone,
  stockV2SentinelRiskLabel,
  stockV2SentinelRiskTone,
  stockV2SentinelStatusLabel,
  stockV2SentinelStatusTone,
  stockV2SentinelTriggerLabel,
  stockV2SentinelWindowLabel,
} from "../../domain/labels";

// 组合哨兵:窗口级组合信息面 + 数据面巡检。后台定时或手动触发,Agent 判断
// 利好/利空/噪音,生成组合级风险结论,必要时 fan-out 到单票 OperationReview。
// 遵循 Quiet Agent Workbench 风格:列表为主,drawer 看 detail,弹窗触发。

const RUN_PAGE_SIZE = 10;

export function StockV2Sentinel({ actions }: { actions: AppActions }) {
  const [config, setConfig] = useState<StockV2PortfolioSentinelConfig | null>(null);
  const [configLoading, setConfigLoading] = useState(true);
  const [configDraft, setConfigDraft] = useState<StockV2PortfolioSentinelConfigInput>({});
  const [savingConfig, setSavingConfig] = useState(false);

  const [runs, setRuns] = useState<StockV2PortfolioSentinelRun[]>([]);
  const [runsTotal, setRunsTotal] = useState(0);
  const [runsPage, setRunsPage] = useState(1);
  const [runsLoading, setRunsLoading] = useState(false);
  const [filterStatus, setFilterStatus] = useState("");
  const [filterWindow, setFilterWindow] = useState("");
  const [filterTrigger, setFilterTrigger] = useState("");

  const [selectedDetail, setSelectedDetail] = useState<StockV2PortfolioSentinelRunDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const [triggerOpen, setTriggerOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const [agentRunId, setAgentRunId] = useState<string | null>(null);
  const [reviewHitId, setReviewHitId] = useState<string | null>(null);

  async function fetchConfig() {
    setConfigLoading(true);
    try {
      const cfg = await actions.api<StockV2PortfolioSentinelConfig>("/api/stockv2/portfolio-sentinel/config");
      setConfig(cfg);
      setConfigDraft({
        enabled: cfg.enabled,
        preMarketEnabled: cfg.preMarketEnabled,
        middayEnabled: cfg.middayEnabled,
        postCloseEnabled: cfg.postCloseEnabled,
        maxNewsItems: cfg.maxNewsItems,
        maxNewsPerHolding: cfg.maxNewsPerHolding,
        agentDoublecheckEnabled: cfg.agentDoublecheckEnabled,
      });
    } catch (err) {
      actions.setToast(`加载配置失败:${friendlyError(err)}`, "danger");
    } finally {
      setConfigLoading(false);
    }
  }

  async function saveConfig() {
    setSavingConfig(true);
    try {
      const cfg = await actions.api<StockV2PortfolioSentinelConfig>("/api/stockv2/portfolio-sentinel/config", {
        method: "PUT",
        body: configDraft,
      });
      setConfig(cfg);
      actions.setToast("已保存哨兵配置", "good");
    } catch (err) {
      actions.setToast(`保存配置失败:${friendlyError(err)}`, "danger");
    } finally {
      setSavingConfig(false);
    }
  }

  async function fetchRuns(page = runsPage) {
    setRunsLoading(true);
    try {
      const params = new URLSearchParams({ limit: String(RUN_PAGE_SIZE), offset: String((Math.max(1, page) - 1) * RUN_PAGE_SIZE) });
      if (filterStatus) params.set("status", filterStatus);
      if (filterWindow) params.set("windowType", filterWindow);
      if (filterTrigger) params.set("triggerType", filterTrigger);
      const res = await actions.api<StockV2PortfolioSentinelRunListResponse>(`/api/stockv2/portfolio-sentinel/runs?${params}`);
      setRuns(res.items || []);
      setRunsTotal(res.total ?? res.items?.length ?? 0);
    } catch {
      setRuns([]);
      setRunsTotal(0);
    } finally {
      setRunsLoading(false);
    }
  }

  async function openDetail(run: StockV2PortfolioSentinelRun) {
    setSelectedDetail({ run });
    setDetailLoading(true);
    try {
      const detail = await actions.api<StockV2PortfolioSentinelRunDetail>(`/api/stockv2/portfolio-sentinel/runs/${run.id}`);
      setSelectedDetail(detail);
    } catch (err) {
      actions.setToast(`加载详情失败:${friendlyError(err)}`, "danger");
      setSelectedDetail(null);
    } finally {
      setDetailLoading(false);
    }
  }

  async function triggerRun(input: StockV2RequestRunPortfolioSentinel) {
    setSubmitting(true);
    try {
      await actions.api<StockV2PortfolioSentinelRun>("/api/stockv2/portfolio-sentinel/runs", { method: "POST", body: input });
      actions.setToast("已触发组合哨兵", "good");
      setTriggerOpen(false);
      await fetchRuns(1);
      void fetchConfig();
    } catch (err) {
      actions.setToast(`触发失败:${friendlyError(err)}`, "danger");
    } finally {
      setSubmitting(false);
    }
  }

  useEffect(() => {
    void fetchConfig();
    void fetchRuns();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    void fetchRuns(runsPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runsPage]);

  const runsTotalPages = Math.max(1, Math.ceil(runsTotal / RUN_PAGE_SIZE));
  const runsPageNumbers = useMemo(() => paginationWindow(runsPage, runsTotalPages), [runsPage, runsTotalPages]);
  const latestRun = runs[0];
  const enabled = !!config?.enabled;

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <ShieldCheck size={16} className="text-[var(--accent)]" />
          <strong>组合哨兵</strong>
          <Pill tone={enabled ? "good" : "neutral"}>{configLoading ? "加载中" : enabled ? "已启用" : "未启用"}</Pill>
          {config ? (
            <span className="text-xs text-[var(--muted)]">
              窗口 {[
                config.preMarketEnabled && "盘前",
                config.middayEnabled && "午间",
                config.postCloseEnabled && "盘后",
              ].filter(Boolean).join("/") || "无"}
            </span>
          ) : null}
          {latestRun ? (
            <span className="text-xs text-[var(--muted-strong)]">
              最近运行 {stockV2SentinelStatusLabel(latestRun.status)} · {formatDate(latestRun.finishedAt || latestRun.startedAt) || "-"}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={() => void fetchRuns()} disabled={runsLoading}>
            <ArrowsClockwise size={12} className="mr-1" />
            刷新
          </Button>
          <Button tone="primary" onClick={() => setTriggerOpen(true)} disabled={submitting}>
            <Lightning size={12} className="mr-1" />
            手动触发
          </Button>
        </div>
      </div>

      <CollapsibleSection
        title="哨兵配置"
        subtitle="开关与三个定时窗口;手动触发不受窗口开关限制。每个组合最多新闻数 / 每个持仓最多新闻数控制进入 Agent 的消息量。"
        defaultOpen={false}
      >
        {configLoading ? (
          <p className="text-sm text-[var(--muted)]">加载配置…</p>
        ) : config ? (
          <div className="grid gap-3">
            <div className="grid gap-2 sm:grid-cols-2">
              <ToggleRow
                label="启用组合哨兵"
                checked={!!configDraft.enabled}
                onChange={(v) => setConfigDraft((d) => ({ ...d, enabled: v }))}
              />
              <ToggleRow
                label="Agent doublecheck"
                checked={!!configDraft.agentDoublecheckEnabled}
                onChange={(v) => setConfigDraft((d) => ({ ...d, agentDoublecheckEnabled: v }))}
              />
              <ToggleRow
                label="盘前窗口"
                checked={!!configDraft.preMarketEnabled}
                onChange={(v) => setConfigDraft((d) => ({ ...d, preMarketEnabled: v }))}
              />
              <ToggleRow
                label="午间窗口"
                checked={!!configDraft.middayEnabled}
                onChange={(v) => setConfigDraft((d) => ({ ...d, middayEnabled: v }))}
              />
              <ToggleRow
                label="盘后/夜间窗口"
                checked={!!configDraft.postCloseEnabled}
                onChange={(v) => setConfigDraft((d) => ({ ...d, postCloseEnabled: v }))}
              />
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <Field label="每个组合最多新闻数" help="进入 Agent 的消息面上限,默认 80。">
                <input
                  type="number"
                  min="0"
                  step="1"
                  value={String(configDraft.maxNewsItems ?? 0)}
                  onChange={(e) => setConfigDraft((d) => ({ ...d, maxNewsItems: Math.max(0, Number(e.target.value) || 0) }))}
                />
              </Field>
              <Field label="每个持仓最多新闻数" help="单标的消息上限,默认 20。">
                <input
                  type="number"
                  min="0"
                  step="1"
                  value={String(configDraft.maxNewsPerHolding ?? 0)}
                  onChange={(e) => setConfigDraft((d) => ({ ...d, maxNewsPerHolding: Math.max(0, Number(e.target.value) || 0) }))}
                />
              </Field>
            </div>
            <p className="text-xs leading-relaxed text-[var(--muted)]">
              哨兵只做风险摘要与操作提案,不自动下单,不直接修改持仓,不自动接受 OperationReview。
            </p>
            <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
              <Button tone="primary" disabled={savingConfig} onClick={() => void saveConfig()}>
                {savingConfig ? "保存中…" : "保存配置"}
              </Button>
            </div>
          </div>
        ) : (
          <Notice tone="warn">配置暂不可用。</Notice>
        )}
      </CollapsibleSection>

      <CollapsibleSection
        title="运行历史"
        subtitle={runsTotal > 0 ? `${runsTotal} 次哨兵运行 · 展示窗口、风险级别、扫描规模、Agent 状态与生成对象` : "暂无哨兵运行"}
        defaultOpen={true}
      >
        <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
          <FilterSelect label="状态" value={filterStatus} onChange={setFilterStatus} options={[["running", "运行中"], ["completed", "已完成"], ["failed", "失败"]]} />
          <FilterSelect label="窗口" value={filterWindow} onChange={setFilterWindow} options={[["manual", "手动"], ["pre_market", "盘前"], ["midday", "午间"], ["post_close", "盘后"]]} />
          <FilterSelect label="触发" value={filterTrigger} onChange={setFilterTrigger} options={[["manual", "手动触发"], ["scheduled", "定时触发"]]} />
          <Button
            onClick={() => {
              setFilterStatus("");
              setFilterWindow("");
              setFilterTrigger("");
              void fetchRuns(1);
            }}
          >
            清除筛选
          </Button>
          <Button onClick={() => void fetchRuns()}>应用筛选</Button>
        </div>

        {runsLoading ? (
          <p className="text-sm text-[var(--muted)]">加载运行历史…</p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">暂无运行。手动触发或等待定时窗口后,历史会出现在这里。</p>
        ) : (
          <>
            <div className="grid gap-2">
              {runs.map((run) => (
                <SentinelRunRow key={run.id} run={run} onOpen={() => void openDetail(run)} />
              ))}
            </div>
            <Pagination
              loading={runsLoading}
              page={runsPage}
              pageNumbers={runsPageNumbers}
              pageSize={RUN_PAGE_SIZE}
              total={runsTotal}
              totalPages={runsTotalPages}
              onPage={setRunsPage}
              label="运行页码"
            />
          </>
        )}
      </CollapsibleSection>

      {selectedDetail ? (
        <SentinelRunDrawer
          detail={selectedDetail}
          loading={detailLoading}
          onClose={() => setSelectedDetail(null)}
          onOpenAgentRun={(id) => setAgentRunId(id)}
          onOpenReview={(hitId) => setReviewHitId(hitId)}
        />
      ) : null}

      {triggerOpen ? (
        <TriggerDrawer submitting={submitting} onClose={() => setTriggerOpen(false)} onSubmit={(input) => void triggerRun(input)} />
      ) : null}

      {reviewHitId ? <StockV2ReviewDrawer actions={actions} hitId={reviewHitId} onClose={() => setReviewHitId(null)} /> : null}
      {agentRunId ? <StockV2AgentRunDetailDrawer actions={actions} runId={agentRunId} onClose={() => setAgentRunId(null)} /> : null}
    </div>
  );
}

function ToggleRow({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex min-h-10 cursor-pointer items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm">
      <span>{label}</span>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
    </label>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: Array<[string, string]>;
}) {
  return (
    <label className="flex items-center gap-1.5">
      <span className="text-[var(--muted)]">{label}</span>
      <select className="select h-8 text-xs" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">全部</option>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </select>
    </label>
  );
}

function SentinelRunRow({ run, onOpen }: { run: StockV2PortfolioSentinelRun; onOpen: () => void }) {
  const riskTone = stockV2SentinelRiskTone(run.resultRiskLevel);
  return (
    <button
      className="w-full rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-left text-xs transition hover:bg-[var(--surface-soft)]"
      onClick={onOpen}
      type="button"
    >
      <div className="flex flex-wrap items-center gap-2">
        <Pill tone="neutral">{stockV2SentinelWindowLabel(run.windowType)}</Pill>
        <Pill tone={stockV2SentinelStatusTone(run.status)}>{stockV2SentinelStatusLabel(run.status)}</Pill>
        {run.resultRiskLevel ? <Pill tone={riskTone}>风险 {stockV2SentinelRiskLabel(run.resultRiskLevel)}</Pill> : null}
        <Pill tone={run.agentRunId ? "good" : "neutral"}>Agent {run.agentRunId ? "已触发" : "未进入"}</Pill>
        <span className="text-[var(--muted)]">· {stockV2SentinelTriggerLabel(run.triggerType)}</span>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[var(--muted-strong)]">
        <span>组合 {run.scannedPortfolioCount ?? 0}</span>
        <span>持仓 {run.scannedHoldingCount ?? 0}</span>
        <span>新闻 {run.newsEventCount ?? 0}/{run.rawNewsCount ?? 0}</span>
        <span>行情 {run.quoteCount ?? 0}</span>
        <span>生成 Alert {run.generatedAlertCount ?? 0} · Review {run.generatedReviewCount ?? 0}</span>
      </div>
      {run.errorMessage ? <p className="mt-1 break-words text-[var(--danger)]">{run.errorMessage}</p> : null}
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[var(--muted)]">
        <span>{formatDate(run.windowStartAt) || "-"} → {formatDate(run.windowEndAt) || "-"}</span>
        <span>完成 {formatDate(run.finishedAt) || (run.status === "running" ? "进行中" : "-")}</span>
        <span className="text-[var(--accent)]">点击查看详情</span>
      </div>
    </button>
  );
}

function SentinelRunDrawer({
  detail,
  loading,
  onClose,
  onOpenAgentRun,
  onOpenReview,
}: {
  detail: StockV2PortfolioSentinelRunDetail;
  loading: boolean;
  onClose: () => void;
  onOpenAgentRun: (runId: string) => void;
  onOpenReview: (hitId: string) => void;
}) {
  const { run, result } = detail;
  const report = readReport(result?.rawResult);
  const context = result?.contextSummary || {};
  return (
    <Drawer
      title={`组合哨兵详情 · ${stockV2SentinelWindowLabel(run.windowType)}`}
      subtitle={`${stockV2SentinelStatusLabel(run.status)} · ${stockV2SentinelTriggerLabel(run.triggerType)} · ${formatDate(run.startedAt) || "-"}`}
      onClose={onClose}
      width={640}
    >
      {loading ? <p className="text-sm text-[var(--muted)]">加载详情…</p> : null}
      <div className="grid gap-4 text-sm">
        <div className="grid grid-cols-2 gap-2">
          <SummaryCell label="状态" value={stockV2SentinelStatusLabel(run.status)} tone={stockV2SentinelStatusTone(run.status)} />
          <SummaryCell label="风险级别" value={stockV2SentinelRiskLabel(run.resultRiskLevel || report.overallRiskLevel)} tone={stockV2SentinelRiskTone(run.resultRiskLevel || report.overallRiskLevel)} />
          <SummaryCell label="扫描规模" value={`组合 ${run.scannedPortfolioCount ?? 0} · 持仓 ${run.scannedHoldingCount ?? 0}`} tone="neutral" />
          <SummaryCell label="消息面" value={`事件 ${run.newsEventCount ?? 0} · 原始 ${run.rawNewsCount ?? 0}`} tone="neutral" />
          <SummaryCell
            label="Agent"
            value={detail.agentRun ? stockV2AgentRunStatusLabel(detail.agentRun.status) : run.agentRunId ? "已触发" : "未进入"}
            tone={detail.agentRun ? stockV2AgentRunStatusTone(detail.agentRun.status) : run.agentRunId ? "good" : "neutral"}
          />
        </div>

        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="grid gap-1.5">
            <KeyValue label="时间窗口" value={`${formatDate(run.windowStartAt) || "-"} → ${formatDate(run.windowEndAt) || "-"}`} />
            <KeyValue label="市场数据" value={`行情 ${run.quoteCount ?? 0} · 日K标的 ${run.dailyBarSymbolCount ?? 0} · 分钟线标的 ${run.minuteBarSymbolCount ?? 0}`} />
            <KeyValue label="生成对象" value={`Alert ${run.generatedAlertCount ?? 0} · MonitorHit ${run.generatedHitCount ?? 0} · Review ${run.generatedReviewCount ?? 0}`} />
            {run.agentRunId ? <KeyValue label="AgentRun" value={run.agentRunId} mono /> : null}
            {run.decisionLedgerId ? <KeyValue label="DecisionLedger" value={run.decisionLedgerId} mono /> : null}
            {run.errorMessage ? <KeyValue label="错误" value={run.errorMessage} /> : null}
          </div>
        </div>

        {result?.summary || report.runSummary ? (
          <div>
            <strong className="text-sm">风险摘要</strong>
            <p className="mt-1 text-xs leading-relaxed text-[var(--muted-strong)]">{result?.summary || report.runSummary}</p>
          </div>
        ) : null}

        {report.affectedHoldings.length > 0 ? (
          <div>
            <strong className="text-sm">受影响持仓</strong>
            <div className="mt-2 grid gap-2">
              {report.affectedHoldings.map((item, idx) => (
                <AffectedHoldingItem key={idx} item={item} />
              ))}
            </div>
          </div>
        ) : null}

        {(report.positiveItems.length > 0 || report.negativeItems.length > 0 || report.noiseItems.length > 0) ? (
          <div className="grid gap-2">
            {report.positiveItems.length > 0 ? <ItemList title="利好" tone="good" items={report.positiveItems} /> : null}
            {report.negativeItems.length > 0 ? <ItemList title="利空" tone="danger" items={report.negativeItems} /> : null}
            {report.noiseItems.length > 0 ? <ItemList title="噪音" tone="neutral" items={report.noiseItems} /> : null}
          </div>
        ) : null}

        {report.dataQualityNotes.length > 0 ? (
          <div>
            <strong className="text-sm">数据质量备注</strong>
            <ul className="mt-1 grid gap-1 text-xs text-[var(--muted-strong)]">
              {report.dataQualityNotes.map((note, idx) => (
                <li key={idx}>· {note}</li>
              ))}
            </ul>
          </div>
        ) : null}

        {report.nextWatchFocus.length > 0 ? (
          <div>
            <strong className="text-sm">后续关注</strong>
            <ul className="mt-1 grid gap-1 text-xs text-[var(--muted-strong)]">
              {report.nextWatchFocus.map((focus, idx) => (
                <li key={idx}>· {focus}</li>
              ))}
            </ul>
          </div>
        ) : null}

        {detail.alerts && detail.alerts.length > 0 ? (
          <div>
            <strong className="text-sm">生成的 Alert</strong>
            <div className="mt-2 grid gap-2">
              {detail.alerts.map((alert) => (
                <div key={alert.id} className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
                  <div className="flex flex-wrap items-center gap-2">
                    <Pill tone={stockV2SentinelRiskTone(alert.level)}>{stockV2SentinelRiskLabel(alert.level)}</Pill>
                    {alert.symbol ? <Pill tone="neutral">{alert.symbol}</Pill> : null}
                    <strong>{alert.title || alert.id}</strong>
                  </div>
                  {alert.summary ? <p className="mt-1 break-words text-[var(--muted-strong)]">{alert.summary}</p> : null}
                </div>
              ))}
            </div>
          </div>
        ) : null}

        {detail.reviews && detail.reviews.length > 0 ? (
          <div>
            <strong className="text-sm">生成的 OperationReview</strong>
            <div className="mt-2 grid gap-2">
              {detail.reviews.map((review) => (
                <ReviewItem key={review.id} review={review} onOpenReview={onOpenReview} />
              ))}
            </div>
          </div>
        ) : null}

        {result?.contextSummary ? (
          <details className="rounded border border-[var(--line)] bg-[var(--surface-soft)]">
            <summary className="cursor-pointer px-2 py-1 text-xs text-[var(--muted)]">Context 统计与数据新鲜度</summary>
            <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words px-2 py-2 text-[11px] text-[var(--muted-strong)]">
              {stringifyJSON(context)}
            </pre>
          </details>
        ) : null}

        {result?.rawResult ? (
          <details className="rounded border border-[var(--line)] bg-[var(--surface-soft)]">
            <summary className="cursor-pointer px-2 py-1 text-xs text-[var(--muted)]">Agent 原始结果 (raw_result)</summary>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words px-2 py-2 text-[11px] text-[var(--muted-strong)]">
              {stringifyJSON(result.rawResult)}
            </pre>
          </details>
        ) : null}

        <div className="flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-3">
          {run.agentRunId ? (
            <Button tone="primary" onClick={() => { const id = run.agentRunId; if (id) onOpenAgentRun(id); }}>
              查看 Agent 执行
            </Button>
          ) : null}
        </div>
      </div>
    </Drawer>
  );
}

function AffectedHoldingItem({ item }: { item: Record<string, unknown> }) {
  const symbol = str(item["symbol"]);
  const name = str(item["name"]);
  const risk = str(item["risk_level"]);
  const direction = str(item["direction"] || item["impact"]);
  const reasons = arr(item["reasons"]).map(str);
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        {symbol ? <Pill tone="neutral">{symbol}</Pill> : null}
        {name ? <strong className="text-sm">{name}</strong> : null}
        {risk ? <Pill tone={stockV2SentinelRiskTone(risk)}>风险 {stockV2SentinelRiskLabel(risk)}</Pill> : null}
        {direction ? <Pill tone={direction === "negative" ? "danger" : direction === "positive" ? "good" : "neutral"}>{direction}</Pill> : null}
      </div>
      {reasons.length > 0 ? (
        <ul className="mt-1.5 grid gap-1 text-[var(--muted-strong)]">
          {reasons.map((r, idx) => (
            <li key={idx}>· {r}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function ItemList({ title, tone, items }: { title: string; tone: "good" | "warn" | "danger" | "neutral"; items: Array<Record<string, unknown>> }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
      <div className="flex items-center gap-2">
        <Pill tone={tone}>{title}</Pill>
        <span className="text-[var(--muted)]">{items.length} 条</span>
      </div>
      <ul className="mt-1.5 grid gap-1 text-[var(--muted-strong)]">
        {items.map((item, idx) => (
          <li key={idx}>· {str(item["summary"] || item["title"] || item["reason"]) || stringifyJSON(item)}</li>
        ))}
      </ul>
    </div>
  );
}

function ReviewItem({ review, onOpenReview }: { review: StockV2OperationReview; onOpenReview: (hitId: string) => void }) {
  const guardrails = mapFromAny(review.result?.["guardrails"]);
  const guardStatus = str(guardrails["status"]);
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <Pill tone={stockV2ReviewStatusTone(review.status)}>{stockV2ReviewStatusLabel(review.status)}</Pill>
        {review.outputType ? <Pill tone="neutral">{stockV2ReviewOutputTypeLabel(review.outputType)}</Pill> : null}
        {review.symbol ? <Pill tone="neutral">{review.symbol}</Pill> : null}
        {guardStatus ? <Pill tone={guardStatus === "blocked" ? "danger" : "good"}>guardrails {guardStatus}</Pill> : null}
      </div>
      {review.resultSummary ? <p className="mt-1 break-words text-[var(--muted-strong)]">{review.resultSummary}</p> : null}
      <div className="mt-2 flex justify-end gap-1.5">
        <Button tone="primary" disabled={!review.hitId} onClick={() => review.hitId && onOpenReview(review.hitId)}>
          查看 Review
        </Button>
      </div>
    </div>
  );
}

function TriggerDrawer({
  submitting,
  onClose,
  onSubmit,
}: {
  submitting: boolean;
  onClose: () => void;
  onSubmit: (input: StockV2RequestRunPortfolioSentinel) => void;
}) {
  const now = new Date();
  const startDefault = new Date(now.getTime() - 12 * 60 * 60 * 1000);
  const [portfolioId, setPortfolioId] = useState("");
  const [startAt, setStartAt] = useState(toDatetimeLocal(startDefault));
  const [endAt, setEndAt] = useState(toDatetimeLocal(now));
  const [note, setNote] = useState("");

  return (
    <Drawer title="手动触发组合哨兵" subtitle="窗口类型固定为 manual;未填时间范围默认取最近 12 小时。" onClose={onClose} width={460}>
      <div className="grid gap-3">
        <Field label="组合范围" help="留空表示扫描所有当前活跃组合。">
          <input type="text" value={portfolioId} placeholder="全部组合" onChange={(e) => setPortfolioId(e.target.value)} />
        </Field>
        <Field label="开始时间" help="本地时间,与结束时间一起决定消息面/数据面窗口。">
          <input type="datetime-local" value={startAt} onChange={(e) => setStartAt(e.target.value)} />
        </Field>
        <Field label="结束时间">
          <input type="datetime-local" value={endAt} onChange={(e) => setEndAt(e.target.value)} />
        </Field>
        <Field label="备注(可选)">
          <textarea className="min-h-16" value={note} placeholder="例如:复盘隔夜海外存储链条大跌" onChange={(e) => setNote(e.target.value)} />
        </Field>
        <p className="text-xs leading-relaxed text-[var(--muted)]">
          触发后创建一次运行,收集窗口内消息面与数据面,由 Agent 给出组合级风险结论;需要操作时 fan-out 到单票 OperationReview,等待人工确认。
        </p>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={onClose}>取消</Button>
          <Button
            tone="primary"
            disabled={submitting}
            onClick={() =>
              onSubmit({
                portfolioId: portfolioId.trim() || undefined,
                windowType: "manual",
                startAt: startAt || undefined,
                endAt: endAt || undefined,
                note: note.trim() || undefined,
              })
            }
          >
            {submitting ? "触发中…" : "触发"}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}

function SummaryCell({ label, value, tone }: { label: string; value: string; tone: "neutral" | "good" | "warn" | "danger" }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <span className="block text-xs text-[var(--muted)]">{label}</span>
      <Pill tone={tone} className="mt-1.5 text-sm">
        {value}
      </Pill>
    </div>
  );
}

function KeyValue({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-3">
      <span className="text-[var(--muted)]">{label}</span>
      <span className={`break-words ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function Pagination({
  loading,
  page,
  pageNumbers,
  pageSize,
  total,
  totalPages,
  onPage,
  label,
}: {
  loading: boolean;
  page: number;
  pageNumbers: Array<number | "ellipsis">;
  pageSize: number;
  total: number;
  totalPages: number;
  onPage: (page: number) => void;
  label: string;
}) {
  if (total <= pageSize) return null;
  const start = (page - 1) * pageSize + 1;
  const end = Math.min(total, page * pageSize);
  return (
    <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-3 text-xs">
      <span className="text-[var(--muted)]">
        第 {page} / {totalPages} 页 · {start}-{end} / {total}
      </span>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button disabled={loading || page <= 1} onClick={() => onPage(Math.max(1, page - 1))}>
          上一页
        </Button>
        {pageNumbers.map((item, index) =>
          item === "ellipsis" ? (
            <span className="px-2 text-[var(--muted)]" key={`${label}-gap-${index}`}>
              ...
            </span>
          ) : (
            <Button className={item === page ? "border-[var(--accent)] text-[var(--accent)]" : ""} disabled={loading} key={item} onClick={() => onPage(item)}>
              {item}
            </Button>
          ),
        )}
        <Button disabled={loading || page >= totalPages} onClick={() => onPage(Math.min(totalPages, page + 1))}>
          下一页
        </Button>
      </div>
    </div>
  );
}

// ===== report / 安全取值 helpers =====

interface SentinelReport {
  overallRiskLevel: string;
  runSummary: string;
  positiveItems: Array<Record<string, unknown>>;
  negativeItems: Array<Record<string, unknown>>;
  noiseItems: Array<Record<string, unknown>>;
  affectedHoldings: Array<Record<string, unknown>>;
  portfolioActions: Array<Record<string, unknown>>;
  reviewRequests: Array<Record<string, unknown>>;
  dataQualityNotes: string[];
  nextWatchFocus: string[];
}

function readReport(raw?: Record<string, unknown>): SentinelReport {
  const r = raw || {};
  return {
    overallRiskLevel: str(r["overall_risk_level"]),
    runSummary: str(r["run_summary"]),
    positiveItems: arr(r["positive_items"]),
    negativeItems: arr(r["negative_items"]),
    noiseItems: arr(r["noise_items"]),
    affectedHoldings: arr(r["affected_holdings"]),
    portfolioActions: arr(r["portfolio_actions"]),
    reviewRequests: arr(r["review_requests"]),
    dataQualityNotes: arr(r["data_quality_notes"]).map(str),
    nextWatchFocus: arr(r["next_watch_focus"]).map(str),
  };
}

function mapFromAny(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function arr(value: unknown): Array<Record<string, unknown>> {
  return Array.isArray(value) ? value.filter((v): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v)) : [];
}

function str(value: unknown): string {
  if (value === undefined || value === null || value === "") return "";
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}

function stringifyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function paginationWindow(page: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, idx) => idx + 1);
  }
  const pages = new Set<number>([1, totalPages, page, page - 1, page + 1]);
  if (page <= 3) {
    pages.add(2);
    pages.add(3);
    pages.add(4);
  }
  if (page >= totalPages - 2) {
    pages.add(totalPages - 1);
    pages.add(totalPages - 2);
    pages.add(totalPages - 3);
  }
  const sorted = Array.from(pages)
    .filter((item) => item >= 1 && item <= totalPages)
    .sort((a, b) => a - b);
  const result: Array<number | "ellipsis"> = [];
  sorted.forEach((item) => {
    const previous = result[result.length - 1];
    if (typeof previous === "number" && item - previous > 1) {
      result.push("ellipsis");
    }
    result.push(item);
  });
  return result;
}
