import { useState, type ReactNode } from "react";
import { ArrowSquareOut, ArrowsClockwise, Trash } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentRun,
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

  const [phase, setPhase] = useState<Phase>("form");
  const [submitError, setSubmitError] = useState<string | null>(null);
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
        portfolioId: initial.portfolioId,
        userIntent: userIntent.trim() || undefined,
        userGoal: userGoal.trim() || undefined,
      };
    }
    return {
      mode,
      targetInstruments: targets.map((t) => ({ symbol: t.symbol, market: t.market, name: t.name })),
      userIntent: userIntent.trim() || undefined,
      timeHorizon,
      ...(allowedActions.length ? { allowedActions } : {}),
    };
  }

  async function submit() {
    setPhase("submitting");
    setSubmitError(null);
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
      setSubmitError(friendlyError(err));
      setPhase("error");
      actions.setToast(friendlyError(err), "danger");
    }
  }

  // AgentRun 异步推进，submitted 视图给手动刷新入口而非轮询（克制）。
  async function refreshRun() {
    if (!run) return;
    try {
      const res = await actions.api<StockV2AgentRun>(`/api/stockv2/agent/runs/${run.id}`);
      setRun(res);
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
              <Notice tone="danger">提交失败：{submitError}</Notice>
            ) : null}

            <Notice tone="warn">
              {isPortfolio
                ? "诊断结果以策略草案和运行详情呈现，不直接生成操作单；账户相关建议会进入 Review。"
                : "生成的策略草案需确认激活后才生效；账户无关策略只产 trade_signal，不会直接下单。"}
            </Notice>

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
