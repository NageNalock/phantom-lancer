import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import type { AppActions } from "../../../app/App";
import type {
  StockV2AgentListResponse,
  StockV2AgentModelProfile,
  StockV2AgentTaskProfile,
  StockV2EmbeddingStatus,
  StockV2NewsContextBackfillPreview,
  StockV2NewsContextConfig,
  StockV2NewsContextSummary,
  Tone,
} from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Pill, Toggle, useDangerConfirm } from "../../../components/ui";
import {
  formatNewsContextInterval,
  formatNewsContextTime,
  indexStatusLabel,
  indexStatusTone,
  mcpVerificationLabel,
  mcpVerificationTone,
  resolveAvailableTaskModel,
} from "./model";

const CLEANUP_GRACE_OPTIONS = [1, 3, 7, 14, 30].map((days) => ({
  label: `${days} 天`,
  seconds: days * 86400,
}));

type SettingsDraft = Pick<
  StockV2NewsContextConfig,
  "enabled" | "autoCleanupEnabled" | "hourlyEnabled" | "fourHourEnabled" | "dailyEnabled" | "cleanupGraceSeconds"
> & { additionalResearchPrompt: string };

type Prerequisites = {
  taskProfiles: StockV2AgentTaskProfile[] | null;
  models: StockV2AgentModelProfile[] | null;
  embedding: StockV2EmbeddingStatus | null;
  backfill: StockV2NewsContextBackfillPreview | null;
};

export function NewsContextSettingsDrawer({
  actions,
  config,
  summary,
  onClose,
  onSaved,
}: {
  actions: AppActions;
  config: StockV2NewsContextConfig;
  summary: StockV2NewsContextSummary;
  onClose: () => void;
  onSaved: () => Promise<void> | void;
}) {
  const [draft, setDraft] = useState<SettingsDraft>(() => draftFromConfig(config));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [prerequisites, setPrerequisites] = useState<Prerequisites | null>(null);
  const [prerequisitesLoading, setPrerequisitesLoading] = useState(true);
  const [prerequisitesError, setPrerequisitesError] = useState<string | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  useEffect(() => setDraft(draftFromConfig(config)), [config]);

  async function loadPrerequisites() {
    setPrerequisitesLoading(true);
    setPrerequisitesError(null);
    try {
      const [taskProfiles, models, embedding, backfill] = await Promise.allSettled([
        actions.api<StockV2AgentListResponse<StockV2AgentTaskProfile>>("/api/stockv2/agent/task-profiles?limit=20"),
        actions.api<StockV2AgentListResponse<StockV2AgentModelProfile>>("/api/stockv2/agent/models"),
        actions.api<StockV2EmbeddingStatus>("/api/stockv2/embeddings/status"),
        actions.api<StockV2NewsContextBackfillPreview>("/api/stockv2/news-context/backfill/preview"),
      ]);
      setPrerequisites({
        taskProfiles: taskProfiles.status === "fulfilled" ? taskProfiles.value.items || [] : null,
        models: models.status === "fulfilled" ? models.value.items || [] : null,
        embedding: embedding.status === "fulfilled" ? embedding.value : null,
        backfill: backfill.status === "fulfilled" ? backfill.value : null,
      });
      setPrerequisitesError([
        taskProfiles.status === "rejected" ? `任务绑定：${friendlyError(taskProfiles.reason)}` : null,
        models.status === "rejected" ? `模型列表：${friendlyError(models.reason)}` : null,
        embedding.status === "rejected" ? `向量状态：${friendlyError(embedding.reason)}` : null,
        backfill.status === "rejected" ? `历史待处理：${friendlyError(backfill.reason)}` : null,
      ].filter(Boolean).join("；") || null);
    } catch (error) {
      setPrerequisites(null);
      setPrerequisitesError(friendlyError(error));
    } finally {
      setPrerequisitesLoading(false);
    }
  }

  useEffect(() => {
    void loadPrerequisites();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const promptLength = Array.from(draft.additionalResearchPrompt).length;
  const validationError = useMemo(() => {
    if (draft.enabled && !draft.hourlyEnabled && !draft.fourHourEnabled && !draft.dailyEnabled) {
      return "启用自动归纳时，至少需要开启一种归纳周期。";
    }
    if (draft.autoCleanupEnabled && !draft.enabled) {
      return "自动安全清理依赖自动归纳，请先开启自动归纳。";
    }
    if (draft.autoCleanupEnabled && !draft.dailyEnabled) {
      return "自动安全清理依赖每日归纳，请先开启每日归纳。";
    }
    if (promptLength > 2000) return "附加研究要求不能超过 2000 字。";
    return null;
  }, [draft, promptLength]);

  async function save() {
    if (validationError) {
      setSaveError(validationError);
      return;
    }
    const enablingCleanup = draft.autoCleanupEnabled && !config.autoCleanupEnabled;
    const shorteningCleanupGrace = draft.autoCleanupEnabled
      && config.autoCleanupEnabled
      && draft.cleanupGraceSeconds < config.cleanupGraceSeconds;
    if (enablingCleanup || shorteningCleanupGrace) {
      const confirmed = await confirmDanger({
        title: enablingCleanup ? "启用自动安全清理" : "缩短自动清理等待期",
        body: enablingCleanup
          ? "只有历史补处理、每日结论、影响复核、主题索引和 CLI 检索全部通过后，系统才会清理普通新闻正文。"
          : `等待期将从 ${formatNewsContextInterval(config.cleanupGraceSeconds)} 缩短为 ${formatNewsContextInterval(draft.cleanupGraceSeconds)}，符合安全门的新闻会更早清理。`,
        impact: ["清理满足全部安全门的普通新闻正文和旧检索资料", "继续长期保留主题版本、精简证据和防重复指纹"],
        recovery: "可以随时暂停后续自动清理；已经完成的正文压缩不能恢复原文。",
        confirmLabel: enablingCleanup ? "启用自动清理" : "缩短等待期",
      });
      if (!confirmed) return;
    }

    setSaving(true);
    setSaveError(null);
    try {
      await actions.api<StockV2NewsContextConfig>("/api/stockv2/news-context/config", {
        method: "PATCH",
        csrf: actions.csrf,
        body: {
          enabled: draft.enabled,
          autoCleanupEnabled: draft.autoCleanupEnabled,
          hourlyEnabled: draft.hourlyEnabled,
          fourHourEnabled: draft.fourHourEnabled,
          dailyEnabled: draft.dailyEnabled,
          cleanupGraceSeconds: draft.cleanupGraceSeconds,
          additionalResearchPrompt: draft.additionalResearchPrompt.trim(),
        },
      });
      actions.setToast("消息脉络配置已保存", "good");
      await onSaved();
      onClose();
    } catch (error) {
      setSaveError(friendlyError(error));
    } finally {
      setSaving(false);
    }
  }

  const resolvedIndexStatus = resolveIndexStatus(summary);

  return (
    <>
      <Drawer
        footer={
          <>
            <Button disabled={saving} onClick={onClose}>取消</Button>
            <Button disabled={saving || Boolean(validationError)} onClick={() => void save()} tone="primary">
              {saving ? "保存中" : "保存配置"}
            </Button>
          </>
        }
        onClose={onClose}
        subtitle="归纳周期、清理门禁与研究关注点只影响消息脉络"
        title="消息脉络配置"
        width={560}
      >
        <div className="grid gap-4">
          {saveError || validationError ? <Notice tone="danger">{saveError || validationError}</Notice> : null}

          <section className="grid gap-2" aria-labelledby="news-context-schedule-settings">
            <div>
              <h3 className="m-0 text-sm font-semibold" id="news-context-schedule-settings">归纳与清理</h3>
              <p className="mt-1 mb-0 text-xs text-[var(--muted)]">一个周期内的全部新闻都会处理，系统根据整理后的文字量自动分片。</p>
            </div>
            <Toggle
              checked={draft.enabled}
              label={<SettingLabel title="自动归纳" detail="按已开启周期持续归纳新增新闻" />}
              name="news_context_enabled"
              onChange={(enabled) => setDraft((current) => ({ ...current, enabled }))}
            />
            <div className="grid grid-cols-3 gap-2">
              <Toggle
                checked={draft.hourlyEnabled}
                label={<SettingLabel title="小时归纳" detail="完整处理小时窗口" />}
                name="news_context_hourly_enabled"
                onChange={(hourlyEnabled) => setDraft((current) => ({ ...current, hourlyEnabled }))}
                variant="row"
              />
              <Toggle
                checked={draft.fourHourEnabled}
                label={<SettingLabel title="四小时归纳" detail="汇总小时主题变化" />}
                name="news_context_four_hour_enabled"
                onChange={(fourHourEnabled) => setDraft((current) => ({ ...current, fourHourEnabled }))}
                variant="row"
              />
              <Toggle
                checked={draft.dailyEnabled}
                label={<SettingLabel title="每日归纳" detail="形成每日完整结论" />}
                name="news_context_daily_enabled"
                onChange={(dailyEnabled) => setDraft((current) => ({ ...current, dailyEnabled }))}
                variant="row"
              />
            </div>
            <Toggle
              checked={draft.autoCleanupEnabled}
              label={<SettingLabel title="自动安全清理" detail="只清理通过归纳、复核、索引、CLI 检索与引用检查的新闻" />}
              name="news_context_auto_cleanup_enabled"
              onChange={(autoCleanupEnabled) => setDraft((current) => ({ ...current, autoCleanupEnabled }))}
            />
            <Field label="清理等待期" help="默认 1 天。新闻通过全部安全门后，仍需经过这段可恢复等待期。">
              <select
                disabled={!draft.autoCleanupEnabled}
                onChange={(event) => setDraft((current) => ({ ...current, cleanupGraceSeconds: Number(event.target.value) }))}
                value={draft.cleanupGraceSeconds}
              >
                {CLEANUP_GRACE_OPTIONS.map((item) => <option key={item.seconds} value={item.seconds}>{item.label}</option>)}
              </select>
            </Field>
          </section>

          <Field label="附加研究要求" help="最多 2000 字，只能补充关注重点，不能覆盖固定的完整性和安全规则。">
            <textarea
              onChange={(event) => setDraft((current) => ({ ...current, additionalResearchPrompt: event.target.value }))}
              placeholder="例如：重点检查产业链向下游传导和板块轮换的确认信号。"
              rows={5}
              value={draft.additionalResearchPrompt}
            />
          </Field>
          <div aria-live="polite" className="-mt-3 text-right font-mono text-xs text-[var(--muted)]">{promptLength} / 2000</div>

          <CollapsibleSection title="高级设置" subtitle="固定周期、执行安全策略、下次运行和依赖能力只读展示">
            <div className="grid gap-0 rounded-lg border border-[var(--line)] bg-[var(--surface)]">
              <ReadOnlyRow label="小时周期" value={formatNewsContextInterval(config.hourlyIntervalSeconds || 3600)} detail="固定产品层级；调整需修改消息脉络实现并重新部署" />
              <ReadOnlyRow label="四小时周期" value={formatNewsContextInterval(config.fourHourIntervalSeconds || 14400)} detail="固定产品层级；调整需修改消息脉络实现并重新部署" />
              <ReadOnlyRow label="每日周期" value={formatNewsContextInterval(config.dailyIntervalSeconds || 86400)} detail="固定产品层级；调整需修改消息脉络实现并重新部署" />
              <ReadOnlyRow label="单次归纳上限" value={formatNewsContextInterval(config.agentTimeoutSeconds || 1800)} detail="内置安全策略；超时前会清理当前任务进程组，调整需重新部署" />
              <ReadOnlyRow label="归纳失败自动重试" value={`${config.timeoutRetryLimit ?? 2} 次`} detail="超时、异常退出未提交或结果完整性校验失败时重试；重试前清理本次进程组，并将当前批次减半" />
              <ReadOnlyRow label="后台轮询" value={formatNewsContextInterval(config.schedulerPollSeconds || 5)} detail="内置单机调度节奏；实时任务仍保持单飞" />
              <ReadOnlyRow label="归纳容量" value="按文字量自动分片" detail="不限制每日新闻数、主题数、轮换线索数或主题变化数" />
              <ReadOnlyRow label="下次小时归纳" value={formatNewsContextTime(config.nextHourlyAt)} />
              <ReadOnlyRow label="下次四小时归纳" value={formatNewsContextTime(config.nextFourHourAt)} />
              <ReadOnlyRow label="下次每日归纳" value={formatNewsContextTime(config.nextDailyAt)} />
              <ReadOnlyRow label="上次归纳" value={formatNewsContextTime(config.lastRunAt)} />
              <ReadOnlyRow label="上次清理" value={formatNewsContextTime(config.lastCleanupAt)} />
              <ReadOnlyRow
                label="历史待处理"
                value={prerequisitesLoading ? "加载中" : prerequisites?.backfill ? `${prerequisites.backfill.pendingNewsCount} 条` : "加载失败"}
                detail={prerequisitesLoading
                  ? "正在读取历史待处理状态"
                  : !prerequisites?.backfill
                    ? "历史待处理数量未知，请刷新状态重试"
                    : prerequisites.backfill.pendingNewsCount
                      ? `${formatNewsContextTime(prerequisites.backfill.earliestNewsAt)} 至 ${formatNewsContextTime(prerequisites.backfill.latestNewsAt)}；${typeof prerequisites.backfill.estimatedChunkCount === "number" ? `预计 ${prerequisites.backfill.estimatedChunkCount} 个自动分片` : "自动分片数待评估"}`
                      : "当前没有待补处理的历史新闻"}
              />
            </div>

            {config.lastError ? <Notice tone="danger">最近运行错误：{config.lastError}</Notice> : null}

            <section className="grid gap-2" aria-labelledby="news-context-prerequisites">
              <div className="flex items-center justify-between gap-2">
                <div>
                  <h3 className="m-0 text-sm font-semibold" id="news-context-prerequisites">运行依赖</h3>
                  <p className="mt-1 mb-0 text-xs text-[var(--muted)]">模型和向量绑定请前往股票模块的 Agent 页面修改。</p>
                </div>
                <Button disabled={prerequisitesLoading} onClick={() => void loadPrerequisites()}>{prerequisitesLoading ? "加载中" : "刷新状态"}</Button>
              </div>
              {prerequisitesError ? <Notice tone="danger">依赖状态加载失败：{prerequisitesError}</Notice> : null}
              <div className="grid gap-0 rounded-lg border border-[var(--line)] bg-[var(--surface)]">
                <ModelStatusRow label="消息归纳模型" loading={prerequisitesLoading} prerequisites={prerequisites} taskType="news_event_review" />
                <ModelStatusRow label="组合影响复核模型" loading={prerequisitesLoading} prerequisites={prerequisites} taskType="portfolio_sentinel" />
                <EmbeddingStatusRow loading={prerequisitesLoading} prerequisites={prerequisites} />
                <StatusRow label="主题索引" tone={indexStatusTone(resolvedIndexStatus)} value={indexStatusLabel(resolvedIndexStatus)} />
                <StatusRow
                  label="MCP 主题检索"
                  tone={mcpVerificationTone(summary.mcpAvailable, summary.mcpToolsReady, summary.mcpVerificationStatus)}
                  value={mcpVerificationLabel(summary.mcpAvailable, summary.mcpToolsReady, summary.mcpVerificationStatus)}
                />
              </div>
              {summary.indexError || summary.mcpError ? <Notice tone="danger">{[summary.indexError, summary.mcpError].filter(Boolean).join("；")}</Notice> : null}
            </section>
          </CollapsibleSection>
        </div>
      </Drawer>
      {dangerConfirmDialog}
    </>
  );
}

function draftFromConfig(config: StockV2NewsContextConfig): SettingsDraft {
  return {
    enabled: config.enabled,
    autoCleanupEnabled: config.autoCleanupEnabled,
    hourlyEnabled: config.hourlyEnabled,
    fourHourEnabled: config.fourHourEnabled,
    dailyEnabled: config.dailyEnabled,
    cleanupGraceSeconds: config.cleanupGraceSeconds || 86400,
    additionalResearchPrompt: config.additionalResearchPrompt || "",
  };
}

function resolveIndexStatus(summary: StockV2NewsContextSummary): string {
  if (summary.indexStatus) return summary.indexStatus;
  if ((summary.indexFailedCount || 0) > 0) return "failed";
  if ((summary.indexMissingCount || 0) > 0 || (summary.indexStaleCount || 0) > 0) return "stale";
  if ((summary.indexReadyCount || 0) > 0) return "ready";
  return "missing";
}

function SettingLabel({ title, detail }: { title: string; detail: string }) {
  return <span className="grid gap-0.5"><span>{title}</span><span className="text-xs font-normal text-[var(--muted)]">{detail}</span></span>;
}

function ReadOnlyRow({ label, value, detail }: { label: string; value: ReactNode; detail?: string }) {
  return (
    <div className="grid grid-cols-[140px_minmax(0,1fr)] items-center gap-3 border-b border-[var(--line)] px-3 py-2.5 last:border-b-0">
      <span className="text-xs text-[var(--muted)]">{label}</span>
      <span className="min-w-0 text-right text-sm">
        {value}
        {detail ? <small className="mt-0.5 block text-xs text-[var(--muted)]">{detail}</small> : null}
      </span>
    </div>
  );
}

function StatusRow({ label, value, tone }: { label: string; value: string; tone: Tone }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-[var(--line)] px-3 py-2.5 last:border-b-0">
      <span className="text-xs text-[var(--muted)]">{label}</span>
      <Pill tone={tone}>{value}</Pill>
    </div>
  );
}

function ModelStatusRow({
  label,
  loading,
  prerequisites,
  taskType,
}: {
  label: string;
  loading: boolean;
  prerequisites: Prerequisites | null;
  taskType: string;
}) {
  if (loading) return <StatusRow label={label} tone="neutral" value="加载中" />;
  if (!prerequisites?.taskProfiles || !prerequisites.models) return <StatusRow label={label} tone="neutral" value="状态未知" />;
  const profile = prerequisites.taskProfiles.find((item) => item.taskType === taskType);
  const model = resolveAvailableTaskModel(taskType, prerequisites.taskProfiles, prerequisites.models);
  const ready = Boolean(model);
  const usingFallback = Boolean(model && profile?.fallbackModelId === model.id && profile.primaryModelId !== model.id);
  const name = model?.displayName || model?.modelName;
  return <StatusRow label={label} tone={ready ? "good" : "danger"} value={name ? `${name} / ${usingFallback ? "备用可用" : "可用"}` : "未绑定或不可用"} />;
}

function EmbeddingStatusRow({ loading, prerequisites }: { loading: boolean; prerequisites: Prerequisites | null }) {
  if (loading) return <StatusRow label="向量模型" tone="neutral" value="加载中" />;
  if (!prerequisites?.embedding) return <StatusRow label="向量模型" tone="neutral" value="状态未知" />;
  const status = prerequisites.embedding;
  const modelReady = Boolean(status.modelId || status.config.embeddingModelId)
    && status.errorCode !== "embedding_model_not_configured"
    && status.errorCode !== "embedding_model_unavailable";
  const tone: Tone = status.available ? "good" : modelReady ? "warn" : "danger";
  const suffix = status.available ? "可用" : modelReady ? "模型可用，资产待维护" : "不可用";
  return <StatusRow label="向量模型" tone={tone} value={status.modelName ? `${status.modelName} / ${suffix}` : suffix} />;
}
