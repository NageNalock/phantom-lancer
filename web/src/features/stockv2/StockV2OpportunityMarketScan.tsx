import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowClockwise, GearSix, Play, Repeat } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2OpportunityMarketScanCandidate,
  StockV2OpportunityMarketScanRun,
  StockV2OpportunityMarketScanStatus,
  StockV2OpportunityCandidate,
  StockV2OpportunityEvidence,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, EmptyState, Notice, Panel, Pill, Toggle } from "../../components/ui";
import { formatMeaningfulDateTime } from "./time";
import { ModelHorizonOutlookCompact, ModelHorizonOutlookPanel } from "./StockV2ModelOutlook";

const ACTIVE_STATUSES = new Set(["pending", "prefiltering", "enriching", "researching", "drafting"]);
const STAGES = ["prefiltering", "enriching", "researching", "drafting"] as const;
type ResultStage = "final" | "research_candidate" | "reviewed_out" | "excluded";
type DecisionHealthFilter = "all" | "healthy" | "degraded" | "blocked";
type CandidateSourceFilter = "all" | "sector_related" | "message_related" | "price";

export function StockV2OpportunityMarketScan({ actions }: { actions: AppActions }) {
  const [status, setStatus] = useState<StockV2OpportunityMarketScanStatus | null>(null);
  const [runs, setRuns] = useState<StockV2OpportunityMarketScanRun[]>([]);
  const [selectedRunId, setSelectedRunId] = useState<string>("");
  const [candidates, setCandidates] = useState<StockV2OpportunityMarketScanCandidate[]>([]);
  const [candidateTotal, setCandidateTotal] = useState(0);
  const [resultStage, setResultStage] = useState<ResultStage>("final");
  const [candidateOffset, setCandidateOffset] = useState(0);
  const [decisionHealth, setDecisionHealth] = useState<DecisionHealthFilter>("all");
  const [candidateSource, setCandidateSource] = useState<CandidateSourceFilter>("all");
  const [selectedCandidate, setSelectedCandidate] = useState<StockV2OpportunityMarketScanCandidate | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [runningAction, setRunningAction] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const candidateRequest = useRef(0);

  async function load() {
    try {
      const [nextStatus, history] = await Promise.all([
        actions.api<StockV2OpportunityMarketScanStatus>("/api/stockv2/opportunity-market-scan/config"),
        actions.api<{ items: StockV2OpportunityMarketScanRun[] }>("/api/stockv2/opportunity-market-scan/runs?limit=30"),
      ]);
      setStatus(nextStatus);
      setRuns(history.items || []);
      setSelectedRunId((current) => current || nextStatus.activeRun?.id || history.items?.[0]?.id || "");
      setError(null);
    } catch (cause) {
      setError(friendlyError(cause));
    } finally {
      setLoading(false);
    }
  }

  async function loadCandidates(runId: string, stage = resultStage, offset = candidateOffset, health = decisionHealth, source = candidateSource) {
    if (!runId) { setCandidates([]); return; }
    const requestID = ++candidateRequest.current;
    try {
      const limit = stage === "excluded" ? 50 : 50;
      const result = await actions.api<{ items: StockV2OpportunityMarketScanCandidate[]; total: number }>(
        `/api/stockv2/opportunity-market-scan/runs/${encodeURIComponent(runId)}/candidates?stage=${encodeURIComponent(stage)}&decisionStatus=${health === "all" ? "" : encodeURIComponent(health)}&sourceLane=${source === "all" ? "" : encodeURIComponent(source)}&limit=${limit}&offset=${offset}`,
      );
      if (requestID === candidateRequest.current) {
        setCandidates(result.items || []);
        setCandidateTotal(result.total || 0);
      }
    } catch (cause) {
      setError(friendlyError(cause));
    }
  }

  useEffect(() => { void load(); }, []);
  useEffect(() => {
    setCandidates([]);
    setSelectedCandidate(null);
    const run = runs.find((item) => item.id === selectedRunId) || status?.activeRun;
    const nextStage: ResultStage = run && ACTIVE_STATUSES.has(run.status) ? "research_candidate" : "final";
    setResultStage(nextStage);
    setCandidateOffset(0);
    void loadCandidates(selectedRunId, nextStage, 0);
  }, [selectedRunId]);
  useEffect(() => { void loadCandidates(selectedRunId, resultStage, candidateOffset, decisionHealth, candidateSource); }, [resultStage, candidateOffset, decisionHealth, candidateSource]);
  useEffect(() => {
    if (!status?.activeRun) return;
    const timer = window.setInterval(() => { void load(); void loadCandidates(selectedRunId, resultStage, candidateOffset, decisionHealth, candidateSource); }, 15_000);
    return () => window.clearInterval(timer);
  }, [candidateOffset, candidateSource, decisionHealth, resultStage, selectedRunId, status?.activeRun?.id]);

  const selectedRun = useMemo(
    () => runs.find((item) => item.id === selectedRunId) || (status?.activeRun?.id === selectedRunId ? status.activeRun : undefined),
    [runs, selectedRunId, status?.activeRun],
  );

  async function runNow() {
    setRunningAction(true);
    try {
      const run = await actions.api<StockV2OpportunityMarketScanRun>("/api/stockv2/opportunity-market-scan/runs", { method: "POST" });
      setSelectedRunId(run.id);
      actions.setToast("市场扫描已启动", "good");
      await load();
    } catch (cause) {
      actions.setToast(friendlyError(cause), "danger");
    } finally { setRunningAction(false); }
  }

  async function retry(run: StockV2OpportunityMarketScanRun) {
    setRunningAction(true);
    try {
      await actions.api(`/api/stockv2/opportunity-market-scan/runs/${encodeURIComponent(run.id)}/retry`, { method: "POST" });
      actions.setToast("扫描已重新启动", "good");
      await load();
    } catch (cause) { actions.setToast(friendlyError(cause), "danger"); }
    finally { setRunningAction(false); }
  }

  if (loading && !status) return <Panel title="市场扫描"><p className="muted text-sm">正在读取扫描状态…</p></Panel>;

  return (
    <div className="grid gap-4">
      {error ? <Notice tone="danger">{error}</Notice> : null}
      <Panel
        title="A 股主板机会扫描"
        subtitle="全市场确定性预筛 → 有界数据富集 → Agent 证据复核 → 最多 10 份未激活建仓草案"
        actions={<>
          <Button onClick={() => setSettingsOpen(true)}><GearSix size={14} />配置</Button>
          <Button onClick={() => void load()}><ArrowClockwise size={14} />刷新</Button>
          <Button disabled={runningAction || !!status?.activeRun || !status?.ready} onClick={() => void runNow()} tone="primary">
            <Play size={14} />立即扫描
          </Button>
        </>}
      >
        <div className="grid grid-cols-4 gap-px overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--line)] max-xl:grid-cols-2">
          <ScanMetric label="最新交易日" value={status?.latestDataTradeDate || "-"} />
          <ScanMetric label="本地覆盖" value={`${status?.coveredCount || 0} / ${status?.universeCount || 0}`} detail={`${((status?.coverageRatio || 0) * 100).toFixed(1)}%`} />
          <ScanMetric label="自动扫描" value={status?.config.enabled ? "已启用" : "未启用"} detail={status?.scheduleDescription} />
          <ScanMetric label="当前状态" value={scanStatusLabel(status?.activeRun?.status || status?.latestRun?.status)} detail={status?.activeRun ? `重试 ${status.activeRun.retryCount}/${status.maxRetries}` : "无运行中任务"} />
        </div>
        {!status?.ready && status?.blockedReason ? <div className="mt-3"><Notice>{status.blockedReason}</Notice></div> : null}
        {status?.activeRun ? <div className="mt-4"><RunProgress run={status.activeRun} /></div> : null}
        {status?.activeRun?.errorMessage ? <div className="mt-3"><Notice>{status.activeRun.errorMessage}{status.activeRun.nextRetryAt ? `；下次尝试 ${formatMeaningfulDateTime(status.activeRun.nextRetryAt)}` : ""}</Notice></div> : null}
      </Panel>

      <div className="grid grid-cols-[280px_minmax(0,1fr)] gap-4 max-xl:grid-cols-1">
        <Panel title="扫描记录" subtitle="失败与部分成功会保留，可手动重试">
          <div className="grid gap-1">
            {runs.length === 0 ? <EmptyState title="暂无扫描记录" body="数据准备就绪后可手动运行；自动扫描默认关闭。" /> : runs.map((run) => (
              <button
                className={`rounded-md border px-3 py-2 text-left ${run.id === selectedRunId ? "border-[var(--accent)] bg-[var(--surface-strong)]" : "border-[var(--line)] hover:bg-[var(--surface-soft)]"}`}
                key={run.id} onClick={() => setSelectedRunId(run.id)} type="button"
              >
                <span className="flex items-center justify-between gap-2 text-xs"><strong>{run.tradeDate || "待确定"}</strong><Pill tone={scanStatusTone(run.status)}>{scanStatusLabel(run.status)}</Pill></span>
                <span className="muted mt-1 block text-xs">{scanTriggerLabel(run.triggerType)} · {formatMeaningfulDateTime(run.createdAt)}</span>
              </button>
            ))}
          </div>
        </Panel>

        <Panel
          title={selectedRun ? `${selectedRun.tradeDate || "扫描"} · 扫描结果` : "扫描结果"}
          subtitle={selectedRun ? `预筛 ${selectedRun.prefilterCount} · 板块 ${selectedRun.sectorSnapshot?.trackedSectorCount || 0} · 消息候选 ${selectedRun.themeSnapshot?.messageCandidateCount || 0} · 复核 ${selectedRun.researchCount} · 最终 ${selectedRun.finalCandidateCount} · 草案 ${selectedRun.strategyCreatedCount}` : "选择一条扫描记录"}
          actions={selectedRun && (selectedRun.status === "failed" || selectedRun.status === "partial") ? <Button disabled={runningAction || !!status?.activeRun} onClick={() => void retry(selectedRun)}><Repeat size={14} />重试</Button> : undefined}
        >
          {selectedRun?.errorMessage ? <div className="mb-3"><Notice tone={selectedRun.status === "failed" ? "danger" : "warn"}>{selectedRun.errorMessage}</Notice></div> : null}
          {selectedRun ? <SectorTrendSnapshot run={selectedRun} /> : null}
          {selectedRun ? <ResultTabs run={selectedRun} stage={resultStage} onChange={(next) => { setResultStage(next); setCandidateOffset(0); }} /> : null}
          {selectedRun ? <div className="my-3 flex flex-wrap items-center justify-between gap-3 text-xs"><span className="muted">板块状态决定研究优先级，确定性四道门仍会阻断新增风险。</span><div className="flex flex-wrap items-center gap-3"><label className="flex items-center gap-2"><span className="muted">候选来源</span><select aria-label="按候选来源过滤" className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 outline-none focus:border-[var(--accent)]" onChange={(event) => { setCandidateSource(event.target.value as CandidateSourceFilter); setCandidateOffset(0); }} value={candidateSource}><option value="all">全部</option><option value="sector_related">板块轮动</option><option value="message_related">消息相关</option><option value="price">仅行情</option></select></label><label className="flex items-center gap-2"><span className="muted">数据健康</span><select aria-label="按数据健康过滤" className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 outline-none focus:border-[var(--accent)]" onChange={(event) => { setDecisionHealth(event.target.value as DecisionHealthFilter); setCandidateOffset(0); }} value={decisionHealth}><option value="all">全部</option><option value="healthy">健康</option><option value="degraded">降级</option><option value="blocked">阻断</option></select></label></div></div> : null}
          {selectedRun?.fundFlowRequestedCount ? <div className="mb-3 mt-3 flex flex-wrap items-center gap-2 text-xs"><span className="muted">主动资金证据</span><Pill tone={selectedRun.fundFlowUsed ? "good" : "warn"}>{fundFlowRunLabel(selectedRun)}</Pill>{selectedRun.fundFlowSource ? <span className="font-mono">{selectedRun.fundFlowSource}</span> : null}</div> : null}
          {selectedRun?.themeSnapshot?.versionCount ? <div className="mb-3 flex flex-wrap items-center gap-2 text-xs"><span className="muted">消息主题召回</span><Pill tone={decisionHealthTone(selectedRun.themeSnapshot.status)}>{selectedRun.themeSnapshot.versionCount} 个实质变化主题</Pill><span className="muted">命中 {selectedRun.themeSnapshot.matchedCandidateCount}，入池 {selectedRun.themeSnapshot.messageCandidateCount}</span>{selectedRun.themeSnapshot.status === "degraded" ? <span className="text-[var(--warn)]">{selectedRun.themeSnapshot.message}</span> : null}</div> : null}
          {candidates.length === 0 ? <EmptyState title="当前分组暂无记录" body={selectedRun && ACTIVE_STATUSES.has(selectedRun.status) ? "扫描仍在执行，研究池会随阶段持续落盘。" : "本轮在这个结果分组中没有记录。"} /> : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1160px] table-fixed text-left text-xs">
                <thead className="text-[var(--muted)]"><tr className="border-b border-[var(--line)]"><th className="w-12 px-2 py-2">排名</th><th className="w-44 px-2 py-2">标的</th><th className="w-24 px-2 py-2">行业</th><th className="w-14 px-2 py-2">综合</th><th className="w-14 px-2 py-2">20日</th><th className="w-36 px-2 py-2">5 日模型预期</th><th className="w-24 px-2 py-2">主动资金证据</th><th className="w-28 px-2 py-2">来源 / 主题</th><th className="w-24 px-2 py-2">四道门</th><th className="px-2 py-2">筛选结论</th><th className="w-20 px-2 py-2">策略</th></tr></thead>
                <tbody>{candidates.map((candidate) => <CandidateRow candidate={candidate} key={candidate.id} onSelect={() => setSelectedCandidate(candidate)} />)}</tbody>
              </table>
            </div>
          )}
          {candidateTotal > 50 ? <div className="mt-3 flex items-center justify-end gap-2 text-xs"><span className="muted">{candidateOffset + 1}-{Math.min(candidateOffset + 50, candidateTotal)} / {candidateTotal}</span><Button disabled={candidateOffset === 0} onClick={() => setCandidateOffset(Math.max(0, candidateOffset - 50))}>上一页</Button><Button disabled={candidateOffset + 50 >= candidateTotal} onClick={() => setCandidateOffset(candidateOffset + 50)}>下一页</Button></div> : null}
        </Panel>
      </div>

      {settingsOpen && status ? <MarketScanSettings actions={actions} status={status} onClose={() => setSettingsOpen(false)} onSaved={load} /> : null}
      {selectedCandidate ? <CandidateDrawer actions={actions} candidate={selectedCandidate} discoveryRunId={selectedRun?.discoveryRunId} onClose={() => setSelectedCandidate(null)} /> : null}
    </div>
  );
}

function SectorTrendSnapshot({ run }: { run: StockV2OpportunityMarketScanRun }) {
  const snapshot = run.sectorSnapshot;
  if (!snapshot?.capturedAt && !snapshot?.eligibleCount) {
    return <div className="mb-3"><Notice>这条历史记录生成时尚未启用板块轮动检测。</Notice></div>;
  }
  const trends = snapshot.trends || [];
  return <CollapsibleSection
    title={`板块轮动 · ${trends.length} 个状态`}
    subtitle={`分类覆盖 ${snapshot.classifiedCount}/${snapshot.eligibleCount} (${(snapshot.coverageRatio * 100).toFixed(1)}%)；首次发现与连续天数按历史扫描快照延续`}
  >
    {snapshot.status === "blocked" ? <Notice tone="danger">{snapshot.message || "行业分类覆盖不足，本轮不可生成板块结论。"}</Notice> : <>
      {snapshot.status === "degraded" ? <div className="mb-3"><Notice>{snapshot.message || "部分标的缺少行业分类，本轮板块状态已降级。"}</Notice></div> : null}
      {trends.length === 0 ? <p className="muted m-0 text-xs">本轮没有达到涌现、确认、过热、退潮或失效阈值的板块。</p> : (
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] table-fixed text-left text-xs">
          <thead className="text-[var(--muted)]"><tr className="border-b border-[var(--line)]"><th className="w-36 px-2 py-2">板块</th><th className="w-20 px-2 py-2">状态</th><th className="w-28 px-2 py-2">首次 / 连续</th><th className="w-24 px-2 py-2">站上 MA20</th><th className="w-20 px-2 py-2">3日扩散</th><th className="w-24 px-2 py-2">5日中位</th><th className="w-20 px-2 py-2">放量占比</th><th className="px-2 py-2">代表股</th></tr></thead>
          <tbody>{trends.map((trend) => <tr className="border-b border-[var(--line)] last:border-0" key={trend.key}><td className="px-2 py-2"><strong>{trend.name}</strong><span className="muted ml-2 font-mono">{trend.score.toFixed(0)}</span></td><td className="px-2 py-2"><Pill tone={sectorStateTone(trend.state)}>{sectorStateLabel(trend.state)}</Pill></td><td className="px-2 py-2 font-mono">{trend.firstSeenTradeDate || "-"}<span className="muted ml-2">{trend.streak} 日</span></td><td className="px-2 py-2 font-mono">{formatRatioPct(trend.aboveMa20Ratio)}<span className="muted ml-1">{trend.memberCount}只</span></td><td className={`px-2 py-2 font-mono ${trend.aboveMa20Delta3 < 0 ? "text-[var(--danger)]" : ""}`}>{formatRatioPct(trend.aboveMa20Delta3)}</td><td className="px-2 py-2 font-mono">{formatPct(trend.medianReturn5Pct)}</td><td className="px-2 py-2 font-mono">{formatRatioPct(trend.volumeExpansionRatio)}</td><td className="px-2 py-2"><span className="block truncate" title={(trend.representativeNames || []).join("、")}>{(trend.representativeNames || trend.representativeSymbols || []).join("、") || "-"}</span></td></tr>)}</tbody>
        </table>
      </div>
      )}
    </>}
  </CollapsibleSection>;
}

function ScanMetric({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return <div className="min-h-20 bg-[var(--surface)] p-3"><span className="muted block text-xs">{label}</span><strong className="mt-1 block font-mono text-sm">{value}</strong>{detail ? <span className="muted mt-1 block text-xs">{detail}</span> : null}</div>;
}

function RunProgress({ run }: { run: StockV2OpportunityMarketScanRun }) {
  const current = run.status === "completed" ? STAGES.length : run.status === "partial" ? STAGES.length - 1 : STAGES.indexOf(run.status as typeof STAGES[number]);
  return <div className="grid grid-cols-4 gap-2">{STAGES.map((stage, index) => <div className={`rounded-md border px-3 py-2 text-xs ${index < current ? "border-[rgba(18,132,79,.25)] bg-[var(--good-soft)]" : index === current ? "border-[var(--accent)] bg-[var(--surface-strong)]" : "border-[var(--line)] text-[var(--muted)]"}`} key={stage}><span className="font-mono">0{index + 1}</span><span className="ml-2">{scanStatusLabel(stage)}</span></div>)}</div>;
}

function ResultTabs({ run, stage, onChange }: { run: StockV2OpportunityMarketScanRun; stage: ResultStage; onChange: (stage: ResultStage) => void }) {
  const terminal = !ACTIVE_STATUSES.has(run.status);
  const tabs: Array<{ stage: ResultStage; label: string; count: number }> = terminal
    ? [
      { stage: "final", label: "最终入选", count: run.finalCandidateCount },
      { stage: "reviewed_out", label: "复核未入选", count: Math.max(0, run.researchCount - run.finalCandidateCount) },
      { stage: "excluded", label: "预筛排除", count: Math.max(0, run.prefilterCount - run.researchCount) },
    ]
    : [{ stage: "research_candidate", label: "当前研究池", count: run.researchCount }];
  return <div className="flex gap-1 overflow-x-auto border-b border-[var(--line)]" role="tablist" aria-label="扫描结果分组">{tabs.map((tab) => <button aria-selected={stage === tab.stage} className={`border-b-2 px-3 py-2 text-xs ${stage === tab.stage ? "border-[var(--accent)] text-[var(--text)]" : "border-transparent text-[var(--muted)] hover:text-[var(--text)]"}`} key={tab.stage} onClick={() => onChange(tab.stage)} role="tab" type="button">{tab.label} <span className="ml-1 font-mono">{tab.count}</span></button>)}</div>;
}

function CandidateRow({ candidate, onSelect }: { candidate: StockV2OpportunityMarketScanCandidate; onSelect: () => void }) {
  return <tr className="border-b border-[var(--line)] hover:bg-[var(--surface-soft)]">
    <td className="px-2 py-2 font-mono">{candidate.finalRank || candidate.prefilterRank || "-"}</td>
    <td className="px-2 py-2"><button className="text-left hover:text-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)]" onClick={onSelect} type="button"><strong>{candidate.name}</strong><span className="muted ml-2 font-mono">{candidate.symbol}</span><span className="mt-1 block"><Pill tone="neutral">{candidateStageLabel(candidate.stage)}</Pill></span></button></td>
    <td className="px-2 py-2"><span className="block truncate" title={candidate.industry || undefined}>{candidate.industry || "-"}</span></td>
    <td className="px-2 py-2 font-mono">{candidate.finalScore.toFixed(1)}</td>
    <td className="px-2 py-2 font-mono">{formatPct(candidate.metrics.return20Pct)}</td>
    <td className="px-2 py-2"><ModelHorizonOutlookCompact items={candidate.horizonOutlooks} /></td>
    <td className="px-2 py-2"><span className="font-mono">{candidate.metrics.fundFlowUsed ? candidate.flowScore.toFixed(0) : fundFlowCandidateLabel(candidate)}</span>{candidate.metrics.fundFlowSource ? <span className="muted mt-1 block text-[10px]">{candidate.metrics.fundFlowSource}</span> : null}</td>
    <td className="px-2 py-2"><span className="font-mono">{candidate.themeScore.toFixed(0)}</span><span className="mt-1 block"><Pill tone={candidate.metrics.sectorSignals?.some((item) => item.state === "emerging") ? "good" : candidate.metrics.themeMatches?.some((item) => item.requiresCausalVerification) ? "warn" : "neutral"}>{candidateSourceLaneLabel(candidate.metrics.sourceLane)}</Pill></span>{candidate.metrics.sectorSignals?.[0] ? <span className="muted mt-1 block truncate text-[10px]" title={candidate.metrics.sectorSignals.map((item) => `${item.name} ${sectorStateLabel(item.state)}`).join("、")}>{candidate.metrics.sectorSignals[0].name} · {sectorStateLabel(candidate.metrics.sectorSignals[0].state)}</span> : null}</td>
    <td className="px-2 py-2"><Pill tone={decisionHealthTone(candidate.metrics.decisionStatus)}>{decisionHealthLabel(candidate.metrics.decisionStatus)}</Pill><span className="muted mt-1 block font-mono text-[10px]">{decisionGateCount(candidate)} / 4</span></td>
    <td className="px-2 py-2"><span className="muted block truncate text-[11px]" title={candidate.decisionReason || candidate.exclusionReason || "等待后续筛选"}>{candidate.decisionReason || candidate.exclusionReason || "等待后续筛选"}</span></td>
    <td className="px-2 py-2"><Pill tone={candidate.strategyStatus === "generated" ? "good" : "neutral"}>{strategyStatusLabel(candidate.strategyStatus)}</Pill></td>
  </tr>;
}

function MarketScanSettings({ actions, status, onClose, onSaved }: { actions: AppActions; status: StockV2OpportunityMarketScanStatus; onClose: () => void; onSaved: () => Promise<void> }) {
  const [enabled, setEnabled] = useState(status.config.enabled);
  const [primaryKey, setPrimaryKey] = useState("");
  const [backupKey, setBackupKey] = useState("");
  const [backupProxy, setBackupProxy] = useState("");
  const [clearPrimary, setClearPrimary] = useState(false);
  const [clearBackup, setClearBackup] = useState(false);
  const [clearProxy, setClearProxy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [probing, setProbing] = useState(false);
  async function save() {
    setSaving(true);
    try {
      await actions.api("/api/stockv2/opportunity-market-scan/config", { method: "PATCH", body: { enabled, primaryFundFlowApiKey: primaryKey, backupFundFlowApiKey: backupKey, backupFundFlowProxy: backupProxy, clearPrimaryFundFlowApiKey: clearPrimary, clearBackupFundFlowApiKey: clearBackup, clearBackupFundFlowProxy: clearProxy } });
      actions.setToast("市场扫描配置已保存", "good"); await onSaved(); onClose();
    } catch (cause) { actions.setToast(friendlyError(cause), "danger"); } finally { setSaving(false); }
  }
  async function probe() {
    setProbing(true);
    try {
      const result = await actions.api<{ ok: boolean; status: string; sources: Record<string, { status: string; source?: string; count?: number; error?: string }> }>("/api/stockv2/opportunity-market-scan/decision-data/probe", { method: "POST" });
      const unhealthy = Object.entries(result.sources).filter(([, item]) => item.status !== "healthy").map(([key]) => sourceProbeLabel(key));
      const message = result.status === "healthy" ? "资金流、基准、交易日历、公司事件与财务事实源均可用" : `${result.status === "blocked" ? "关键数据源不可用" : "可选数据源降级"}：${unhealthy.join("、") || "未知来源"}`;
      actions.setToast(message, result.status === "healthy" ? "good" : result.status === "blocked" ? "danger" : "warn");
    } catch (cause) { actions.setToast(friendlyError(cause), "danger"); } finally { setProbing(false); }
  }
  return <Drawer title="市场扫描配置" subtitle="自动扫描默认关闭；手动扫描不受此开关影响" onClose={onClose} width={520}>
    <div className="grid gap-4">
      <Toggle checked={enabled} label={<span><strong className="block">每日自动扫描</strong><span className="muted text-xs">全市场数据维护成功后，每个新交易日最多启动一次</span></span>} onChange={setEnabled} />
      <CollapsibleSection title="Tushare 决策数据源" subtitle="用于主动资金、市场基准、交易日历、公司事件与财务事实；与账户可用资金无关">
        <div className="grid gap-3 text-xs">
          <SecretField configured={status.config.primaryFundFlowConfigured} label="DataHubCo 主源 API Key" value={primaryKey} onChange={setPrimaryKey} clear={clearPrimary} onClear={setClearPrimary} />
          <SecretField configured={status.config.backupFundFlowConfigured} label="Indevs 备源 API Key" value={backupKey} onChange={setBackupKey} clear={clearBackup} onClear={setClearBackup} />
          <label><span className="mb-1 block font-medium">备源独立代理 URL</span><input className="w-full rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 font-mono outline-none focus:border-[var(--accent)]" onChange={(event) => setBackupProxy(event.target.value)} placeholder={status.config.backupFundFlowProxyConfigured ? "已配置，留空保持不变" : "可选，仅支持 http/https"} type="password" value={backupProxy} /></label>
          <Toggle checked={clearProxy} label={<span>清除已保存的备源代理</span>} onChange={setClearProxy} />
          <div className="flex justify-end"><Button disabled={probing} onClick={() => void probe()}>{probing ? "检测中" : "检测全部决策数据源"}</Button></div>
        </div>
      </CollapsibleSection>
      <CollapsibleSection title="固定预算与模型建议" subtitle="预算是资源保护边界，不在运行时开放调节">
        <dl className="grid grid-cols-2 gap-3 text-xs">{Object.entries(status.budgets).map(([key, value]) => <div key={key}><dt className="muted">{budgetLabel(key)}</dt><dd className="m-0 mt-1 font-mono">{value}</dd></div>)}</dl>
        <p className="muted mt-4 text-xs">机会发现 Agent 建议绑定：{status.recommendedModel}。页面不会覆盖 Agent 绑定中已有的模型与推理强度。</p>
      </CollapsibleSection>
      <div className="flex justify-end gap-2"><Button onClick={onClose}>取消</Button><Button disabled={saving} onClick={() => void save()} tone="primary">{saving ? "保存中" : "保存"}</Button></div>
    </div>
  </Drawer>;
}

function SecretField({ label, configured, value, onChange, clear, onClear }: { label: string; configured: boolean; value: string; onChange: (value: string) => void; clear: boolean; onClear: (value: boolean) => void }) {
  return <div className="grid gap-2"><label><span className="mb-1 block font-medium">{label}</span><input autoComplete="off" className="w-full rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 font-mono outline-none focus:border-[var(--accent)]" onChange={(event) => onChange(event.target.value)} placeholder={configured ? "已配置，留空保持不变" : "尚未配置"} type="password" value={value} /></label><Toggle checked={clear} label={<span>清除已保存的 {label}</span>} onChange={onClear} /></div>;
}

function CandidateDrawer({ actions, candidate, discoveryRunId, onClose }: { actions: AppActions; candidate: StockV2OpportunityMarketScanCandidate; discoveryRunId?: string; onClose: () => void }) {
  const metrics = candidate.metrics;
  const [review, setReview] = useState<StockV2OpportunityCandidate | null>(null);
  const [evidence, setEvidence] = useState<StockV2OpportunityEvidence[]>([]);
  useEffect(() => {
    if (!discoveryRunId || !candidate.opportunityCandidateId) return;
    const runId = encodeURIComponent(discoveryRunId);
    const candidateId = encodeURIComponent(candidate.opportunityCandidateId);
    void Promise.all([
      actions.api<{ items: StockV2OpportunityCandidate[] }>(`/api/stockv2/opportunity-discovery-runs/${runId}/candidates?symbol=${encodeURIComponent(candidate.symbol)}&limit=10`),
      actions.api<{ items: StockV2OpportunityEvidence[] }>(`/api/stockv2/opportunity-discovery-runs/${runId}/evidence?candidateId=${candidateId}&limit=100`),
    ]).then(([candidateResult, evidenceResult]) => {
      setReview(candidateResult.items.find((item) => item.id === candidate.opportunityCandidateId) || null);
      setEvidence(evidenceResult.items || []);
    }).catch(() => { setReview(null); setEvidence([]); });
  }, [actions, candidate.opportunityCandidateId, candidate.symbol, discoveryRunId]);
  return <Drawer title={`${candidate.name} · ${candidate.symbol}`} subtitle={`综合排名 ${candidate.finalRank || candidate.prefilterRank || "-"} · ${candidate.industry || "行业未标注"}`} onClose={onClose} width={620}>
    <div className="grid gap-4 text-sm">
      {metrics.decisionStatus === "blocked" ? <Notice tone="danger">关键数据或确定性门未通过：不会生成建仓/加仓动作。减仓与退出风险控制仍可继续。</Notice> : metrics.decisionStatus === "degraded" ? <Notice>部分可选数据缺失，结论已降级并在下方逐项标注。</Notice> : null}
      <div className="grid grid-cols-3 gap-2"><ScanMetric label="综合评分" value={candidate.finalScore.toFixed(1)} /><ScanMetric label="主动资金证据" value={metrics.fundFlowUsed ? candidate.flowScore.toFixed(1) : fundFlowCandidateLabel(candidate)} /><ScanMetric label="主题评分" value={candidate.themeScore.toFixed(1)} /></div>
      <ModelHorizonOutlookPanel items={candidate.horizonOutlooks || review?.horizonOutlooks} />
      <Panel title="行情与质量"><dl className="grid grid-cols-2 gap-3 text-xs"><MetricItem label="最新价" value={metrics.latestPrice?.toFixed(3) || "-"} /><MetricItem label="当日涨跌" value={formatPct(metrics.latestPctChange)} /><MetricItem label="5 日收益" value={formatPct(metrics.return5Pct)} /><MetricItem label="20 日收益" value={formatPct(metrics.return20Pct)} /><MetricItem label="ATR(14)" value={metrics.atr14 ? `${metrics.atr14.toFixed(3)} / ${metrics.atr14Pct?.toFixed(2)}%` : "-"} /><MetricItem label="市场状态" value={marketRegimeLabel(metrics.marketRegime)} /><MetricItem label="量比 5/20" value={metrics.volumeRatio5To20?.toFixed(2) || "-"} /><MetricItem label="主动资金证据" value={metrics.fundFlowAvailable ? `20 日净额占主动成交 ${metrics.mainFlowRatio20?.toFixed(2)}% · ${metrics.fundFlowAsOf || "日期未知"}` : fundFlowCandidateLabel(candidate)} /></dl></Panel>
      {metrics.sectorSignals?.length ? <Panel title="板块轮动准入"><div className="grid gap-2">{metrics.sectorSignals.map((signal) => <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] pb-2 text-xs last:border-0 last:pb-0" key={signal.key}><div><strong>{signal.name}</strong><span className="muted mt-1 block">首次 {signal.firstSeenTradeDate || "-"} · 连续 {signal.streak} 日 · 状态分 {signal.score.toFixed(0)}</span></div><Pill tone={sectorStateTone(signal.state)}>{sectorStateLabel(signal.state)}</Pill></div>)}</div></Panel> : null}
      <Panel title="确定性四道门">{metrics.decisionGates?.length ? <div className="grid gap-2">{metrics.decisionGates.map((gate) => <div className="rounded-md border border-[var(--line)] p-3" key={gate.key}><div className="flex items-center justify-between gap-3"><strong className="text-xs">{gate.label}</strong><Pill tone={decisionGateTone(gate.status)}>{decisionGateStatusLabel(gate.status)}</Pill></div><p className="muted mt-1 mb-0 text-xs">{gate.summary}</p>{gate.reasons?.length ? <ul className="mt-2 mb-0 pl-5 text-xs">{gate.reasons.map((reason) => <li key={reason}>{reason}</li>)}</ul> : null}</div>)}</div> : <p className="muted m-0 text-xs">这条历史记录生成时尚未运行确定性四道门。</p>}</Panel>
      <CollapsibleSection title="数据健康" subtitle="关键项缺失会阻断新增风险，可选项缺失只降级">{metrics.dataHealth?.length ? <div className="grid gap-2">{metrics.dataHealth.map((item) => <div className="flex items-start justify-between gap-4 border-b border-[var(--line)] pb-2 text-xs last:border-0 last:pb-0" key={item.key}><div><strong>{item.label}</strong><span className="muted mt-1 block">{item.message || item.asOf || "已检查"}{item.source ? ` · ${item.source}` : ""}</span></div><Pill tone={decisionHealthTone(item.status)}>{decisionHealthLabel(item.status)}</Pill></div>)}</div> : <p className="muted m-0 text-xs">无健康快照。</p>}</CollapsibleSection>
      {metrics.themeMatches?.length ? <Panel title="消息驱动来源"><div className="grid gap-3">{metrics.themeMatches.map((match) => <div className="border-b border-[var(--line)] pb-3 text-xs last:border-0 last:pb-0" key={match.versionId}><div className="flex items-start justify-between gap-3"><div><strong>{match.title}</strong><span className="muted mt-1 block">{formatMeaningfulDateTime(match.effectiveAt)} · 主题置信度 {((match.confidence || 0) * 100).toFixed(0)}%</span></div><Pill tone={match.requiresCausalVerification ? "warn" : "good"}>{themeMatchKindLabel(match.matchKind)}</Pill></div>{match.matchedTerms?.length ? <span className="muted mt-2 block">命中：{match.matchedTerms.join("、")}</span> : null}<code className="muted mt-2 block break-all">{match.threadId} / {match.versionId}</code>{match.requiresCausalVerification ? <p className="mt-2 mb-0 text-[var(--warn)]">仅获得研究席位，Agent 必须核验业务暴露、传导路径和定价状态。</p> : null}</div>)}</div></Panel> : null}
      {metrics.decisionOutcomes?.length ? <CollapsibleSection title="事后验证" subtitle="按 1/3/5/10/20 个交易日持续记录，不回写历史结论"><dl className="grid grid-cols-5 gap-2 text-xs">{metrics.decisionOutcomes.map((item) => <MetricItem key={item.horizon} label={`${item.horizon} 日`} value={item.status === "pending" ? "待观察" : item.status === "observed" ? `${formatPct(item.returnPct)} / 超额 ${formatPct(item.excessReturnPct)}` : `${formatPct(item.returnPct)} / 基准缺失`} />)}</dl></CollapsibleSection> : null}
      <Panel title="消息脉络信号">{metrics.themeSignals?.length ? <ul className="m-0 grid gap-2 pl-5 text-xs">{metrics.themeSignals.map((item) => <li key={item}>{item}</li>)}</ul> : <p className="muted m-0 text-xs">未匹配到活跃消息脉络，主题评分不加分。</p>}</Panel>
      {review ? <Panel title="Agent 复核"><dl className="grid grid-cols-2 gap-3 text-xs"><MetricItem label="证据评分" value={review.evidenceScore?.toFixed(1) || "-"} /><MetricItem label="置信度" value={review.confidence?.toFixed(2) || "-"} /></dl><p className="mt-3 mb-0 text-xs">{review.reason || "未提供候选理由"}</p>{review.riskSummary ? <p className="muted mt-2 mb-0 text-xs">风险：{review.riskSummary}</p> : null}</Panel> : null}
      {evidence.length ? <CollapsibleSection title="证据记录" subtitle={`${evidence.length} 条内部与公开资料留痕`}><ul className="m-0 grid gap-3 pl-5 text-xs">{evidence.map((item) => <li key={item.id}><strong>{item.title || item.sourceType}</strong>{item.summary ? <span className="muted mt-1 block">{item.summary}</span> : null}</li>)}</ul></CollapsibleSection> : null}
      {candidate.strategyId ? <Panel title="策略草案"><p className="m-0 text-xs">已生成未激活策略，可在“策略”工作台复核后再启用。</p><code className="muted mt-2 block text-xs">{candidate.strategyId}</code></Panel> : null}
      {candidate.decisionReason || candidate.exclusionReason ? <Notice>{candidate.decisionReason || candidate.exclusionReason}</Notice> : null}
    </div>
  </Drawer>;
}

function MetricItem({ label, value }: { label: string; value: string }) { return <div><dt className="muted">{label}</dt><dd className="m-0 mt-1 font-mono">{value}</dd></div>; }
function formatPct(value?: number) { return value === undefined ? "-" : `${value > 0 ? "+" : ""}${value.toFixed(2)}%`; }
function formatRatioPct(value?: number) { return value === undefined ? "-" : `${value > 0 ? "+" : ""}${(value * 100).toFixed(0)}%`; }
function scanStatusLabel(status?: string) { return ({ pending: "等待", prefiltering: "本地预筛", enriching: "数据富集", researching: "证据复核", drafting: "策略草案", completed: "完成", partial: "部分完成", failed: "失败" } as Record<string, string>)[status || ""] || "未运行"; }
function scanTriggerLabel(trigger?: string) { return ({ scheduled: "自动", manual: "手动", theme_refresh: "主题补扫" } as Record<string, string>)[trigger || ""] || trigger || "未知"; }
function scanStatusTone(status?: string): "neutral" | "good" | "warn" | "danger" { if (status === "completed") return "good"; if (status === "failed") return "danger"; if (status === "partial") return "warn"; return "neutral"; }
function strategyStatusLabel(status?: string) { return ({ pending: "生成中", generated: "已生成", skipped: "未生成" } as Record<string, string>)[status || ""] || "-"; }
function candidateStageLabel(stage?: string) { return ({ prefiltered: "预筛", research_candidate: "研究池", reviewed_out: "复核未入选", final: "最终候选", excluded: "预筛排除" } as Record<string, string>)[stage || ""] || stage || "-"; }
function candidateSourceLaneLabel(source?: string) { return ({ message: "消息驱动", sector: "板块轮动", mixed: "多路准入", price: "行情预筛" } as Record<string, string>)[source || ""] || "行情预筛"; }
function sectorStateLabel(state?: string) { return ({ emerging: "涌现", confirmed: "确认", overheated: "过热", fading: "退潮", invalidated: "失效" } as Record<string, string>)[state || ""] || state || "未知"; }
function sectorStateTone(state?: string): "neutral" | "good" | "warn" | "danger" { if (state === "emerging" || state === "confirmed") return "good"; if (state === "overheated" || state === "fading") return "warn"; if (state === "invalidated") return "danger"; return "neutral"; }
function themeMatchKindLabel(kind?: string) { return ({ direct_symbol: "明确提及", structured_term: "画像结构匹配", profile_keyword: "画像关键词", semantic_recall: "语义召回" } as Record<string, string>)[kind || ""] || "待核验"; }
function fundFlowCandidateLabel(candidate: StockV2OpportunityMarketScanCandidate) { return ({ not_requested: "未请求", source_unavailable: "源不可用", invalid_data: "数据无效", run_degraded: "本轮未采用", available: candidate.metrics.fundFlowUsed ? candidate.flowScore.toFixed(0) : "本轮未采用" } as Record<string, string>)[candidate.metrics.fundFlowStatus || ""] || "未取得"; }
function fundFlowRunLabel(run: StockV2OpportunityMarketScanRun) { if (run.fundFlowUsed) return `${run.fundFlowAvailableCount}/${run.fundFlowRequestedCount} 已用于评分`; if (run.fundFlowStatus === "not_configured") return "未配置数据源"; return `${run.fundFlowAvailableCount}/${run.fundFlowRequestedCount} 覆盖不足，本轮未采用`; }
function budgetLabel(key: string) { return ({ localPrefilter: "本地预筛", priceAdmission: "行情基础准入", sectorAdmission: "板块代表准入", sectorResearch: "板块候选复核保留", qfqAndQuote: "前复权与报价", fundFlow: "资金流", agentResearch: "Agent 复核", finalCandidates: "最终候选", strategyDrafts: "策略草案", messageAdmission: "消息候选准入", messageResearch: "消息候选复核保留", sectorCoverageHealthyPct: "板块分类健康覆盖率 (%)", sectorCoverageMinimumPct: "板块分类阻断覆盖率 (%)" } as Record<string, string>)[key] || key; }
function decisionGateCount(candidate: StockV2OpportunityMarketScanCandidate) { return candidate.metrics.decisionGates?.filter((item) => item.status === "pass" || item.status === "not_applicable").length || 0; }
function decisionHealthLabel(status?: string) { return ({ healthy: "健康", degraded: "降级", blocked: "阻断", not_applicable: "不适用" } as Record<string, string>)[status || ""] || "未检查"; }
function decisionHealthTone(status?: string): "neutral" | "good" | "warn" | "danger" { if (status === "healthy") return "good"; if (status === "blocked") return "danger"; if (status === "degraded") return "warn"; return "neutral"; }
function decisionGateStatusLabel(status?: string) { return ({ pass: "通过", blocked: "阻断", degraded: "降级", not_applicable: "不适用" } as Record<string, string>)[status || ""] || "未检查"; }
function decisionGateTone(status?: string): "neutral" | "good" | "warn" | "danger" { if (status === "pass") return "good"; if (status === "blocked") return "danger"; if (status === "degraded") return "warn"; return "neutral"; }
function marketRegimeLabel(status?: string) { return ({ risk_on: "风险偏好", neutral: "中性", risk_off: "风险关闭" } as Record<string, string>)[status || ""] || "未判断"; }
function sourceProbeLabel(key: string) { return ({ fundFlow: "资金流", benchmark: "市场基准", tradeCalendar: "交易日历", eventCalendar: "公司事件", financialFacts: "财务事实", config: "配置" } as Record<string, string>)[key] || key; }
