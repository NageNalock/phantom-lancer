import { CheckCircle, Play, Plus, ShieldCheck } from "@phosphor-icons/react";
import type { FormEvent } from "react";
import { useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockAlert, StockProposedOperation } from "../../app/types";
import { Button, EmptyState, Field, Notice, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { durationText, money, number, numberText, operationLabel, optionalNumber, percent, price, text } from "./format";

export function StockWatchReview({ actions, data, openAlerts, pendingOperations, runAction }: { actions: AppActions; data: AppData; openAlerts: StockAlert[]; pendingOperations: StockProposedOperation[]; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const watches = data.stock.watches || [];
  const [confirmingId, setConfirmingId] = useState("");
  const [cancelingId, setCancelingId] = useState("");

  async function saveQuote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await runAction("已保存行情快照", async () => {
      await actions.api("/api/stock/quotes", {
        method: "POST",
        body: {
          symbol: text(form, "symbol"),
          market: text(form, "market"),
          name: text(form, "name"),
          lastPrice: number(form, "lastPrice"),
          previousClose: number(form, "previousClose"),
          volume: number(form, "volume"),
          amount: number(form, "amount"),
          dataTimestamp: new Date().toISOString(),
          dataFreshness: text(form, "dataFreshness") || "fresh",
          tradableStatus: text(form, "tradableStatus") || "tradable",
        },
      });
      event.currentTarget.reset();
    });
  }

  async function checkWatches() {
    await runAction("盯盘检查完成", async () => {
      await actions.api("/api/stock/watches/check", { method: "POST", body: { force: true } });
    });
  }

  async function updateWatch(watchId: string, body: Record<string, unknown>, label: string) {
    await runAction(label, async () => {
      await actions.api(`/api/stock/watches/${watchId}`, { method: "PATCH", body });
    });
  }

  async function editWatch(event: FormEvent<HTMLFormElement>, watchId: string) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await updateWatch(watchId, {
      status: text(form, "status"),
      checkIntervalSeconds: number(form, "checkIntervalSeconds"),
      cooldownSeconds: number(form, "cooldownSeconds"),
      triggerPriceAbove: optionalNumber(form, "triggerPriceAbove"),
      triggerPriceBelow: optionalNumber(form, "triggerPriceBelow"),
    }, "已更新盯盘");
  }

  async function reviewAlert(alert: StockAlert) {
    await runAction("Review 已生成", async () => {
      await actions.api(`/api/stock/alerts/${alert.id}/review`, { method: "POST", body: {} });
    });
  }

  async function ignoreAlert(alert: StockAlert) {
    await runAction("已忽略提醒", async () => {
      await actions.api(`/api/stock/alerts/${alert.id}`, { method: "PATCH", body: { status: "ignored" } });
    });
  }

  async function acknowledgeAlert(alert: StockAlert) {
    await runAction("已确认提醒", async () => {
      await actions.api(`/api/stock/alerts/${alert.id}`, { method: "PATCH", body: { status: "acknowledged" } });
    });
  }

  async function snoozeAlert(alert: StockAlert, seconds = 30 * 60) {
    await runAction("已稍后提醒", async () => {
      await actions.api(`/api/stock/alerts/${alert.id}`, { method: "PATCH", body: { status: "snoozed", snoozeSeconds: seconds } });
    });
  }

  async function confirmOperation(event: FormEvent<HTMLFormElement>, op: StockProposedOperation) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await runAction("已确认人工操作", async () => {
      await actions.api(`/api/stock/proposed-operations/${op.id}/confirm`, {
        method: "POST",
        body: {
          price: number(form, "price"),
          quantity: number(form, "quantity"),
          notes: text(form, "notes") || "从股票工作台确认",
          riskAcknowledged: form.get("riskAcknowledged") === "on",
          expectedAction: op.action || "",
          expectedSymbol: op.symbol || "",
          expectedGuardrail: op.guardrailResult || "",
          expectedRiskSummary: op.guardrailReason || "ok",
          confirmedReferenceAt: new Date().toISOString(),
          maxQuoteAgeSeconds: 15 * 60,
        },
      });
      setConfirmingId("");
    });
  }

  async function cancelOperation(op: StockProposedOperation) {
    await runAction("已作废操作建议", async () => {
      await actions.api(`/api/stock/proposed-operations/${op.id}/cancel`, {
        method: "POST",
        body: { reason: "用户在股票工作台作废" },
      });
      setCancelingId("");
    });
  }

  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 max-md:grid-cols-1">
        <Notice>后台盯盘每 30 秒在 A 股连续竞价时段检查一次；手动运行会强制检查价格条件，非交易时段仅用于人工验证。</Notice>
        <Button tone="primary" onClick={() => void checkWatches()}><Play size={15} />运行检查</Button>
      </div>
      <Panel title="盯盘任务" subtitle="展示状态、间隔、冷却和最近检查时间，创建策略后的对象会保留在这里。">
        <div className="grid gap-2">
          {watches.map((watch) => {
            const relatedAlerts = (data.stock.alerts || []).filter((alert) => alert.watchId === watch.id && (alert.status === "new" || alert.status === "acknowledged" || alert.status === "snoozed"));
            return (
              <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={watch.id}>
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 max-md:grid-cols-1">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <strong><span className="mono">{watch.symbol}</span> {watch.name}</strong>
                      <Pill tone={watch.status === "active" ? "good" : "neutral"}>{watch.status || "unknown"}</Pill>
                      {relatedAlerts.length ? <Pill tone="warn">{relatedAlerts.length} open</Pill> : null}
                    </div>
                    <p className="muted mt-2 mb-0 text-sm">
                      间隔 {durationText(watch.checkIntervalSeconds)} / 冷却 {durationText(watch.cooldownSeconds)} / 最后检查 {watch.lastCheckedAt ? formatDate(watch.lastCheckedAt) : "尚未检查"}
                    </p>
                  </div>
                  <div className="flex flex-wrap justify-end gap-2 text-xs">
                    {watch.triggerPriceAbove ? <Pill>上穿 {price(watch.triggerPriceAbove)}</Pill> : null}
                    {watch.triggerPriceBelow ? <Pill>下破 {price(watch.triggerPriceBelow)}</Pill> : null}
                    <Button onClick={() => void updateWatch(watch.id, { status: watch.status === "active" ? "paused" : "active" }, watch.status === "active" ? "已暂停盯盘" : "已恢复盯盘")}>
                      {watch.status === "active" ? "暂停" : "恢复"}
                    </Button>
                  </div>
                </div>
                <form className="grid grid-cols-[120px_120px_120px_120px_120px_auto] items-end gap-2 max-xl:grid-cols-3 max-sm:grid-cols-1" onSubmit={(event) => void editWatch(event, watch.id)}>
                  <Field label="状态">
                    <select className="select" name="status" defaultValue={watch.status || "active"}>
                      <option value="active">active</option>
                      <option value="paused">paused</option>
                      <option value="archived">archived</option>
                    </select>
                  </Field>
                  <Field label="间隔(s)"><input className="input" min="30" name="checkIntervalSeconds" step="30" type="number" defaultValue={watch.checkIntervalSeconds || 30} /></Field>
                  <Field label="冷却(s)"><input className="input" min="60" name="cooldownSeconds" step="60" type="number" defaultValue={watch.cooldownSeconds || 900} /></Field>
                  <Field label="上穿"><input className="input" min="0" name="triggerPriceAbove" step="0.001" type="number" defaultValue={watch.triggerPriceAbove || ""} /></Field>
                  <Field label="下破"><input className="input" min="0" name="triggerPriceBelow" step="0.001" type="number" defaultValue={watch.triggerPriceBelow || ""} /></Field>
                  <Button type="submit">保存</Button>
                </form>
              </div>
            );
          })}
          {!watches.length ? <EmptyState body="在策略页创建盯盘后，任务状态、检查间隔和触发条件会显示在这里。" title="暂无盯盘任务" /> : null}
        </div>
      </Panel>
      <Panel title="行情快照">
        <form className="grid grid-cols-8 gap-3 max-2xl:grid-cols-4 max-md:grid-cols-1" onSubmit={(event) => void saveQuote(event)}>
          <Field label="代码"><input className="input mono" name="symbol" required /></Field>
          <Field label="市场"><input className="input mono" name="market" defaultValue="SH" /></Field>
          <Field label="名称"><input className="input" name="name" /></Field>
          <Field label="最新价"><input className="input" min="0" name="lastPrice" required step="0.001" type="number" /></Field>
          <Field label="昨收"><input className="input" min="0" name="previousClose" step="0.001" type="number" /></Field>
          <Field label="新鲜度">
            <select className="select" name="dataFreshness" defaultValue="fresh">
              <option value="fresh">fresh</option>
              <option value="stale">stale</option>
              <option value="unknown">unknown</option>
            </select>
          </Field>
          <Field label="交易状态">
            <select className="select" name="tradableStatus" defaultValue="tradable">
              <option value="tradable">tradable</option>
              <option value="halted">halted</option>
              <option value="limit_up">limit_up</option>
              <option value="limit_down">limit_down</option>
              <option value="unknown">unknown</option>
            </select>
          </Field>
          <div className="flex items-end"><Button tone="primary" type="submit"><Plus size={15} />保存</Button></div>
        </form>
      </Panel>
      <Panel title="提醒 / Review">
        <div className="grid gap-2">
          {openAlerts.map((alert) => (
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-md:grid-cols-1" key={alert.id}>
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <strong>{alert.title || alert.symbol}</strong>
                  <Pill tone={alert.level === "urgent" || alert.level === "strong" ? "warn" : "neutral"}>{alert.level || "info"}</Pill>
                  <Pill>{alert.status || "new"}</Pill>
                </div>
                <p className="muted mt-2 mb-0 text-sm">{alert.summary}</p>
                {alert.cooldownUntil ? <p className="muted mt-1 mb-0 text-xs">冷却至 {formatDate(alert.cooldownUntil)}</p> : null}
              </div>
              <div className="flex flex-wrap justify-end gap-2">
                <Button onClick={() => void reviewAlert(alert)}><ShieldCheck size={15} />Review</Button>
                {alert.status === "new" ? <Button onClick={() => void acknowledgeAlert(alert)}>确认</Button> : null}
                <Button onClick={() => void snoozeAlert(alert)}>稍后</Button>
                <Button onClick={() => void ignoreAlert(alert)}>忽略</Button>
              </div>
            </div>
          ))}
          {!openAlerts.length ? <EmptyState body="保存行情并运行检查后，命中的盯盘会进入 Alert Ledger。" title="暂无待处理提醒" /> : null}
        </div>
      </Panel>
      <Panel title="操作建议">
        <div className="grid gap-2">
          {pendingOperations.map((op) => (
            <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={op.id}>
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <strong><span className="mono">{op.symbol}</span> {operationLabel(op.action)}</strong>
                  <Pill tone={op.guardrailResult === "passed" ? "good" : "warn"}>{op.guardrailResult || "unknown"}</Pill>
                </div>
                <p className="muted mt-2 mb-0 text-sm">数量 {numberText(op.quantity)} / 参考价 {price(op.price)} / 金额 {money(op.amount)} / 目标仓位 {percent(op.targetPositionPct)}</p>
                {op.guardrailReason ? <p className="muted mt-1 mb-0 text-xs">风险摘要: {op.guardrailReason}</p> : null}
              </div>
              {confirmingId === op.id ? (
                <form className="grid grid-cols-[120px_120px_minmax(0,1fr)_auto] items-end gap-2 max-lg:grid-cols-2 max-sm:grid-cols-1" onSubmit={(event) => void confirmOperation(event, op)}>
                  <Field label="成交价"><input className="input" defaultValue={op.price || 0} min="0" name="price" required step="0.001" type="number" /></Field>
                  <Field label="成交数量"><input className="input" defaultValue={op.quantity || 0} min="0" name="quantity" required step="1" type="number" /></Field>
                  <Field label="备注"><input className="input" name="notes" defaultValue="从股票工作台确认" /></Field>
                  <label className="flex min-h-9 items-center gap-2 text-xs text-[var(--muted)]">
                    <input name="riskAcknowledged" required type="checkbox" />
                    已核对账户权限、行情新鲜度和可交易状态
                  </label>
                  <div className="flex gap-2">
                    <Button disabled={op.guardrailResult !== "passed"} tone="primary" type="submit"><CheckCircle size={15} />提交</Button>
                    <Button type="button" onClick={() => setConfirmingId("")}>取消</Button>
                  </div>
                </form>
              ) : cancelingId === op.id ? (
                <div className="grid gap-2 rounded-lg border border-[var(--warn)]/30 bg-[var(--surface)] p-3">
                  <span className="text-xs text-[var(--muted-strong)]">作废后会关闭这条操作建议，并推进关联提醒状态。请确认这不是要继续跟踪的操作。</span>
                  <div className="flex flex-wrap gap-2">
                    <Button tone="danger" onClick={() => void cancelOperation(op)}>确认作废</Button>
                    <Button onClick={() => setCancelingId("")}>返回</Button>
                  </div>
                </div>
              ) : (
                <div className="flex flex-wrap gap-2">
                  <Button disabled={op.guardrailResult !== "passed"} tone="primary" onClick={() => setConfirmingId(op.id)}><CheckCircle size={15} />确认记录</Button>
                  <Button onClick={() => setCancelingId(op.id)}>作废</Button>
                </div>
              )}
            </div>
          ))}
          {!pendingOperations.length ? <EmptyState body="只有账户绑定策略且通过 guardrails 后，才会出现待确认操作。" title="暂无待确认操作" /> : null}
        </div>
      </Panel>
      <Panel title="最近 Review">
        <div className="grid gap-2">
          {(data.stock.reviews || []).slice(0, 8).map((review) => (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={review.id}>
              <div className="flex flex-wrap items-center gap-2">
                <strong>{review.symbol}</strong>
                <Pill tone={review.status === "completed" ? "good" : review.status === "guardrail_checking" ? "warn" : "neutral"}>{review.status || "unknown"}</Pill>
                <Pill>{review.reviewResult || "unknown"}</Pill>
                <Pill tone={review.guardrailResult === "passed" ? "good" : review.guardrailResult === "blocked" ? "warn" : "neutral"}>{review.guardrailResult || "n/a"}</Pill>
              </div>
              <p className="muted mt-2 mb-0">{review.summary}</p>
            </div>
          ))}
          {!data.stock.reviews?.length ? <EmptyState body="从提醒执行 Review 后会在这里留底。" title="暂无 Review" /> : null}
        </div>
      </Panel>
    </div>
  );
}
