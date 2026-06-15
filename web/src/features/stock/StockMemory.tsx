import { CheckCircle, GitBranch } from "@phosphor-icons/react";
import type { FormEvent } from "react";
import { useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockAgentAuthorization, StockStrategyPatch } from "../../app/types";
import { Button, ContextList, EmptyState, Field, Metric, Notice, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { money, number, numberText, operationLabel, price, snippet, text } from "./format";

type PatchConfirm = { id: string; action: "accept" | "reject" };
type AuthorizationConfirm = { id: string; action: "approve" | "deny" };

export function StockMemory({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const runs = data.stock.agentRuns || [];
  const authorizations = data.stock.agentAuthorizations || [];
  const pendingAuthorizations = authorizations.filter((item) => item.status === "pending");
  const [selectedRunId, setSelectedRunId] = useState("");
  const [patchConfirm, setPatchConfirm] = useState<PatchConfirm | null>(null);
  const [authorizationConfirm, setAuthorizationConfirm] = useState<AuthorizationConfirm | null>(null);
  const selectedRun = useMemo(() => runs.find((run) => run.id === selectedRunId) || runs[0], [runs, selectedRunId]);
  const steps = (data.stock.agentSteps || []).filter((step) => !selectedRun || step.runId === selectedRun.id);
  const claims = (data.stock.agentClaims || []).filter((claim) => !selectedRun || claim.runId === selectedRun.id);
  const patches = data.stock.strategyPatches || [];
  const graph = selectedRun ? safeJSON<{ nodes?: Array<Record<string, unknown>>; edges?: Array<Record<string, unknown>> }>(selectedRun.runGraphJson) : {};

  async function saveAgentProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    await runAction("已保存 Agent 模型配置", async () => {
      await actions.api("/api/stock/agent/model-profiles", {
        method: "POST",
        body: {
          name: text(form, "name"),
          provider: text(form, "provider") || "system",
          model: text(form, "model") || "rule-engine",
          taskType: text(form, "taskType") || "review",
          decisionProtocol: text(form, "decisionProtocol") || "single_review",
          authMode: text(form, "authMode") || "none",
          enabled: form.get("enabled") === "on",
          temperature: number(form, "temperature"),
          dailyTokenBudget: number(form, "dailyTokenBudget"),
          dailyCostBudget: number(form, "dailyCostBudget"),
          status: text(form, "status") || (form.get("enabled") === "on" ? "available" : "disabled"),
        },
      });
      formElement.reset();
    });
  }

  async function acceptPatch(patch: StockStrategyPatch) {
    await runAction("已接受策略补丁", async () => {
      await actions.api(`/api/stock/strategy-patches/${patch.id}/accept`, { method: "POST", body: {} });
      setPatchConfirm(null);
    });
  }

  async function rejectPatch(patch: StockStrategyPatch) {
    await runAction("已拒绝策略补丁", async () => {
      await actions.api(`/api/stock/strategy-patches/${patch.id}/reject`, { method: "POST", body: {} });
      setPatchConfirm(null);
    });
  }

  async function approveAuthorization(auth: StockAgentAuthorization) {
    await runAction("已确认执行外部 Agent", async () => {
      await actions.api(`/api/stock/agent/authorizations/${auth.id}/approve`, { method: "POST", body: {} });
      setAuthorizationConfirm(null);
    });
  }

  async function denyAuthorization(auth: StockAgentAuthorization) {
    await runAction("已拒绝执行外部 Agent", async () => {
      await actions.api(`/api/stock/agent/authorizations/${auth.id}/deny`, { method: "POST", body: { reason: "user_denied_from_stock_workbench" } });
      setAuthorizationConfirm(null);
    });
  }

  async function cleanupLedger() {
    await runAction("已清理 Agent Decision Ledger 旧记录", async () => {
      await actions.api("/api/stock/agent/ledger/cleanup", { method: "POST", body: { retentionDays: 30, keepRuns: 500 } });
    });
  }

  return (
    <div className="grid gap-4">
      <section className="grid grid-cols-4 gap-3 max-2xl:grid-cols-2 max-sm:grid-cols-1">
        <Metric label="Agent Runs" value={data.stock.agentTrace?.runCount || 0} detail={data.stock.agentTrace?.lastRunAt ? formatDate(data.stock.agentTrace.lastRunAt) : "暂无运行"} tone={data.stock.agentTrace?.runCount ? "good" : "neutral"} />
        <Metric label="Claim Ledger" value={data.stock.agentTrace?.claimCount || 0} detail="触发、行情、策略、账户和 guardrails" />
        <Metric label="待确认策略补丁" value={data.stock.agentTrace?.pendingPatchCount || 0} detail="人工接受后才更新正式策略" tone={data.stock.agentTrace?.pendingPatchCount ? "warn" : "neutral"} />
        <Metric label="Model Profiles" value={data.stock.agentProfiles?.length || 0} detail={`${(data.stock.agentProfiles || []).filter((item) => item.enabled).length} enabled`} />
      </section>

      <Notice>
        当前 bull_reviewer、bear_reviewer、portfolio_constraint_reviewer 是确定性审计 step，用于结构化留痕和约束检查；真实多 Agent 辩论是后续 Feature，可在同一运行子图中接入多个执行者并保留各自记录。
      </Notice>

      {pendingAuthorizations.length ? (
        <Panel title="待确认 Agent 执行" subtitle="confirm_required profile 会先停在股票模块授权边界，确认后才启动外部 executor。">
          <div className="grid gap-2">
            {pendingAuthorizations.map((auth) => {
              const confirming = authorizationConfirm?.id === auth.id ? authorizationConfirm.action : "";
              return (
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--warn)]/35 bg-[var(--surface-soft)] p-3 text-sm" key={auth.id}>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="mono">{auth.symbol || "-"}</strong>
                      <Pill tone="warn">{auth.status || "pending"}</Pill>
                      <Pill>{auth.decisionProtocol || "single_review"}</Pill>
                      <span className="muted text-xs">{formatDate(auth.createdAt)}</span>
                    </div>
                    <p className="muted mt-2 mb-0 leading-relaxed">{auth.reason || "等待确认是否执行外部 Agent。"}</p>
                    <span className="mono mt-2 block truncate text-xs text-[var(--muted-strong)]">{auth.provider || "-"} / {auth.model || "-"} / {auth.profileId || "-"}</span>
                  </div>
                  {confirming ? (
                    <div className="grid min-w-56 content-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2">
                      <span className="text-xs text-[var(--muted-strong)]">{confirming === "approve" ? "确认后会启动本机外部 Agent executor。" : "拒绝后本次 Review 保留 system guardrails 结果。"}</span>
                      <div className="flex justify-end gap-2">
                        <Button tone={confirming === "approve" ? "primary" : "danger"} onClick={() => confirming === "approve" ? void approveAuthorization(auth) : void denyAuthorization(auth)}>
                          {confirming === "approve" ? "确认启动" : "确认拒绝"}
                        </Button>
                        <Button onClick={() => setAuthorizationConfirm(null)}>返回</Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex items-center justify-end gap-2">
                      <Button tone="primary" onClick={() => setAuthorizationConfirm({ id: auth.id, action: "approve" })}>允许执行</Button>
                      <Button onClick={() => setAuthorizationConfirm({ id: auth.id, action: "deny" })}>拒绝</Button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </Panel>
      ) : null}

      <div className="grid grid-cols-[minmax(0,1fr)_420px] gap-4 max-2xl:grid-cols-1">
        <Panel title="Agent 运行留痕" subtitle="保存 Prompt、输入输出、模型 profile、成本摘要和运行子图。">
          <div className="mb-3 flex justify-end">
            <Button onClick={() => void cleanupLedger()}>清理旧 Ledger</Button>
          </div>
          <div className="grid gap-2">
            {runs.map((run) => (
              <button
                className={`grid gap-2 rounded-lg border p-3 text-left text-sm transition ${selectedRun?.id === run.id ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--surface-soft)] hover:border-[var(--line-strong)]"}`}
                key={run.id}
                onClick={() => setSelectedRunId(run.id)}
                type="button"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <strong className="mono">{run.symbol || "-"}</strong>
                  <Pill tone={run.status === "completed" ? "good" : run.status === "failed" ? "danger" : "neutral"}>{run.status || "unknown"}</Pill>
                  <Pill>{run.decisionProtocol || "single_review"}</Pill>
                  <span className="muted text-xs">{formatDate(run.completedAt || run.createdAt)}</span>
                </div>
                <span className="muted text-xs leading-relaxed">{run.summary || "未记录摘要"}</span>
                <span className="mono text-xs text-[var(--muted-strong)]">{run.provider || "-"} / {run.model || "-"}</span>
              </button>
            ))}
            {!runs.length ? <EmptyState body="从提醒执行 Review 后，会同时生成 Agent Decision Ledger。" title="暂无 Agent 运行" /> : null}
          </div>
        </Panel>

        <Panel title="运行子图" subtitle={selectedRun ? `${selectedRun.id} / ${selectedRun.result || "unknown"}` : "选择一次 Agent run 查看"}>
          {selectedRun ? (
            <div className="grid gap-3">
              <ContextList
                items={[
                  ["Protocol", selectedRun.decisionProtocol || "-"],
                  ["Profile", selectedRun.modelProfileId || "-"],
                  ["Result", selectedRun.result || "-"],
                  ["Confidence", selectedRun.confidence || "-"],
                  ["Redaction", selectedRun.redactionSummary || "-"],
                ]}
              />
              <div className="grid gap-2">
                <strong className="text-xs text-[var(--muted-strong)]">Nodes</strong>
                <div className="flex flex-wrap gap-2">
                  {(graph.nodes || []).map((node, index) => (
                    <Pill key={`${String(node.id || "node")}-${index}`} tone={String(node.status || "") === "missing" ? "warn" : "neutral"}>{String(node.label || node.id || "node")}</Pill>
                  ))}
                  {!graph.nodes?.length ? <Pill>graph empty</Pill> : null}
                </div>
              </div>
              <div className="grid gap-2">
                <strong className="text-xs text-[var(--muted-strong)]">Steps</strong>
                {steps.map((step) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-xs" key={step.id}>
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="mono">{step.stepKey}</strong>
                      <Pill tone={step.status === "completed" ? "good" : step.status === "failed" ? "danger" : "neutral"}>{step.status || "unknown"}</Pill>
                    </div>
                    <span className="muted mt-1 block leading-relaxed">{step.summary}</span>
                  </div>
                ))}
              </div>
              <details className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
                <summary className="cursor-pointer font-medium">Prompt / 输入 / 输出快照</summary>
                <pre className="mono mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3">{snippet(`Prompt:\n${selectedRun.promptSnapshot || "-"}\n\nInput:\n${selectedRun.inputSnapshot || "-"}\n\nOutput:\n${selectedRun.outputSnapshot || "-"}`, 6000)}</pre>
              </details>
            </div>
          ) : <EmptyState body="执行 Review 后会产生可查看的运行子图。" title="暂无子图" />}
        </Panel>
      </div>

      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
        <Panel title="Claim Ledger" subtitle="每条 claim 都带来源引用和验证状态，便于审计输入是否可靠。">
          <div className="grid gap-2">
            {claims.map((claim) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={claim.id}>
                <div className="flex flex-wrap items-center gap-2">
                  <strong>{claim.claimType || "claim"}</strong>
                  <Pill tone={claim.verificationStatus === "verified" ? "good" : claim.verificationStatus === "missing" ? "warn" : "neutral"}>{claim.verificationStatus || "unverified"}</Pill>
                  <Pill>{claim.confidence || "medium"}</Pill>
                </div>
                <p className="muted mt-2 mb-0 leading-relaxed">{claim.text}</p>
                <span className="mono mt-2 block text-xs text-[var(--muted-strong)]">{claim.sourceRef || "-"}</span>
              </div>
            ))}
            {!claims.length ? <EmptyState body="选择已有 Agent run 后，会显示它写入的证据 claim。" title="暂无 claim" /> : null}
          </div>
        </Panel>

        <Panel title="策略补丁确认" subtitle="Agent 只产出 pending patch，人工接受后才创建新策略版本。">
          <div className="grid gap-2">
            {patches.map((patch) => {
              const confirming = patchConfirm?.id === patch.id ? patchConfirm.action : "";
              return (
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={patch.id}>
                  <div className="flex flex-wrap items-center gap-2">
                    <strong className="mono">{patch.strategyId}</strong>
                    <Pill tone={patch.status === "pending_acceptance" ? "warn" : patch.status === "accepted" ? "good" : "neutral"}>{patch.status || "pending"}</Pill>
                    <span className="muted text-xs">{formatDate(patch.updatedAt || patch.createdAt)}</span>
                  </div>
                  <p className="muted mt-2 mb-0 leading-relaxed">{patch.summary}</p>
                  <pre className="mono mt-2 max-h-28 overflow-auto whitespace-pre-wrap rounded-md border border-[var(--line)] bg-[var(--surface)] p-2 text-xs">{snippet(patch.patchJson, 1200)}</pre>
                  {patch.status === "pending_acceptance" ? (
                    confirming ? (
                      <div className="mt-3 grid gap-2 rounded-lg border border-[var(--warn)]/30 bg-[var(--surface)] p-3">
                        <span className="text-xs text-[var(--muted-strong)]">{confirming === "accept" ? "接受后会创建新的正式策略版本。" : "拒绝后该补丁会退出待处理列表。"}</span>
                        <div className="flex flex-wrap gap-2">
                          <Button tone={confirming === "accept" ? "primary" : "danger"} onClick={() => confirming === "accept" ? void acceptPatch(patch) : void rejectPatch(patch)}>
                            {confirming === "accept" ? "确认接受" : "确认拒绝"}
                          </Button>
                          <Button onClick={() => setPatchConfirm(null)}>返回</Button>
                        </div>
                      </div>
                    ) : (
                      <div className="mt-3 flex flex-wrap gap-2">
                        <Button tone="primary" onClick={() => setPatchConfirm({ id: patch.id, action: "accept" })}><CheckCircle size={15} />接受补丁</Button>
                        <Button onClick={() => setPatchConfirm({ id: patch.id, action: "reject" })}>拒绝</Button>
                      </div>
                    )
                  ) : null}
                </div>
              );
            })}
            {!patches.length ? <EmptyState body="Agent Review 会生成等待确认的策略补丁。" title="暂无策略补丁" /> : null}
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)] gap-4 max-xl:grid-cols-1">
        <Panel title="Agent / Model Profile" subtitle="system profile 是本地规则 fallback；codex_cli profile 启用后会以 read-only codex exec 作为 Review 辅助执行者，并把结果写入运行子图。">
          <form className="grid gap-3" onSubmit={(event) => void saveAgentProfile(event)}>
            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="名称"><input className="input" name="name" placeholder="System Rule Trace" /></Field>
              <Field label="Provider"><input className="input mono" name="provider" placeholder="system" /></Field>
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(220px,1.2fr)] gap-3 max-md:grid-cols-1">
              <Field label="Model"><input className="input mono" name="model" placeholder="rule-engine" /></Field>
              <Field label="Task type">
                <select className="select min-w-0" name="taskType" defaultValue="review">
                  <option value="review">review</option>
                  <option value="debate">debate</option>
                </select>
              </Field>
              <Field label="Protocol">
                <select className="select min-w-0" name="decisionProtocol" defaultValue="single_review">
                  <option value="single_review">single</option>
                  <option value="analysis_with_challenge">challenge</option>
                  <option value="portfolio_constrained_debate">portfolio</option>
                </select>
              </Field>
            </div>
            <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-md:grid-cols-1">
              <Field label="Auth mode">
                <select className="select" name="authMode" defaultValue="none">
                  <option value="none">none</option>
                  <option value="user_config">user_config</option>
                  <option value="confirm_required">confirm_required</option>
                  <option value="disabled">disabled</option>
                </select>
              </Field>
              <Field label="Temperature"><input className="input" defaultValue="0" min="0" name="temperature" step="0.1" type="number" /></Field>
              <Field label="Daily tokens"><input className="input" min="0" name="dailyTokenBudget" step="1000" type="number" /></Field>
              <Field label="Status"><input className="input mono" name="status" placeholder="available" /></Field>
            </div>
            <label className="flex items-center gap-2 text-sm text-[var(--muted-strong)]">
              <input name="enabled" type="checkbox" defaultChecked />
              启用此 profile（codex_cli 需要本机 Codex CLI 可用；失败会降级并写入 executor step）
            </label>
            <div><Button tone="primary" type="submit"><GitBranch size={15} />保存 Profile</Button></div>
          </form>
          <div className="mt-4 grid gap-2">
            {(data.stock.agentProfiles || []).map((profile) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs" key={profile.id || `${profile.provider}-${profile.model}-${profile.decisionProtocol}`}>
                <div className="flex flex-wrap items-center gap-2">
                  <strong>{profile.name || profile.model}</strong>
                  <Pill tone={profile.enabled ? "good" : "neutral"}>{profile.enabled ? "enabled" : "disabled"}</Pill>
                  <Pill>{profile.decisionProtocol || "single_review"}</Pill>
                </div>
                <span className="mono muted mt-2 block">{profile.provider || "-"} / {profile.model || "-"} / {profile.authMode || "none"}</span>
              </div>
            ))}
          </div>
        </Panel>

        <Panel title="信号、操作与记忆回流">
          <div className="grid gap-2">
            {(data.stock.tradeSignals || []).slice(0, 8).map((signal) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={signal.id}>
                <div className="flex flex-wrap items-center gap-2"><strong>{signal.symbol}</strong><Pill>{signal.direction || "watch"}</Pill><Pill>{signal.status || "active"}</Pill></div>
                <p className="muted mt-2 mb-0">{signal.triggerSummary || signal.priceRange}</p>
              </div>
            ))}
            {(data.stock.operations || []).slice(0, 8).map((operation) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={operation.id}>
                <div className="flex flex-wrap items-center gap-2"><strong>{operation.symbol}</strong><Pill>{operationLabel(operation.action)}</Pill></div>
                <p className="muted mt-2 mb-0">数量 {numberText(operation.quantity)} / 成交价 {price(operation.price)} / 金额 {money(operation.amount)}</p>
              </div>
            ))}
            {(data.stock.memories || []).slice(0, 10).map((memory) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs" key={memory.id}>
                <div className="flex flex-wrap items-center gap-2">
                  <strong className="text-sm">{memory.symbol || memory.objectType}</strong>
                  <Pill tone={memory.objectType === "agent_run" ? "good" : "neutral"}>{memory.objectType || "memory"}</Pill>
                </div>
                <span className="muted mt-1 block leading-relaxed">{memory.summary}</span>
              </div>
            ))}
            {!data.stock.tradeSignals?.length && !data.stock.operations?.length && !data.stock.memories?.length ? <EmptyState body="Review、确认操作和 Agent trace 都会回写到这里。" title="暂无记忆" /> : null}
          </div>
        </Panel>
      </div>
    </div>
  );
}

function safeJSON<T>(value?: string): T {
  if (!value) return {} as T;
  try {
    return JSON.parse(value) as T;
  } catch {
    return {} as T;
  }
}
