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

const ACTIVE_STATUSES = new Set(["pending", "prefiltering", "enriching", "researching", "drafting"]);
const STAGES = ["prefiltering", "enriching", "researching", "drafting"] as const;
type ResultStage = "final" | "research_candidate" | "reviewed_out" | "excluded";

export function StockV2OpportunityMarketScan({ actions }: { actions: AppActions }) {
  const [status, setStatus] = useState<StockV2OpportunityMarketScanStatus | null>(null);
  const [runs, setRuns] = useState<StockV2OpportunityMarketScanRun[]>([]);
  const [selectedRunId, setSelectedRunId] = useState<string>("");
  const [candidates, setCandidates] = useState<StockV2OpportunityMarketScanCandidate[]>([]);
  const [candidateTotal, setCandidateTotal] = useState(0);
  const [resultStage, setResultStage] = useState<ResultStage>("final");
  const [candidateOffset, setCandidateOffset] = useState(0);
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

  async function loadCandidates(runId: string, stage = resultStage, offset = candidateOffset) {
    if (!runId) { setCandidates([]); return; }
    const requestID = ++candidateRequest.current;
    try {
      const limit = stage === "excluded" ? 50 : 50;
      const result = await actions.api<{ items: StockV2OpportunityMarketScanCandidate[]; total: number }>(
        `/api/stockv2/opportunity-market-scan/runs/${encodeURIComponent(runId)}/candidates?stage=${encodeURIComponent(stage)}&limit=${limit}&offset=${offset}`,
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
  useEffect(() => { void loadCandidates(selectedRunId, resultStage, candidateOffset); }, [resultStage, candidateOffset]);
  useEffect(() => {
    if (!status?.activeRun) return;
    const timer = window.setInterval(() => { void load(); void loadCandidates(selectedRunId, resultStage, candidateOffset); }, 15_000);
    return () => window.clearInterval(timer);
  }, [candidateOffset, resultStage, selectedRunId, status?.activeRun?.id]);

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
        subtitle="全市场确定性预筛 → 有界数据富集 → Agent 证据复核 → 最多 3 份未激活建仓草案"
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
                <span className="muted mt-1 block text-xs">{run.triggerType === "scheduled" ? "自动" : "手动"} · {formatMeaningfulDateTime(run.createdAt)}</span>
              </button>
            ))}
          </div>
        </Panel>

        <Panel
          title={selectedRun ? `${selectedRun.tradeDate || "扫描"} · 扫描结果` : "扫描结果"}
          subtitle={selectedRun ? `预筛 ${selectedRun.prefilterCount} · 复核 ${selectedRun.researchCount} · 最终 ${selectedRun.finalCandidateCount} · 草案 ${selectedRun.strategyCreatedCount}` : "选择一条扫描记录"}
          actions={selectedRun && (selectedRun.status === "failed" || selectedRun.status === "partial") ? <Button disabled={runningAction || !!status?.activeRun} onClick={() => void retry(selectedRun)}><Repeat size={14} />重试</Button> : undefined}
        >
          {selectedRun?.errorMessage ? <div className="mb-3"><Notice tone={selectedRun.status === "failed" ? "danger" : "warn"}>{selectedRun.errorMessage}</Notice></div> : null}
          {selectedRun ? <ResultTabs run={selectedRun} stage={resultStage} onChange={(next) => { setResultStage(next); setCandidateOffset(0); }} /> : null}
          {selectedRun?.fundFlowRequestedCount ? <div className="mb-3 mt-3 flex flex-wrap items-center gap-2 text-xs"><span className="muted">主动资金证据</span><Pill tone={selectedRun.fundFlowUsed ? "good" : "warn"}>{fundFlowRunLabel(selectedRun)}</Pill>{selectedRun.fundFlowSource ? <span className="font-mono">{selectedRun.fundFlowSource}</span> : null}</div> : null}
          {candidates.length === 0 ? <EmptyState title="当前分组暂无记录" body={selectedRun && ACTIVE_STATUSES.has(selectedRun.status) ? "扫描仍在执行，研究池会随阶段持续落盘。" : "本轮在这个结果分组中没有记录。"} /> : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] text-left text-xs">
                <thead className="text-[var(--muted)]"><tr className="border-b border-[var(--line)]"><th className="px-2 py-2">排名</th><th className="px-2 py-2">标的</th><th className="px-2 py-2">行业</th><th className="px-2 py-2">综合</th><th className="px-2 py-2">20日</th><th className="px-2 py-2">主动资金证据</th><th className="px-2 py-2">主题</th><th className="px-2 py-2">策略</th></tr></thead>
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
    <td className="px-2 py-2">{candidate.industry || "-"}</td>
    <td className="px-2 py-2 font-mono">{candidate.finalScore.toFixed(1)}</td>
    <td className="px-2 py-2 font-mono">{formatPct(candidate.metrics.return20Pct)}</td>
    <td className="px-2 py-2"><span className="font-mono">{candidate.metrics.fundFlowUsed ? candidate.flowScore.toFixed(0) : fundFlowCandidateLabel(candidate)}</span>{candidate.metrics.fundFlowSource ? <span className="muted mt-1 block text-[10px]">{candidate.metrics.fundFlowSource}</span> : null}</td>
    <td className="px-2 py-2 font-mono">{candidate.themeScore.toFixed(0)}</td>
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
      const result = await actions.api<{ ok: boolean; source?: string; count: number; durationMs: number; error?: string }>("/api/stockv2/opportunity-market-scan/fund-flow/probe", { method: "POST" });
      actions.setToast(result.ok ? `资金源可用：${result.source}，${result.count} 条，${result.durationMs}ms` : `资金源不可用：${result.error || "未知错误"}`, result.ok ? "good" : "danger");
    } catch (cause) { actions.setToast(friendlyError(cause), "danger"); } finally { setProbing(false); }
  }
  return <Drawer title="市场扫描配置" subtitle="自动扫描默认关闭；手动扫描不受此开关影响" onClose={onClose} width={520}>
    <div className="grid gap-4">
      <Toggle checked={enabled} label={<span><strong className="block">每日自动扫描</strong><span className="muted text-xs">全市场数据维护成功后，每个新交易日最多启动一次</span></span>} onChange={setEnabled} />
      <CollapsibleSection title="主动资金证据源" subtitle="仅用于候选股票的历史主动资金证据，与账户可用资金无关">
        <div className="grid gap-3 text-xs">
          <SecretField configured={status.config.primaryFundFlowConfigured} label="DataHubCo 主源 API Key" value={primaryKey} onChange={setPrimaryKey} clear={clearPrimary} onClear={setClearPrimary} />
          <SecretField configured={status.config.backupFundFlowConfigured} label="Indevs 备源 API Key" value={backupKey} onChange={setBackupKey} clear={clearBackup} onClear={setClearBackup} />
          <label><span className="mb-1 block font-medium">备源独立代理 URL</span><input className="w-full rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 font-mono outline-none focus:border-[var(--accent)]" onChange={(event) => setBackupProxy(event.target.value)} placeholder={status.config.backupFundFlowProxyConfigured ? "已配置，留空保持不变" : "可选，仅支持 http/https"} type="password" value={backupProxy} /></label>
          <Toggle checked={clearProxy} label={<span>清除已保存的备源代理</span>} onChange={setClearProxy} />
          <div className="flex justify-end"><Button disabled={probing} onClick={() => void probe()}>{probing ? "检测中" : "检测已保存配置"}</Button></div>
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
      <div className="grid grid-cols-3 gap-2"><ScanMetric label="综合评分" value={candidate.finalScore.toFixed(1)} /><ScanMetric label="主动资金证据" value={metrics.fundFlowUsed ? candidate.flowScore.toFixed(1) : fundFlowCandidateLabel(candidate)} /><ScanMetric label="主题评分" value={candidate.themeScore.toFixed(1)} /></div>
      <Panel title="行情与质量"><dl className="grid grid-cols-2 gap-3 text-xs"><MetricItem label="最新价" value={metrics.latestPrice?.toFixed(3) || "-"} /><MetricItem label="当日涨跌" value={formatPct(metrics.latestPctChange)} /><MetricItem label="5 日收益" value={formatPct(metrics.return5Pct)} /><MetricItem label="20 日收益" value={formatPct(metrics.return20Pct)} /><MetricItem label="量比 5/20" value={metrics.volumeRatio5To20?.toFixed(2) || "-"} /><MetricItem label="主动资金证据" value={metrics.fundFlowAvailable ? `20 日净额占主动成交 ${metrics.mainFlowRatio20?.toFixed(2)}% · ${metrics.fundFlowAsOf || "日期未知"}` : fundFlowCandidateLabel(candidate)} /></dl></Panel>
      <Panel title="消息脉络信号">{metrics.themeSignals?.length ? <ul className="m-0 grid gap-2 pl-5 text-xs">{metrics.themeSignals.map((item) => <li key={item}>{item}</li>)}</ul> : <p className="muted m-0 text-xs">未匹配到活跃消息脉络，主题评分不加分。</p>}</Panel>
      {review ? <Panel title="Agent 复核"><dl className="grid grid-cols-2 gap-3 text-xs"><MetricItem label="证据评分" value={review.evidenceScore?.toFixed(1) || "-"} /><MetricItem label="置信度" value={review.confidence?.toFixed(2) || "-"} /></dl><p className="mt-3 mb-0 text-xs">{review.reason || "未提供候选理由"}</p>{review.riskSummary ? <p className="muted mt-2 mb-0 text-xs">风险：{review.riskSummary}</p> : null}</Panel> : null}
      {evidence.length ? <CollapsibleSection title="证据记录" subtitle={`${evidence.length} 条内部与公开资料留痕`}><ul className="m-0 grid gap-3 pl-5 text-xs">{evidence.map((item) => <li key={item.id}><strong>{item.title || item.sourceType}</strong>{item.summary ? <span className="muted mt-1 block">{item.summary}</span> : null}</li>)}</ul></CollapsibleSection> : null}
      {candidate.strategyId ? <Panel title="策略草案"><p className="m-0 text-xs">已生成未激活策略，可在“策略”工作台复核后再启用。</p><code className="muted mt-2 block text-xs">{candidate.strategyId}</code></Panel> : null}
      {candidate.exclusionReason ? <Notice>{candidate.exclusionReason}</Notice> : null}
    </div>
  </Drawer>;
}

function MetricItem({ label, value }: { label: string; value: string }) { return <div><dt className="muted">{label}</dt><dd className="m-0 mt-1 font-mono">{value}</dd></div>; }
function formatPct(value?: number) { return value === undefined ? "-" : `${value > 0 ? "+" : ""}${value.toFixed(2)}%`; }
function scanStatusLabel(status?: string) { return ({ pending: "等待", prefiltering: "本地预筛", enriching: "数据富集", researching: "证据复核", drafting: "策略草案", completed: "完成", partial: "部分完成", failed: "失败" } as Record<string, string>)[status || ""] || "未运行"; }
function scanStatusTone(status?: string): "neutral" | "good" | "warn" | "danger" { if (status === "completed") return "good"; if (status === "failed") return "danger"; if (status === "partial") return "warn"; return "neutral"; }
function strategyStatusLabel(status?: string) { return ({ pending: "生成中", generated: "已生成", skipped: "未生成" } as Record<string, string>)[status || ""] || "-"; }
function candidateStageLabel(stage?: string) { return ({ prefiltered: "预筛", research_candidate: "研究池", reviewed_out: "复核未入选", final: "最终候选", excluded: "预筛排除" } as Record<string, string>)[stage || ""] || stage || "-"; }
function fundFlowCandidateLabel(candidate: StockV2OpportunityMarketScanCandidate) { return ({ not_requested: "未请求", source_unavailable: "源不可用", invalid_data: "数据无效", run_degraded: "本轮未采用", available: candidate.metrics.fundFlowUsed ? candidate.flowScore.toFixed(0) : "本轮未采用" } as Record<string, string>)[candidate.metrics.fundFlowStatus || ""] || "未取得"; }
function fundFlowRunLabel(run: StockV2OpportunityMarketScanRun) { if (run.fundFlowUsed) return `${run.fundFlowAvailableCount}/${run.fundFlowRequestedCount} 已用于评分`; if (run.fundFlowStatus === "not_configured") return "未配置数据源"; return `${run.fundFlowAvailableCount}/${run.fundFlowRequestedCount} 覆盖不足，本轮未采用`; }
function budgetLabel(key: string) { return ({ localPrefilter: "本地预筛", qfqAndQuote: "前复权与报价", fundFlow: "资金流", agentResearch: "Agent 复核", finalCandidates: "最终候选", strategyDrafts: "策略草案" } as Record<string, string>)[key] || key; }
