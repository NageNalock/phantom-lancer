import { useState, type ReactNode } from "react";
import { ArrowSquareOut, ArrowsClockwise, Trash } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  ApiError,
  StockV2AgentRun,
  StockV2AssetReadinessDecision,
  StockV2AssetReadinessMode,
  StockV2StrategyActionType,
  StockV2StrategyGenerationInput,
  StockV2StrategyGenerationMode,
  StockV2StrategyGenerationTargetInstrument,
  StockV2StrategyGenerationTimeHorizon,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, ContextList, Drawer, Field, Notice, Pill } from "../../components/ui";
import { stockV2AgentRunStatusLabel, stockV2AgentRunStatusTone } from "../../domain/labels";
import { playbookActionLabel } from "./StockV2StrategyPlaybook";
import { SymbolPicker, SymbolRef } from "./StockV2SymbolPicker";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";

// 策略生成允许的动作集合（与 playbook rule.action 同一套协议，不引入第二套）。
const ACTION_OPTIONS: StockV2StrategyActionType[] = [
  "observe",
  "build_position",
  "add_position",
  "hold",
  "reduce_position",
  "exit_position",
];

const TIME_HORIZONS: Array<{ value: StockV2StrategyGenerationTimeHorizon; label: string }> = [
  { value: "short", label: "短线" },
  { value: "swing", label: "波段" },
  { value: "medium", label: "中期" },
  { value: "long", label: "长期" },
  { value: "unspecified", label: "不指定" },
];

export interface StrategyGenerationInitial {
  mode: StockV2StrategyGenerationMode;
  targetInstruments?: StockV2StrategyGenerationTargetInstrument[];
  portfolioId?: string;
  portfolioName?: string;
}

type Phase = "form" | "submitting" | "submitted" | "error";

// StrategyGenerationDrawer：策略生成 / 组合诊断的统一入口。
//   manual_target             —— 用户选标的，完整表单
//   single_instrument         —— 标的预填只读（来自股票详情）
//   portfolio_strategy_diagnosis —— 组合诊断，极简（组合只读 + 诉求）
// 提交后后端异步生成草案；本组件只产 trade_signal/review_request，
// 不展示成操作单，账户绑定建议走 OperationReview。
export function StrategyGenerationDrawer({
  actions,
  initial,
  onClose,
  onSubmitted,
}: {
  actions: AppActions;
  initial: StrategyGenerationInitial;
  onClose: () => void;
  onSubmitted?: (run: StockV2AgentRun) => void;
}) {
  const mode = initial.mode;
  const isPortfolio = mode === "portfolio_strategy_diagnosis";
  const isSingle = mode === "single_instrument";

  const [targets, setTargets] = useState<StockV2StrategyGenerationTargetInstrument[]>(
    initial.targetInstruments?.length ? initial.targetInstruments : [],
  );
  const [pickKey, setPickKey] = useState(0);
  const [userIntent, setUserIntent] = useState("");
  const [userGoal, setUserGoal] = useState("");
  const [timeHorizon, setTimeHorizon] = useState<StockV2StrategyGenerationTimeHorizon>("unspecified");
  const [allowedActions, setAllowedActions] = useState<StockV2StrategyActionType[]>([]);
  const [readinessMode, setReadinessMode] = useState<StockV2AssetReadinessMode>("strict");

  const [phase, setPhase] = useState<Phase>("form");
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [readinessDecision, setReadinessDecision] = useState<StockV2AssetReadinessDecision | null>(null);
  const [run, setRun] = useState<StockV2AgentRun | null>(null);
  const [agentDetailRunId, setAgentDetailRunId] = useState<string | null>(null);

  const canSubmit = isPortfolio ? !!initial.portfolioId : targets.length > 0;

  function addTarget(ref: SymbolRef) {
    const symbol = ref.symbol.trim();
    if (!symbol || targets.some((t) => t.symbol === symbol)) {
      setPickKey((n) => n + 1);
      return;
    }
    setTargets((prev) => [...prev, { symbol, market: ref.market, name: ref.name }]);
    setPickKey((n) => n + 1); // 重置搜索框，便于继续添加下一个标的
  }

  function removeTarget(symbol: string) {
    setTargets((prev) => prev.filter((t) => t.symbol !== symbol));
  }

  function toggleAction(action: StockV2StrategyActionType) {
    setAllowedActions((prev) =>
      prev.includes(action) ? prev.filter((a) => a !== action) : [...prev, action],
    );
  }

  function buildInput(): StockV2StrategyGenerationInput {
    if (isPortfolio) {
      // 组合诊断：让后端用完整动作空间诊断，前端不传 allowedActions/timeHorizon。
      return {
        mode,
        readinessMode,
        portfolioId: initial.portfolioId,
        userIntent: userIntent.trim() || undefined,
        userGoal: userGoal.trim() || undefined,
      };
    }
    return {
      mode,
      readinessMode,
      targetInstruments: targets.map((t) => ({ symbol: t.symbol, market: t.market, name: t.name })),
      userIntent: userIntent.trim() || undefined,
      timeHorizon,
      ...(allowedActions.length ? { allowedActions } : {}),
    };
  }

  async function submit() {
    setPhase("submitting");
    setSubmitError(null);
    setReadinessDecision(null);
    try {
      const res = await actions.api<StockV2AgentRun>("/api/stockv2/agent/strategy-generation/run", {
        method: "POST",
        body: buildInput(),
      });
      setRun(res);
      onSubmitted?.(res);
      setPhase("submitted");
      actions.setToast("Agent 运行已启动，策略草案将在完成后进入草案列表", "good");
    } catch (err) {
      const decision = readinessDecisionFromError(err);
      const message = decision ? "数据资产未达到当前运行要求" : friendlyError(err);
      setReadinessDecision(decision);
      setSubmitError(message);
      setPhase("error");
      actions.setToast(message, "danger");
    }
  }

  // AgentRun 异步推进，submitted 视图给手动刷新入口而非轮询（克制）。
  async function refreshRun() {
    if (!run) return;
    try {
      const res = await actions.api<StockV2AgentRun>(`/api/stockv2/agent/runs/${run.id}`);
      setRun({ ...res, readinessDecision: res.readinessDecision ?? run.readinessDecision });
    } catch {
      // 刷新失败静默，保留已展示的 submitted 视图，用户可重试。
    }
  }

  const title = isPortfolio ? "诊断当前组合" : isSingle ? "为该股票生成策略" : "生成策略";
  const subtitle = isPortfolio
    ? "逐持仓给出后续建议，生成策略草案或补丁"
    : "Agent 基于行情/消息/组合上下文生成策略草案";

  return (
    <>
      <Drawer title={title} subtitle={subtitle} onClose={onClose} width={560}>
        {phase === "submitted" && run ? (
          <SubmittedView
            run={run}
            isPortfolio={isPortfolio}
            onOpenDetail={() => setAgentDetailRunId(run.id)}
            onRefresh={() => void refreshRun()}
            onClose={onClose}
          />
        ) : (
          <div className="grid gap-4">
            {phase === "error" && submitError ? (
              readinessDecision ? (
                <ReadinessBlockedNotice decision={readinessDecision} />
              ) : (
                <Notice tone="danger">提交失败：{submitError}</Notice>
              )
            ) : null}

            <Notice tone="warn">
              {isPortfolio
                ? "诊断结果以策略草案和运行详情呈现，不直接生成操作单；账户相关建议会进入 Review。"
                : "生成的策略草案需确认激活后才生效；账户无关策略只产 trade_signal，不会直接下单。"}
            </Notice>

            <ReadinessModeField value={readinessMode} onChange={setReadinessMode} />

            {isPortfolio ? (
              <Field label="目标组合">
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-sm">
                  <strong>{initial.portfolioName || initial.portfolioId}</strong>
                  <span className="ml-2 font-mono text-xs text-[var(--muted)]">
                    {initial.portfolioId ? initial.portfolioId.slice(0, 8) : ""}
                  </span>
                </div>
              </Field>
            ) : (
              <TargetsField
                targets={targets}
                readonly={isSingle}
                actions={actions}
                pickKey={pickKey}
                onAdd={addTarget}
                onRemove={removeTarget}
              />
            )}

            <Field
              label={isPortfolio ? "诊断诉求" : "用户意图"}
              help={
                isPortfolio
                  ? "例如：哪些持仓优先处理、是否减仓防范集中度。可不填，进行整体诊断"
                  : "你的目标或问题，例如 302132 中期看多"
              }
            >
              <textarea
                rows={3}
                value={isPortfolio ? userGoal : userIntent}
                onChange={(e) => (isPortfolio ? setUserGoal(e.target.value) : setUserIntent(e.target.value))}
                placeholder={isPortfolio ? "可选" : "可选，但建议填写以引导 Agent"}
              />
            </Field>

            {!isPortfolio ? (
              <>
                <Field label="时间范围">
                  <select
                    value={timeHorizon}
                    onChange={(e) => setTimeHorizon(e.target.value as StockV2StrategyGenerationTimeHorizon)}
                  >
                    {TIME_HORIZONS.map((t) => (
                      <option key={t.value} value={t.value}>
                        {t.label}
                      </option>
                    ))}
                  </select>
                </Field>

                <Field label="允许动作" help="限定 Agent 可建议的动作范围；不选则不限制">
                  <div className="flex flex-wrap gap-1.5">
                    {ACTION_OPTIONS.map((action) => {
                      const active = allowedActions.includes(action);
                      return (
                        <button
                          key={action}
                          type="button"
                          onClick={() => toggleAction(action)}
                          className={`rounded-md border px-2 py-1 text-xs transition ${
                            active
                              ? "border-[var(--accent)] text-[var(--accent)]"
                              : "border-[var(--line)] text-[var(--muted-strong)] hover:border-[var(--line-strong)]"
                          }`}
                        >
                          {playbookActionLabel(action)}
                        </button>
                      );
                    })}
                  </div>
                </Field>
              </>
            ) : null}

            <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
              <Button onClick={onClose}>取消</Button>
              <Button tone="primary" disabled={!canSubmit || phase === "submitting"} onClick={() => void submit()}>
                {phase === "submitting" ? "提交中…" : isPortfolio ? "开始诊断" : "开始生成"}
              </Button>
            </div>
          </div>
        )}
      </Drawer>

      {agentDetailRunId ? (
        <StockV2AgentRunDetailDrawer
          actions={actions}
          runId={agentDetailRunId}
          onClose={() => setAgentDetailRunId(null)}
        />
      ) : null}
    </>
  );
}

function ReadinessModeField({
  value,
  onChange,
}: {
  value: StockV2AssetReadinessMode;
  onChange: (value: StockV2AssetReadinessMode) => void;
}) {
  const options: Array<{ value: StockV2AssetReadinessMode; title: string; description: string }> = [
    {
      value: "strict",
      title: "严格模式",
      description: "任一标的数据面、消息面或 AI 画像未就绪时阻止运行。",
    },
    {
      value: "allow_degraded",
      title: "允许降级",
      description: "继续运行，并把缺失项和限制写入本次决策记录。",
    },
  ];
  return (
    <Field label="数据就绪策略" help="默认使用严格模式。只有明确接受不完整数据时才允许降级">
      <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="数据就绪策略">
        {options.map((option) => {
          const selected = value === option.value;
          return (
            <label
              key={option.value}
              className={`flex min-w-0 items-start gap-2 rounded-lg border px-3 py-2.5 text-left transition-colors ${
                selected
                  ? "border-[var(--accent)] bg-[var(--accent-soft)]"
                  : "border-[var(--line)] bg-[var(--surface)] hover:border-[var(--line-strong)]"
              }`}
            >
              <input
                checked={selected}
                className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--accent)]"
                name="stockv2-readiness-mode"
                onChange={() => onChange(option.value)}
                type="radio"
                value={option.value}
              />
              <span className="min-w-0">
                <strong className="block text-xs font-semibold text-[var(--text)]">{option.title}</strong>
                <span className="mt-0.5 block text-[11px] leading-4 text-[var(--muted-strong)]">
                  {option.description}
                </span>
              </span>
            </label>
          );
        })}
      </div>
    </Field>
  );
}

function ReadinessBlockedNotice({ decision }: { decision: StockV2AssetReadinessDecision }) {
  const failedSymbols = decision.failedSymbols ?? [];
  const visibleSymbols = failedSymbols.slice(0, 20);
  const groupedReasons = groupReadinessReasons(decision);
  return (
    <Notice tone="danger">
      <div className="grid gap-2">
        <div>
          <strong>严格模式已阻止运行</strong>
          <span className="ml-2 text-xs">
            已就绪 {decision.readyCount} / {decision.targetCount}
          </span>
        </div>
        {visibleSymbols.length ? (
          <div className="text-xs">
            <span className="text-[var(--muted-strong)]">未就绪标的</span>
            <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 font-mono text-[var(--text)]">
              {visibleSymbols.map((symbol) => <span key={symbol}>{symbol}</span>)}
              {failedSymbols.length > visibleSymbols.length ? (
                <span className="font-sans text-[var(--muted-strong)]">另 {failedSymbols.length - visibleSymbols.length} 只</span>
              ) : null}
            </div>
          </div>
        ) : null}
        {groupedReasons.length ? (
          <div className="text-xs">
            <span className="text-[var(--muted-strong)]">阻断原因</span>
            <div className="mt-1 grid gap-1">
              {groupedReasons.map((reason) => (
                <div className="flex flex-wrap items-baseline justify-between gap-x-3" key={`${reason.domain}:${reason.code}`}>
                  <span>{readinessReasonLabel(reason.code)} <span className="font-mono text-[10px] text-[var(--muted)]">{reason.code}</span></span>
                  <span className="font-mono text-[var(--muted-strong)]">{reason.count}</span>
                </div>
              ))}
            </div>
          </div>
        ) : null}
        <p className="m-0 text-xs text-[var(--muted-strong)]">
          可先维护数据后重试，或明确选择“允许降级”继续并保留缺失记录。
        </p>
      </div>
    </Notice>
  );
}

function readinessDecisionFromError(error: unknown): StockV2AssetReadinessDecision | null {
  const apiError = error as ApiError;
  if (apiError?.code !== "stockv2_assets_not_ready" || !apiError.payload || typeof apiError.payload !== "object") {
    return null;
  }
  const decision = (apiError.payload as { decision?: StockV2AssetReadinessDecision }).decision;
  return decision?.status === "blocked" ? decision : null;
}

function groupReadinessReasons(decision: StockV2AssetReadinessDecision) {
  const grouped = new Map<string, { domain: string; code: string; count: number }>();
  for (const reason of decision.reasons ?? []) {
    const key = `${reason.domain}:${reason.code}`;
    const current = grouped.get(key);
    if (current) current.count += 1;
    else grouped.set(key, { domain: reason.domain, code: reason.code, count: 1 });
  }
  return Array.from(grouped.values());
}

function readinessReasonLabel(code: string): string {
  const labels: Record<string, string> = {
    trading_calendar_unavailable: "交易日历不可用",
    trading_calendar_not_authoritative: "交易日历未完成权威校验",
    trading_calendar_stale: "交易日历已过期",
    daily_bar_missing: "缺少日 K",
    daily_bar_coverage_unverified: "日 K 覆盖未验证",
    daily_bar_date_gaps: "日 K 存在日期缺口",
    daily_bar_fields_incomplete: "日 K 字段不完整",
    daily_bar_coverage_outdated: "日 K 覆盖已过期",
    base_profile_missing: "缺少基础画像",
    base_profile_stale: "基础画像已过期",
    announcement_cursor_behind: "公告水位未追平",
    ai_profile_missing_or_not_ready: "AI 画像未就绪",
    ai_desired_input_version_missing: "AI 输入版本缺失",
    ai_input_version_outdated: "AI 画像输入版本已过期",
    major_announcement_content_unavailable: "重大公告正文或摘要不可用",
    asset_list_empty: "没有可分析标的",
  };
  return labels[code] ?? "数据资产未就绪";
}

function TargetsField({
  targets,
  readonly,
  actions,
  pickKey,
  onAdd,
  onRemove,
}: {
  targets: StockV2StrategyGenerationTargetInstrument[];
  readonly: boolean;
  actions: AppActions;
  pickKey: number;
  onAdd: (ref: SymbolRef) => void;
  onRemove: (symbol: string) => void;
}) {
  return (
    <Field label="目标股票" help={readonly ? undefined : "可添加多个标的"}>
      <div className="grid gap-2">
        {targets.length === 0 ? (
          <p className="text-xs text-[var(--muted)]">{readonly ? "未提供标的" : "尚未添加标的，在下方搜索并选择"}</p>
        ) : (
          targets.map((t) => (
            <div
              key={t.symbol}
              className="flex items-center justify-between rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-sm"
            >
              <span className="min-w-0">
                <span className="font-mono">{t.symbol}</span>
                {t.name ? <span className="ml-2 text-[var(--muted-strong)]">{t.name}</span> : null}
                {t.market ? <span className="ml-2 text-xs text-[var(--muted)]">{t.market}</span> : null}
              </span>
              {!readonly ? (
                <Button title="移除标的" onClick={() => onRemove(t.symbol)}>
                  <Trash size={12} className="mr-1" />
                  移除
                </Button>
              ) : null}
            </div>
          ))
        )}
        {!readonly ? (
          // key 随 pickKey 变化，选中后重置搜索框
          <SymbolPicker key={pickKey} actions={actions} value={{ symbol: "" }} onChange={onAdd} />
        ) : null}
      </div>
    </Field>
  );
}

function SubmittedView({
  run,
  isPortfolio,
  onOpenDetail,
  onRefresh,
  onClose,
}: {
  run: StockV2AgentRun;
  isPortfolio: boolean;
  onOpenDetail: () => void;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const metaItems: Array<[string, ReactNode]> = [
    ["Run ID", <span className="font-mono">{run.id}</span>],
    ["任务类型", <span className="font-mono">{run.taskType}</span>],
  ];
  if (run.modelId) metaItems.push(["模型", <span className="font-mono">{run.modelId}</span>]);
  if (run.decisionLedgerId) metaItems.push(["Ledger", <span className="font-mono">{run.decisionLedgerId}</span>]);
  return (
    <div className="grid gap-4">
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <strong>{isPortfolio ? "组合诊断已启动" : "策略生成已启动"}</strong>
          <Pill tone={stockV2AgentRunStatusTone(run.status)}>{stockV2AgentRunStatusLabel(run.status)}</Pill>
        </div>
        <p className="mt-1.5 text-xs text-[var(--muted-strong)]">
          Agent 正在后台运行。完成后{isPortfolio ? "诊断结果" : "策略草案"}会出现在策略列表的草案中；运行过程和结构化输出可在 Agent 运行详情查看。
        </p>
      </div>

      {run.errorMessage ? (
        <Notice tone="danger">运行错误：{run.errorMessage}</Notice>
      ) : null}

      {run.readinessDecision?.status === "degraded" ? (
        <ReadinessDegradedNotice decision={run.readinessDecision} />
      ) : null}

      <ContextList items={metaItems} />

      <div className="flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-3">
        <Button onClick={onRefresh} title="重新拉取运行状态">
          <ArrowsClockwise size={14} className="mr-1.5" />
          刷新状态
        </Button>
        <Button onClick={onOpenDetail} title="查看 Agent 运行详情">
          <ArrowSquareOut size={14} className="mr-1.5" />
          查看 Agent 运行详情
        </Button>
        <Button tone="primary" onClick={onClose}>
          关闭
        </Button>
      </div>
    </div>
  );
}

function ReadinessDegradedNotice({ decision }: { decision: StockV2AssetReadinessDecision }) {
  const failedSymbols = decision.failedSymbols ?? [];
  const reasons = groupReadinessReasons(decision);
  return (
    <Notice tone="warn">
      <div className="grid gap-1.5 text-xs">
        <strong>本次按允许降级模式运行</strong>
        <span>已就绪 {decision.readyCount} / {decision.targetCount}</span>
        {failedSymbols.length ? (
          <span>未就绪标的：<span className="font-mono">{failedSymbols.slice(0, 12).join("、")}</span>{failedSymbols.length > 12 ? `，另 ${failedSymbols.length - 12} 只` : ""}</span>
        ) : null}
        {reasons.length ? (
          <span>限制：{reasons.map((reason) => `${readinessReasonLabel(reason.code)} × ${reason.count}`).join("；")}</span>
        ) : null}
      </div>
    </Notice>
  );
}
