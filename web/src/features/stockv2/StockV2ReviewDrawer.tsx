import { ShieldCheck } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2OperationReview,
  StockV2OperationReviewListResponse,
  StockV2OperationReviewOutputType,
  StockV2OperationReviewResultInput,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, ContextList, Drawer, Field, Notice, Pill } from "../../components/ui";
import {
  formatDate,
  stockV2GuardrailsStatusLabel,
  stockV2GuardrailsStatusTone,
  stockV2ReviewOutputTypeLabel,
  stockV2ReviewStatusLabel,
  stockV2ReviewStatusTone,
} from "../../domain/labels";

// Review drawer:从 MonitorHit 进入人工/Agent 审阅。显示 ContextPack 摘要、
// Review 状态,支持人工填写并保存结构化结果(trade_signal / proposed_operation /
// strategy_patch / ignore / continue_monitoring)。proposed_operation 保存后由
// 后端回填 guardrails 结果,在此展示。本轮不调用真实 Agent,只接已有后端 API。
//
// 健壮性:无 review / loading / error / completed / blocked guardrails 各态显式分支,
// ContextPack 任一子块缺失跳过,Record<string,unknown> 经安全取值读取,不崩。

const OUTPUT_TYPES: StockV2OperationReviewOutputType[] = [
  "trade_signal",
  "proposed_operation",
  "strategy_patch",
  "ignore",
  "continue_monitoring",
];

const OPERATION_ACTIONS = ["buy", "sell", "reduce", "clear"] as const;

export function StockV2ReviewDrawer({
  actions,
  hitId,
  onClose,
}: {
  actions: AppActions;
  hitId: string | null;
  onClose: () => void;
}) {
  const [review, setReview] = useState<StockV2OperationReview | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "error" | "creating">("loading");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // 表单状态
  const [outputType, setOutputType] = useState<StockV2OperationReviewOutputType | "">("");
  const [status, setStatus] = useState("completed");
  const [resultSummary, setResultSummary] = useState("");
  // trade_signal
  const [direction, setDirection] = useState("");
  const [priceRange, setPriceRange] = useState("");
  const [triggerSummary, setTriggerSummary] = useState("");
  const [stopLoss, setStopLoss] = useState("");
  const [takeProfit, setTakeProfit] = useState("");
  // proposed_operation
  const [opAction, setOpAction] = useState<string>("");
  const [quantity, setQuantity] = useState("");
  const [price, setPrice] = useState("");
  const [amount, setAmount] = useState("");
  // strategy_patch
  const [patchSummary, setPatchSummary] = useState("");

  async function loadReview(id: string, mode: "initial" | "refresh") {
    if (mode === "initial") setPhase("loading");
    setError(null);
    try {
      const res = await actions.api<StockV2OperationReviewListResponse>(
        `/api/stockv2/reviews?hitId=${encodeURIComponent(id)}&limit=1`,
      );
      const found = res.items?.[0];
      if (!found) {
        if (mode === "initial") {
          setReview(null);
          setPhase("ready"); // 无 review,显示"进入 Review"入口
        }
        return;
      }
      const full = await actions.api<StockV2OperationReview>(`/api/stockv2/reviews/${found.id}`);
      setReview(full);
      hydrateForm(full);
      setPhase("ready");
    } catch (err) {
      setError(friendlyError(err));
      if (mode === "initial") setPhase("error");
    }
  }

  async function createReview() {
    if (!hitId) return;
    setPhase("creating");
    setError(null);
    try {
      const created = await actions.api<StockV2OperationReview>(
        `/api/stockv2/monitor/hits/${encodeURIComponent(hitId)}/review`,
        { method: "POST" },
      );
      // POST 可能返回已存在的 active review,补取完整 InputContext。
      const full = await actions.api<StockV2OperationReview>(`/api/stockv2/reviews/${created.id}`);
      setReview(full);
      hydrateForm(full);
      setPhase("ready");
    } catch (err) {
      setError(friendlyError(err));
      setPhase("error");
    }
  }

  function hydrateForm(r: StockV2OperationReview) {
    setOutputType((r.outputType as StockV2OperationReviewOutputType) || "");
    setStatus(r.status || "pending");
    setResultSummary(r.resultSummary || "");
    const result = r.result || {};
    if (r.outputType === "trade_signal") {
      const ts = mapFromAny(result.tradeSignal);
      setDirection(readStr(ts, "direction"));
      setPriceRange(readStr(ts, "priceRange"));
      setTriggerSummary(readStr(ts, "triggerSummary"));
      setStopLoss(readStr(ts, "stopLoss"));
      setTakeProfit(readStr(ts, "takeProfit"));
    } else if (r.outputType === "proposed_operation") {
      const op = mapFromAny(result.proposedOperation) || mapFromAny(result.operation) || {};
      setOpAction(readStr(op, "action") || readStr(op, "operation") || readStr(op, "type"));
      setQuantity(readStr(op, "quantity"));
      setPrice(readStr(op, "price"));
      setAmount(readStr(op, "amount"));
    } else if (r.outputType === "strategy_patch") {
      setPatchSummary(readStr(result, "patchSummary"));
    }
  }

  useEffect(() => {
    if (!hitId) {
      setReview(null);
      setPhase("loading");
      return;
    }
    void loadReview(hitId, "initial");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hitId]);

  function buildResult(): Record<string, unknown> {
    switch (outputType) {
      case "trade_signal":
        return {
          tradeSignal: {
            direction: direction.trim(),
            priceRange: priceRange.trim(),
            triggerSummary: triggerSummary.trim(),
            stopLoss: stopLoss.trim(),
            takeProfit: takeProfit.trim(),
          },
        };
      case "proposed_operation":
        return {
          proposedOperation: {
            action: opAction.trim(),
            quantity: quantity.trim(),
            price: price.trim(),
            amount: amount.trim(),
          },
        };
      case "strategy_patch":
        return { patchSummary: patchSummary.trim() };
      default:
        return {};
    }
  }

  async function save() {
    if (!review) return;
    if (!outputType) {
      actions.setToast("请选择结果类型", "danger");
      return;
    }
    setSubmitting(true);
    try {
      const body: StockV2OperationReviewResultInput = {
        outputType,
        result: buildResult(),
        resultSummary: resultSummary.trim(),
        status,
      };
      const updated = await actions.api<StockV2OperationReview>(
        `/api/stockv2/reviews/${review.id}/result`,
        { method: "PUT", body },
      );
      setReview(updated);
      hydrateForm(updated);
      actions.setToast("已保存 Review 结果", "good");
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  // ===== guardrails 区域(proposed_operation 保存后后端回填)=====
  const guardrails = review?.result ? mapFromAny(review.result.guardrails) : null;
  const guardrailsStatus = guardrails ? readStr(guardrails, "status") : "";
  const acceptanceStatus = review?.result ? readStr(review.result, "acceptanceStatus") : "";
  const guardrailReasons = guardrails && Array.isArray(guardrails.reasons) ? guardrails.reasons : [];
  const blocked = guardrailsStatus === "blocked";

  return (
    <Drawer
      title="操作复核 · Review"
      subtitle={review ? `状态 ${stockV2ReviewStatusLabel(review.status)}` : "从监控命中进入复核"}
      onClose={onClose}
      width={560}
      footer={
        review ? (
          <div className="flex justify-end gap-2">
            <Button onClick={onClose}>关闭</Button>
            <Button tone="primary" disabled={submitting || !outputType} onClick={() => void save()}>
              {submitting ? "保存中…" : "保存结果"}
            </Button>
          </div>
        ) : null
      }
    >
      <div className="grid gap-4 text-sm">
        {phase === "loading" ? (
          <p className="text-[var(--muted)]">加载 Review…</p>
        ) : null}

        {phase === "error" ? (
          <div className="grid gap-3">
            <Notice tone="danger">{error || "加载 Review 失败"}</Notice>
            <div className="flex gap-2">
              {hitId ? <Button onClick={() => void loadReview(hitId, "initial")}>重试</Button> : null}
              <Button onClick={onClose}>关闭</Button>
            </div>
          </div>
        ) : null}

        {phase === "ready" && !review ? (
          <div className="grid gap-3">
            <p className="text-[var(--muted-strong)]">
              该命中尚未进入复核。进入后将基于命中上下文(ContextPack)创建一条 Review 记录,可在其中填写结构化结果。
            </p>
            <div className="flex gap-2">
              <Button tone="primary" onClick={() => void createReview()}>
                进入 Review
              </Button>
              <Button onClick={onClose}>取消</Button>
            </div>
          </div>
        ) : null}

        {phase === "creating" ? <p className="text-[var(--muted)]">创建 Review…</p> : null}

        {phase === "ready" && review ? (
          <>
            <ReviewStatusRow review={review} />

            <CollapsibleSection title="上下文摘要" subtitle="ContextPack(hit / 策略 / 行情 / 日K / 组合)">
              <ContextPackSummary review={review} />
            </CollapsibleSection>

            <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <strong className="text-sm">结构化结果</strong>
              <Field label="结果类型" help="选择本次复核产出的结构化结论类型。">
                <select value={outputType} onChange={(e) => setOutputType(e.target.value as StockV2OperationReviewOutputType)}>
                  <option value="">(未选择)</option>
                  {OUTPUT_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {stockV2ReviewOutputTypeLabel(t)}
                    </option>
                  ))}
                </select>
              </Field>

              {outputType === "trade_signal" ? (
                <TradeSignalFields
                  direction={direction}
                  setDirection={setDirection}
                  priceRange={priceRange}
                  setPriceRange={setPriceRange}
                  triggerSummary={triggerSummary}
                  setTriggerSummary={setTriggerSummary}
                  stopLoss={stopLoss}
                  setStopLoss={setStopLoss}
                  takeProfit={takeProfit}
                  setTakeProfit={setTakeProfit}
                />
              ) : null}

              {outputType === "proposed_operation" ? (
                <ProposedOperationFields
                  opAction={opAction}
                  setOpAction={setOpAction}
                  quantity={quantity}
                  setQuantity={setQuantity}
                  price={price}
                  setPrice={setPrice}
                  amount={amount}
                  setAmount={setAmount}
                />
              ) : null}

              {outputType === "strategy_patch" ? (
                <Field label="补丁摘要" help="策略补丁的摘要说明;正式补丁需后续经策略版本流程接受。">
                  <textarea
                    rows={3}
                    value={patchSummary}
                    onChange={(e) => setPatchSummary(e.target.value)}
                    placeholder="补丁摘要"
                  />
                </Field>
              ) : null}

              {outputType === "ignore" ? (
                <p className="text-xs text-[var(--muted)]">标记为忽略:命中将置为已忽略,不产生结构化结论。</p>
              ) : null}
              {outputType === "continue_monitoring" ? (
                <p className="text-xs text-[var(--muted)]">继续监控:不产生操作,命中保持观察。</p>
              ) : null}

              <Field label="结果摘要" help="可选的人工说明,随结果一起保存。">
                <textarea rows={2} value={resultSummary} onChange={(e) => setResultSummary(e.target.value)} placeholder="结果摘要" />
              </Field>

              <Field label="Review 状态" help="保存后推进 Review 状态;completed/closed 会同步更新命中状态。">
                <select value={status} onChange={(e) => setStatus(e.target.value)}>
                  <option value="completed">已完成</option>
                  <option value="closed">已关闭</option>
                  <option value="failed">失败</option>
                  <option value="pending">待处理</option>
                </select>
              </Field>
            </div>

            {outputType === "proposed_operation" && guardrails ? (
              <GuardrailsBlock status={guardrailsStatus} acceptanceStatus={acceptanceStatus} reasons={guardrailReasons} />
            ) : null}

            {outputType === "proposed_operation" && !guardrails ? (
              <p className="text-xs text-[var(--muted)]">
                填写操作提案并保存后,后端将运行执行 guardrails 并在此展示结果。
              </p>
            ) : null}
          </>
        ) : null}
      </div>
    </Drawer>
  );
}

function ReviewStatusRow({ review }: { review: StockV2OperationReview }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Pill tone={stockV2ReviewStatusTone(review.status)}>{stockV2ReviewStatusLabel(review.status)}</Pill>
      {review.outputType ? <Pill tone="neutral">{stockV2ReviewOutputTypeLabel(review.outputType)}</Pill> : null}
      {review.symbol ? <Pill tone="neutral">{review.symbol}</Pill> : null}
      <span className="text-xs text-[var(--muted)]">创建 {formatDate(review.createdAt) || "-"}</span>
      <span className="text-xs text-[var(--muted)]">更新 {formatDate(review.updatedAt) || "-"}</span>
    </div>
  );
}

function ContextPackSummary({ review }: { review: StockV2OperationReview }) {
  const pack = review.inputContext;
  if (!pack) {
    return <p className="text-xs text-[var(--muted)]">无上下文摘要。</p>;
  }
  const items: Array<[string, React.ReactNode]> = [];
  const hit = pack.hit;
  if (hit) {
    if (hit.title) items.push(["命中", hit.title]);
    if (hit.symbol) items.push(["标的", hit.symbol]);
    if (hit.strategyId) items.push(["策略", hit.strategyId.slice(0, 8)]);
    if (hit.portfolioId) items.push(["组合", hit.portfolioId.slice(0, 8)]);
  }
  if (pack.quote) {
    items.push(["最新价", `${pack.quote.lastPrice}(${pack.quote.symbol})`]);
  }
  if (pack.dailyBars) {
    const bars = pack.dailyBars;
    items.push(["日K", `${bars.count ?? 0} 根 · 最新收 ${bars.latestClose ?? "-"}`]);
  }
  if (pack.portfolio) {
    const snap = pack.portfolio.snapshot;
    items.push(["组合快照", snap ? `估值 ${snap.valuationAt || "-"}` : "无快照"]);
  }
  if (pack.freshness && Object.keys(pack.freshness).length > 0) {
    items.push(["新鲜度", <span className="font-mono text-[11px]">{stringify(pack.freshness)}</span>]);
  }
  if (items.length === 0) {
    return <p className="text-xs text-[var(--muted)]">上下文摘要为空。</p>;
  }
  return (
    <div className="grid gap-3">
      <ContextList items={items} />
      <details className="rounded border border-[var(--line)] bg-[var(--surface)]">
        <summary className="cursor-pointer px-2 py-1 text-xs text-[var(--muted)]">原始 ContextPack JSON</summary>
        <pre className="max-h-56 overflow-auto px-2 py-2 text-[11px] text-[var(--muted-strong)]">{stringify(pack)}</pre>
      </details>
    </div>
  );
}

function TradeSignalFields(props: {
  direction: string;
  setDirection: (v: string) => void;
  priceRange: string;
  setPriceRange: (v: string) => void;
  triggerSummary: string;
  setTriggerSummary: (v: string) => void;
  stopLoss: string;
  setStopLoss: (v: string) => void;
  takeProfit: string;
  setTakeProfit: (v: string) => void;
}) {
  return (
    <div className="grid gap-3">
      <Field label="方向">
        <select value={props.direction} onChange={(e) => props.setDirection(e.target.value)}>
          <option value="">(未选择)</option>
          <option value="long">做多</option>
          <option value="short">做空</option>
          <option value="neutral">中性</option>
        </select>
      </Field>
      <Field label="价格区间">
        <input type="text" value={props.priceRange} onChange={(e) => props.setPriceRange(e.target.value)} placeholder="如 10.5-11.2" />
      </Field>
      <Field label="触发摘要">
        <input type="text" value={props.triggerSummary} onChange={(e) => props.setTriggerSummary(e.target.value)} />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="止损">
          <input type="text" value={props.stopLoss} onChange={(e) => props.setStopLoss(e.target.value)} />
        </Field>
        <Field label="止盈">
          <input type="text" value={props.takeProfit} onChange={(e) => props.setTakeProfit(e.target.value)} />
        </Field>
      </div>
      <p className="text-xs text-[var(--muted)]">
        交易信号仅作为人工参考产出,不会自动下单;实际操作需经操作提案与确认流程。
      </p>
    </div>
  );
}

function ProposedOperationFields(props: {
  opAction: string;
  setOpAction: (v: string) => void;
  quantity: string;
  setQuantity: (v: string) => void;
  price: string;
  setPrice: (v: string) => void;
  amount: string;
  setAmount: (v: string) => void;
}) {
  return (
    <div className="grid gap-3">
      <Field label="操作类型" help="buy 买入 / sell 卖出 / reduce 减仓 / clear 清仓。">
        <select value={props.opAction} onChange={(e) => props.setOpAction(e.target.value)}>
          <option value="">(未选择)</option>
          {OPERATION_ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      </Field>
      <div className="grid grid-cols-3 gap-3">
        <Field label="数量">
          <input type="text" value={props.quantity} onChange={(e) => props.setQuantity(e.target.value)} />
        </Field>
        <Field label="价格">
          <input type="text" value={props.price} onChange={(e) => props.setPrice(e.target.value)} />
        </Field>
        <Field label="金额">
          <input type="text" value={props.amount} onChange={(e) => props.setAmount(e.target.value)} />
        </Field>
      </div>
      <p className="text-xs text-[var(--muted)]">
        操作提案保存后将由后端执行 guardrails(标的/组合/现金/仓位/行情等确定性约束)校验,结果在下方展示。提案不直接执行,需后续确认。
      </p>
    </div>
  );
}

function GuardrailsBlock({
  status,
  acceptanceStatus,
  reasons,
}: {
  status: string;
  acceptanceStatus: string;
  reasons: unknown[];
}) {
  const tone = stockV2GuardrailsStatusTone(status);
  return (
    <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <ShieldCheck size={14} className="text-[var(--muted)]" />
        <strong>执行 Guardrails</strong>
        <Pill tone={tone}>{stockV2GuardrailsStatusLabel(status)}</Pill>
        {acceptanceStatus ? <Pill tone="neutral">{acceptanceStatusLabel(acceptanceStatus)}</Pill> : null}
      </div>
      {status === "blocked" ? (
        <Notice tone="danger">操作被 guardrails 拦截,不会执行。请修改提案后重新保存,或改选其他结果类型。</Notice>
      ) : null}
      {status === "degraded" ? (
        <Notice tone="warn">guardrails 降级:缺少部分上下文(快照/行情),提案仅作降级审阅,不直接执行。</Notice>
      ) : null}
      {reasons.length > 0 ? (
        <ul className="grid gap-1">
          {reasons.map((reason, idx) => {
            const r = mapFromAny(reason);
            const code = readStr(r, "code");
            const message = readStr(r, "message");
            const detail = r.detail;
            return (
              <li key={idx} className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1">
                <span className="font-mono text-[11px] text-[var(--muted-strong)]">{code || `#${idx + 1}`}</span>
                <span className="ml-2">{message || "(无说明)"}</span>
                {detail !== undefined && detail !== null ? (
                  <span className="ml-2 font-mono text-[11px] text-[var(--muted)]">{stringify(detail)}</span>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : status === "pass" ? (
        <p className="text-[var(--muted)]">无拦截项,提案通过确定性约束检查。</p>
      ) : null}
    </div>
  );
}

// ===== 安全取值工具(从 Record<string,unknown> / any 读取,不抛错)=====

function mapFromAny(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function readStr(map: Record<string, unknown>, ...keys: string[]): string {
  for (const k of keys) {
    const v = map[k];
    if (v !== undefined && v !== null && v !== "") {
      return typeof v === "object" ? stringify(v) : String(v);
    }
  }
  return "";
}

function stringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function acceptanceStatusLabel(status: string): string {
  switch (status) {
    case "blocked":
      return "已拦截";
    case "pending_guardrail_review":
      return "待 guardrail 复核";
    case "pending_confirmation":
      return "待确认";
    case "pending":
      return "待处理";
    default:
      return status;
  }
}
