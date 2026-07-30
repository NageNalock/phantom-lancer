import { Plus, Trash } from "@phosphor-icons/react";
import type {
  StockV2MonitorTask,
  StockV2StrategyActionRule,
  StockV2StrategyActionType,
  StockV2StrategyPlaybook,
  StockV2StrategyPrefilter,
} from "../../app/types";
import { Button, CollapsibleSection, Field, Pill } from "../../components/ui";
import { formatDate, stockV2MonitorRunStatusLabel } from "../../domain/labels";

export const PLAYBOOK_META_KEY = "playbook";

const ACTIONS: Array<{ value: StockV2StrategyActionType; label: string }> = [
  { value: "observe", label: "观察" },
  { value: "build_position", label: "建仓" },
  { value: "add_position", label: "加仓" },
  { value: "hold", label: "持有" },
  { value: "reduce_position", label: "减仓" },
  { value: "exit_position", label: "清仓" },
];

const PREFILTERS: Array<{ value: string; label: string; bucket: "dataPrefilters" | "portfolioPrefilters" }> = [
  { value: "price_above", label: "价格高于", bucket: "dataPrefilters" },
  { value: "price_below", label: "价格低于", bucket: "dataPrefilters" },
  { value: "price_between", label: "价格区间", bucket: "dataPrefilters" },
  { value: "pct_change_above", label: "涨跌幅高于", bucket: "dataPrefilters" },
  { value: "pct_change_below", label: "涨跌幅低于", bucket: "dataPrefilters" },
  { value: "daily_close_above", label: "日收盘高于", bucket: "dataPrefilters" },
  { value: "daily_close_below", label: "日收盘低于", bucket: "dataPrefilters" },
  { value: "quote_stale", label: "行情过期", bucket: "dataPrefilters" },
  { value: "portfolio_symbol_weight_above", label: "仓位高于", bucket: "portfolioPrefilters" },
  { value: "portfolio_symbol_weight_below", label: "仓位低于", bucket: "portfolioPrefilters" },
];

export function PlaybookEditor({
  rules,
  onChange,
}: {
  rules: StockV2StrategyActionRule[];
  onChange: (rules: StockV2StrategyActionRule[]) => void;
}) {
  function updateRule(index: number, patch: Partial<StockV2StrategyActionRule>) {
    onChange(rules.map((rule, idx) => idx === index ? { ...rule, ...patch } : rule));
  }

  function addRule() {
    onChange([...rules, newPlaybookRule("observe", rules.length)]);
  }

  function removeRule(index: number) {
    const next = rules.filter((_, idx) => idx !== index);
    onChange(next.length ? next : [newPlaybookRule("observe", 0)]);
  }

  function setPrefilters(index: number, items: StockV2StrategyPrefilter[]) {
    const dataPrefilters = items.filter((item) => prefilterBucket(item.type) === "dataPrefilters");
    const portfolioPrefilters = items.filter((item) => prefilterBucket(item.type) === "portfolioPrefilters");
    updateRule(index, {
      dataPrefilters: dataPrefilters.length ? dataPrefilters : undefined,
      portfolioPrefilters: portfolioPrefilters.length ? portfolioPrefilters : undefined,
    });
  }

  return (
    <CollapsibleSection
      title="操作剧本"
      subtitle="策略倾向只是总体判断,这里描述建仓、加仓、减仓、清仓等动作分支"
      defaultOpen
    >
      <div className="grid gap-3">
        {rules.map((rule, index) => (
          <div key={rule.id || `${rule.action}-${index}`} className="grid gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
              <div className="grid grid-cols-2 gap-2">
                <Field label="动作">
                  <select value={rule.action || "observe"} onChange={(event) => updateRule(index, { action: event.target.value })}>
                    {ACTIONS.map((action) => (
                      <option key={action.value} value={action.value}>{action.label}</option>
                    ))}
                  </select>
                </Field>
                <Field label="标题">
                  <input
                    type="text"
                    value={rule.title || ""}
                    placeholder={playbookActionLabel(rule.action)}
                    onChange={(event) => updateRule(index, { title: event.target.value })}
                  />
                </Field>
              </div>
              <Button
                aria-label="删除动作规则"
                className="mt-6 h-9 w-9 justify-center px-0"
                onClick={() => removeRule(index)}
                title="删除动作规则"
              >
                <Trash size={14} />
              </Button>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label="触发条件">
                <textarea
                  rows={2}
                  value={rule.trigger || ""}
                  placeholder="例如:回踩支撑位企稳、放量突破、消息证伪"
                  onChange={(event) => updateRule(index, { trigger: event.target.value })}
                />
              </Field>
              <Field label="前置条件">
                <textarea
                  rows={2}
                  value={rule.preconditions || ""}
                  placeholder="例如:已有底仓、仓位低于上限、行情数据新鲜"
                  onChange={(event) => updateRule(index, { preconditions: event.target.value })}
                />
              </Field>
              <Field label="目标状态">
                <textarea
                  rows={2}
                  value={rule.target || ""}
                  placeholder="例如:建仓到 5%、减仓 30%、清仓"
                  onChange={(event) => updateRule(index, { target: event.target.value })}
                />
              </Field>
              <Field label="风险备注">
                <textarea
                  rows={2}
                  value={rule.risk || ""}
                  placeholder="例如:不追高、跌破关键位进入 Review"
                  onChange={(event) => updateRule(index, { risk: event.target.value })}
                />
              </Field>
            </div>
            <PrefilterEditor
              items={[...(rule.dataPrefilters || []), ...(rule.portfolioPrefilters || [])]}
              onChange={(items) => setPrefilters(index, items)}
            />
          </div>
        ))}
      </div>
      <div className="flex justify-end">
        <Button onClick={addRule}>
          <Plus size={14} className="mr-1.5" />
          添加动作
        </Button>
      </div>
    </CollapsibleSection>
  );
}

function PrefilterEditor({
  items,
  onChange,
}: {
  items: StockV2StrategyPrefilter[];
  onChange: (items: StockV2StrategyPrefilter[]) => void;
}) {
  function updateItem(index: number, patch: Partial<StockV2StrategyPrefilter>) {
    onChange(items.map((item, idx) => idx === index ? normalizePrefilter({ ...item, ...patch }, idx) : item));
  }

  function addItem() {
    onChange([...items, normalizePrefilter({ type: "price_above" }, items.length)]);
  }

  function removeItem(index: number) {
    onChange(items.filter((_, idx) => idx !== index));
  }

  return (
    <div className="grid gap-2 rounded-md border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-[var(--muted-strong)]">数据/组合预筛</span>
        <Button className="h-8 px-2 text-xs" onClick={addItem}>
          <Plus size={12} className="mr-1" />
          添加预筛
        </Button>
      </div>
      {items.length === 0 ? (
        <span className="text-xs text-[var(--muted)]">不配置预筛时,该动作只作为 Agent 上下文,不会由数据面监控直接命中。</span>
      ) : (
        <div className="grid gap-2">
          {items.map((item, index) => {
            const between = item.type === "price_between";
            const stale = item.type === "quote_stale";
            return (
              <div key={item.key || `${item.type}-${index}`} className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto] gap-2">
                <select
                  className="select"
                  value={item.type}
                  onChange={(event) => updateItem(index, { type: event.target.value })}
                >
                  {PREFILTERS.map((prefilter) => (
                    <option key={prefilter.value} value={prefilter.value}>{prefilter.label}</option>
                  ))}
                </select>
                {between ? (
                  <div className="grid grid-cols-2 gap-2">
                    <input
                      className="input"
                      type="number"
                      step="0.01"
                      value={numToInput(item.low)}
                      placeholder="下限"
                      onChange={(event) => updateItem(index, { low: numOrUndef(event.target.value), threshold: undefined })}
                    />
                    <input
                      className="input"
                      type="number"
                      step="0.01"
                      value={numToInput(item.high)}
                      placeholder="上限"
                      onChange={(event) => updateItem(index, { high: numOrUndef(event.target.value), threshold: undefined })}
                    />
                  </div>
                ) : stale ? (
                  <input
                    className="input"
                    type="number"
                    step="1"
                    value={numToInput(item.maxAgeSeconds)}
                    placeholder="最大过期秒数"
                    onChange={(event) => updateItem(index, { maxAgeSeconds: numOrUndef(event.target.value), threshold: undefined })}
                  />
                ) : (
                  <input
                    className="input"
                    type="number"
                    step="0.01"
                    value={numToInput(item.threshold)}
                    placeholder={prefilterThresholdPlaceholder(item.type)}
                    onChange={(event) => updateItem(index, { threshold: numOrUndef(event.target.value) })}
                  />
                )}
                <Button
                  aria-label="删除预筛"
                  className="h-9 w-9 justify-center px-0"
                  onClick={() => removeItem(index)}
                  title="删除预筛"
                >
                  <Trash size={13} />
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function PlaybookSummary({
  playbook,
  monitorTask,
}: {
  playbook?: StockV2StrategyPlaybook;
  monitorTask?: StockV2MonitorTask | null;
}) {
  const rules = playbook?.rules || [];
  if (rules.length === 0) return null;
  const sentinelPlan = rules.some(isPortfolioSentinelPlan);
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="text-xs font-medium text-[var(--muted-strong)]">操作剧本</div>
        <Pill tone="neutral">{rules.length} 个动作</Pill>
      </div>
      {sentinelPlan ? <SentinelMonitorStatus task={monitorTask} /> : null}
      <div className="grid gap-2 text-xs">
        {rules.map((rule, index) => (
          <div key={rule.id || `${rule.action}-${index}`} className="grid gap-1 rounded-md border border-[var(--line)] bg-[var(--surface)] p-2">
            <div className="flex flex-wrap items-center gap-2">
              <Pill tone="neutral">{playbookActionLabel(rule.action)}</Pill>
              {rule.title ? <strong className="text-[var(--text)]">{rule.title}</strong> : null}
            </div>
            {rule.trigger ? <span className="text-[var(--muted-strong)]">触发: {rule.trigger}</span> : null}
            {rule.preconditions ? <span className="text-[var(--muted-strong)]">前置: {rule.preconditions}</span> : null}
            {rule.target || rule.sizing ? <span className="text-[var(--muted-strong)]">目标: {rule.target || playbookSizingSummary(rule)}</span> : null}
            {rule.reason ? <span className="text-[var(--muted-strong)]">理由: {rule.reason}</span> : null}
            {rule.risk || rule.riskNotes ? <span className="text-[var(--muted-strong)]">风险: {rule.risk || rule.riskNotes}</span> : null}
            {playbookPrefilterSummary(rule) ? <span className="text-[var(--muted)]">预筛: {playbookPrefilterSummary(rule)}</span> : null}
            {playbookMonitorWindowSummary(rule) ? <span className="text-[var(--muted)]">监控窗口: {playbookMonitorWindowSummary(rule)}</span> : null}
            {rule.portfolioSentinelRunId ? (
              <span className="text-[var(--muted)]">
                来源: 组合哨兵运行 <span className="font-mono">{shortID(rule.portfolioSentinelRunId)}</span>
              </span>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

function SentinelMonitorStatus({ task }: { task?: StockV2MonitorTask | null }) {
  const config = task?.config;
  const latest = task?.latestRun;
  const hasPlanCounts = typeof latest?.metadata?.portfolioSentinelPlanEvaluatedCount === "number";
  return (
    <div className="mb-2 grid gap-1 border-b border-[var(--line)] pb-2 text-xs text-[var(--muted-strong)]">
      <div className="flex flex-wrap items-center gap-2">
        <span>绑定数据面策略监控</span>
        <Pill tone={!task ? "neutral" : config?.enabled ? "good" : "warn"}>
          {!task ? "状态未加载" : config?.enabled ? "已启用" : "未启用"}
        </Pill>
        {config?.intervalSeconds ? <span>扫描周期 {formatCompactInterval(config.intervalSeconds)}</span> : null}
      </div>
      <span className="text-[var(--muted)]">价格与涨跌幅越线由分钟行情刷新即时复查；其他条件按完整扫描周期检查。</span>
      <span className="text-[var(--muted)]">
        最近扫描: {latest ? `${stockV2MonitorRunStatusLabel(latest.status)}，${formatDate(latest.startedAt) || "-"}` : "暂无记录"}
        {latest && hasPlanCounts ? `，检查 ${latest.metadata?.portfolioSentinelPlanEvaluatedCount} 条哨兵计划，命中 ${latest.metadata?.portfolioSentinelPlanMatchedCount ?? 0} 条` : ""}
      </span>
    </div>
  );
}

export function defaultPlaybookRules(): StockV2StrategyActionRule[] {
  return [
    newPlaybookRule("observe", 0),
    newPlaybookRule("build_position", 1),
    newPlaybookRule("add_position", 2),
    newPlaybookRule("hold", 3),
    newPlaybookRule("reduce_position", 4),
    newPlaybookRule("exit_position", 5),
  ];
}

export function playbookFromGenerationMeta(meta?: Record<string, unknown>): StockV2StrategyPlaybook | undefined {
  const raw = meta?.[PLAYBOOK_META_KEY];
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return undefined;
  const record = raw as Record<string, unknown>;
  const rawRules = Array.isArray(record.rules) ? record.rules : [];
  const rules = rawRules
    .map((item, index) => normalizePlaybookRule(item, index))
    .filter((item): item is StockV2StrategyActionRule => Boolean(item));
  return rules.length ? { version: typeof record.version === "string" ? record.version : "v1", rules } : undefined;
}

export function playbookRulesForForm(meta?: Record<string, unknown>): StockV2StrategyActionRule[] {
  const playbook = playbookFromGenerationMeta(meta);
  return playbook?.rules?.length ? playbook.rules : defaultPlaybookRules();
}

export function playbookMetaFromRules(
  rules: StockV2StrategyActionRule[],
  fallback?: { entryConditions?: string; exitConditions?: string; riskNotes?: string },
): StockV2StrategyPlaybook | undefined {
  const normalized = rules
    .map((rule, index) => normalizePlaybookRule(rule, index))
    .filter((rule): rule is StockV2StrategyActionRule => Boolean(rule && hasPlaybookRuleContent(rule)));

  if (normalized.length === 0) {
    const entry = fallback?.entryConditions?.trim();
    const exit = fallback?.exitConditions?.trim();
    const risk = fallback?.riskNotes?.trim();
    if (entry) {
      normalized.push({
        id: "build_position",
        action: "build_position",
        title: "建仓",
        trigger: entry,
        risk,
        priority: 1,
      });
    }
    if (exit) {
      normalized.push({
        id: "exit_position",
        action: "exit_position",
        title: "清仓",
        trigger: exit,
        risk,
        priority: normalized.length + 1,
      });
    }
  }

  return normalized.length ? { version: "v1", rules: normalized } : undefined;
}

export function playbookActionLabel(action?: string): string {
  return ACTIONS.find((item) => item.value === action)?.label || action || "动作";
}

export function playbookSummaryText(playbook?: StockV2StrategyPlaybook): string {
  const rules = playbook?.rules || [];
  if (rules.length === 0) return "";
  return rules.map((rule) => playbookActionLabel(rule.action)).join(" / ");
}

function newPlaybookRule(action: StockV2StrategyActionType, index: number): StockV2StrategyActionRule {
  return {
    id: `${action}-${index + 1}`,
    action,
    title: playbookActionLabel(action),
    priority: index + 1,
  };
}

function normalizePlaybookRule(value: unknown, index: number): StockV2StrategyActionRule | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const raw = value as Record<string, unknown>;
  const action = stringFromUnknown(raw.action) || "observe";
  const rule: StockV2StrategyActionRule = {
    id: stringFromUnknown(raw.id) || `${action}-${index + 1}`,
    action,
    title: stringFromUnknown(raw.title),
    trigger: stringFromUnknown(raw.trigger),
    preconditions: stringFromUnknown(raw.preconditions),
    target: stringFromUnknown(raw.target),
    risk: stringFromUnknown(raw.risk),
    dataPrefilters: normalizePrefilterList(raw.dataPrefilters),
    portfolioPrefilters: normalizePrefilterList(raw.portfolioPrefilters),
    newsPrefilters: normalizePrefilterList(raw.newsPrefilters),
    symbol: stringFromUnknown(raw.symbol),
    market: stringFromUnknown(raw.market),
    portfolioId: stringFromUnknown(raw.portfolioId),
    triggerPolicy: stringFromUnknown(raw.triggerPolicy),
    sizing: normalizeSizing(raw.sizing),
    reason: stringFromUnknown(raw.reason),
    riskNotes: stringFromUnknown(raw.riskNotes),
    validUntil: stringFromUnknown(raw.validUntil),
    monitorWindow: normalizeMonitorWindow(raw.monitorWindow),
    portfolioSentinelActionPlan: booleanOrStringFromUnknown(raw.portfolioSentinelActionPlan),
    portfolioSentinelRunId: stringFromUnknown(raw.portfolioSentinelRunId),
    priority: numberFromUnknown(raw.priority) || index + 1,
  };
  return rule;
}

function hasPlaybookRuleContent(rule: StockV2StrategyActionRule): boolean {
  const defaultTitle = playbookActionLabel(rule.action);
  return Boolean(
    rule.trigger?.trim() ||
    rule.preconditions?.trim() ||
    rule.target?.trim() ||
    rule.risk?.trim() ||
    Boolean(rule.dataPrefilters?.length) ||
    Boolean(rule.portfolioPrefilters?.length) ||
    Boolean(rule.newsPrefilters?.length) ||
    Boolean(rule.sizing) ||
    Boolean(rule.reason?.trim()) ||
    Boolean(rule.riskNotes?.trim()) ||
    Boolean(rule.validUntil) ||
    Boolean(rule.monitorWindow) ||
    Boolean(rule.portfolioSentinelActionPlan) ||
    (rule.title?.trim() && rule.title.trim() !== defaultTitle),
  );
}

function normalizePrefilterList(value: unknown): StockV2StrategyPrefilter[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const items = value
    .map((item, index) => normalizePrefilter(item, index))
    .filter(isPrefilterReady)
    .filter((item) => item.type);
  return items.length ? items : undefined;
}

function normalizePrefilter(value: unknown, index: number): StockV2StrategyPrefilter {
  const raw = value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
  const type = stringFromUnknown(raw.type) || "price_above";
  return {
    key: stringFromUnknown(raw.key) || `${type}-${index + 1}`,
    type,
    threshold: numberFromUnknown(raw.threshold),
    low: numberFromUnknown(raw.low),
    high: numberFromUnknown(raw.high),
    maxAgeSeconds: numberFromUnknown(raw.maxAgeSeconds),
    minScore: numberFromUnknown(raw.minScore),
    topics: Array.isArray(raw.topics) ? raw.topics.filter((item): item is string => typeof item === "string" && item.trim() !== "") : undefined,
  };
}

function prefilterBucket(type?: string): "dataPrefilters" | "portfolioPrefilters" {
  return PREFILTERS.find((item) => item.value === type)?.bucket || "dataPrefilters";
}

function prefilterThresholdPlaceholder(type?: string): string {
  if (type?.includes("pct_change") || type?.includes("weight")) return "例如: 5";
  return "例如: 60.00";
}

function numToInput(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

function numOrUndef(value: string): number | undefined {
  if (value.trim() === "") return undefined;
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function playbookPrefilterSummary(rule: StockV2StrategyActionRule): string {
  const items = [...(rule.dataPrefilters || []), ...(rule.portfolioPrefilters || []), ...(rule.newsPrefilters || [])];
  return items.map(strategyPrefilterSummary).join(" / ");
}

export function strategyPrefilterSummary(item: StockV2StrategyPrefilter): string {
  const label = prefilterLabel(item.type);
  if (item.type === "price_between") {
    return `${label} ${formatRuleNumber(item.low)} - ${formatRuleNumber(item.high)}`;
  }
  if (item.type === "quote_stale") {
    return `${label}超过 ${formatRuleNumber(item.maxAgeSeconds)} 秒`;
  }
  if (item.type === "news_semantic_relevance") {
    const details = [
      typeof item.minScore === "number" ? `分数 ${formatRuleNumber(item.minScore)}` : "",
      item.topics?.length ? `主题 ${item.topics.join("、")}` : "",
    ].filter(Boolean);
    return details.length ? `${label} ${details.join("，")}` : label;
  }
  const suffix = item.type?.includes("pct_change") || item.type?.includes("weight") ? "%" : "";
  return typeof item.threshold === "number" ? `${label} ${formatRuleNumber(item.threshold)}${suffix}` : label;
}

function prefilterLabel(type?: string): string {
  if (type === "news_semantic_relevance") return "消息相关度";
  return PREFILTERS.find((prefilter) => prefilter.value === type)?.label || type || "预筛";
}

function playbookSizingSummary(rule: StockV2StrategyActionRule): string {
  const sizing = rule.sizing;
  if (!sizing) return "";
  const value = formatRuleNumber(sizing.value);
  if (sizing.mode === "target_portfolio_pct") return `目标组合占比 ${value}%`;
  if (sizing.mode === "available_quantity_pct") return `调整届时可用数量的 ${value}%`;
  return `${sizing.mode} ${value}`;
}

function playbookMonitorWindowSummary(rule: StockV2StrategyActionRule): string {
  const window = rule.monitorWindow;
  const expiresAt = window?.expiresAt || rule.validUntil;
  if (window?.kind === "continuous_until_expiry") {
    return `生成后持续检查，至 ${formatDate(expiresAt) || "-"}`;
  }
  return expiresAt ? `至 ${formatDate(expiresAt) || "-"}` : "";
}

function normalizeSizing(value: unknown): StockV2StrategyActionRule["sizing"] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const raw = value as Record<string, unknown>;
  const mode = stringFromUnknown(raw.mode);
  const amount = numberFromUnknown(raw.value);
  return mode && amount !== undefined ? { mode, value: amount } : undefined;
}

function normalizeMonitorWindow(value: unknown): StockV2StrategyActionRule["monitorWindow"] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const raw = value as Record<string, unknown>;
  const window = {
    kind: stringFromUnknown(raw.kind),
    startsAt: stringFromUnknown(raw.startsAt),
    expiresAt: stringFromUnknown(raw.expiresAt),
  };
  return window.kind || window.startsAt || window.expiresAt ? window : undefined;
}

function booleanOrStringFromUnknown(value: unknown): boolean | string | undefined {
  if (typeof value === "boolean") return value;
  return stringFromUnknown(value);
}

function isPortfolioSentinelPlan(rule: StockV2StrategyActionRule): boolean {
  return rule.portfolioSentinelActionPlan === true || rule.portfolioSentinelActionPlan === "true";
}

function formatRuleNumber(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 4 }).format(value);
}

function formatCompactInterval(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`;
  return `${seconds} 秒`;
}

function shortID(value: string): string {
  return value.length > 12 ? value.slice(-12) : value;
}

function isPrefilterReady(item: StockV2StrategyPrefilter): boolean {
  if (item.type === "quote_stale") return true;
  if (item.type === "news_semantic_relevance") return typeof item.minScore === "number" || Boolean(item.topics?.length);
  if (item.type === "price_between") return typeof item.low === "number" && typeof item.high === "number";
  return typeof item.threshold === "number";
}

function stringFromUnknown(value: unknown): string | undefined {
  return typeof value === "string" ? value.trim() : undefined;
}

function numberFromUnknown(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const n = Number(value);
    if (Number.isFinite(n)) return n;
  }
  return undefined;
}
