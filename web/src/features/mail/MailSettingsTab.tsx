import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { AppData } from "../../app/types";
import { Button, CheckLabel, CollapsibleSection, EmptyState, Field, Notice, Panel, Pill, SubTabs, useDangerConfirm } from "../../components/ui";
import {
  friendlyError,
  mailRetentionRuleList,
  mailRetentionRuleUpsert,
  mailRetentionRuleDelete,
  mailRetentionApplyNow,
  mailBackupList,
  mailBackupCreate,
  mailBackupDelete,
  mailBackupDownloadUrl,
  mailBackupScheduleList,
  mailBackupScheduleUpsert,
  mailBackupScheduleDelete,
  mailDangerRequirements,
  mailDangerGenerateCode,
  mailDangerHardDelete,
  type MailRetentionRule,
  type MailBackup,
  type MailBackupCreateReq,
  type MailBackupSchedule,
  type DangerRequirementsResp,
  type DangerGenerateCodeResp,
} from "../../api/client";
import { formatBytesZero } from "../../utils/format";
import { formatDate } from "../../domain/labels";

const RETENTION_SCOPES = [
  { value: "delivery_events", label: "投递事件", help: "投递 / 队列 / 退信历史" },
  { value: "health_checks", label: "健康检查", help: "L1-L9 探针历史" },
  { value: "webhook_events", label: "Webhook 事件", help: "入站 Webhook 鉴权与处理记录" },
  { value: "index_messages", label: "索引消息元数据", help: "FTS5 搜索索引中的邮件元数据" },
  { value: "expired_backups", label: "过期备份", help: "根据 expires_at 清理旧备份记录" },
] as const;

type BackupSubTab = "manual" | "schedules";

export function MailSettingsTab({ actions, reload, data }: { actions: AppActions; reload: () => Promise<void>; data: AppData }) {
  return (
    <section className="grid gap-3">
      <RetentionSection actions={actions} reload={reload} data={data} />
      <BackupSection actions={actions} reload={reload} data={data} />
      <DangerZone actions={actions} reload={reload} />
    </section>
  );
}

// ============================================================================
// ---- Retention Policies ----------------------------------------------------
// ============================================================================

function RetentionSection({ actions, reload }: { actions: AppActions; reload: () => Promise<void>; data: AppData }) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const [rules, setRules] = useState<MailRetentionRule[]>([]);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<MailRetentionRule | null>(null);
  const [formTarget, setFormTarget] = useState<MailRetentionRule["target_kind"]>("delivery_events");
  const [formDays, setFormDays] = useState<number | "">("");
  const [formKeepMinCount, setFormKeepMinCount] = useState<number | "">("");
  const [formEnabled, setFormEnabled] = useState(true);
  const [formError, setFormError] = useState("");
  const [applyResult, setApplyResult] = useState<{ deleted_by_target: Record<string, number>; total_deleted: number; applied_at_iso: string } | null>(null);

  const loadRules = useCallback(async () => {
    setLoading(true);
    try {
      const list = await mailRetentionRuleList();
      setRules(list);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions]);

  useEffect(() => {
    void loadRules();
  }, [loadRules]);

  const usedTargets = useMemo(() => new Set(rules.map((r) => r.target_kind)), [rules]);
  const availableTargets = RETENTION_SCOPES.filter((s) => !usedTargets.has(s.value) || editing?.target_kind === s.value);

  function resetForm() {
    setShowForm(false);
    setEditing(null);
    setFormTarget("delivery_events");
    setFormDays("");
    setFormKeepMinCount("");
    setFormEnabled(true);
    setFormError("");
  }

  function startEdit(rule: MailRetentionRule) {
    setEditing(rule);
    setShowForm(true);
    setFormTarget(rule.target_kind);
    setFormDays(rule.days || "");
    setFormKeepMinCount(rule.keep_min_count ?? "");
    setFormEnabled(rule.enabled !== false);
    setFormError("");
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    const days = formDays === "" ? 0 : Number(formDays);
    const keepMin = formKeepMinCount === "" ? 0 : Number(formKeepMinCount);
    if (!Number.isFinite(days) || days <= 0) {
      setFormError("保留天数必须大于 0");
      return;
    }
    if (!Number.isFinite(keepMin) || keepMin < 0) {
      setFormError("最少保留条数不能小于 0");
      return;
    }
    try {
      const payload = editing
        ? { ...editing, target_kind: formTarget, days, keep_min_count: keepMin, enabled: formEnabled }
        : { target_kind: formTarget, days, keep_min_count: keepMin, enabled: formEnabled, rule_kind: "custom" };
      const saved = await mailRetentionRuleUpsert(payload);
      setRules((prev) => {
        const filtered = prev.filter((r) => r.id !== saved.id);
        return [...filtered, saved];
      });
      actions.setToast(`保留策略已保存：${scopeLabel(saved.target_kind)}`, "good");
      resetForm();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleDelete(rule: MailRetentionRule) {
    const ok = await confirmDanger({
      title: "删除保留策略？",
      body: `删除后 ${scopeLabel(rule.target_kind)} 将不再自动清理旧数据。是否继续？`,
      confirmLabel: "确认删除",
    });
    if (!ok) return;
    try {
      await mailRetentionRuleDelete(rule.id);
      setRules((prev) => prev.filter((r) => r.id !== rule.id));
      actions.setToast("保留策略已删除", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleApplyNow() {
    const ok = await confirmDanger({
      title: "立即执行保留策略？",
      body: "将根据当前保留策略规则删除过期/超限的历史数据。操作不可逆。",
      confirmLabel: "确认执行清理",
    });
    if (!ok) return;
    setApplying(true);
    try {
      const result = await mailRetentionApplyNow();
      setApplyResult({
        deleted_by_target: result.deleted_by_target || {},
        total_deleted: result.total_deleted || 0,
        applied_at_iso: result.applied_at_iso,
      });
      actions.setToast(`保留策略清理完成：${result.total_deleted || 0} 条`, "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setApplying(false);
    }
  }

  return (
    <Panel
      actions={
        <div className="flex gap-2">
          <Button disabled={applying} onClick={() => {
            resetForm();
            setShowForm(true);
          }}>
            添加规则
          </Button>
          <Button disabled={applying || rules.length === 0} onClick={() => void handleApplyNow()} tone="danger">
            {applying ? "执行中…" : "立即执行清理"}
          </Button>
        </div>
      }
      subtitle="为投递事件、健康检查、Webhook 历史、索引元数据和过期备份设置保留天数。"
      title="保留策略"
    >
      {showForm ? (
        <form className="mb-3 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" onSubmit={handleSubmit}>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] gap-2 max-md:grid-cols-1">
            <Field label="清理目标" help="每个目标建议只保留一条规则">
              <select
                className="select"
                disabled={!!editing}
                name="retention_scope"
                value={formTarget}
                onChange={(e) => setFormTarget(e.target.value as MailRetentionRule["target_kind"])}
              >
                {(editing ? RETENTION_SCOPES : availableTargets).map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label} ({s.value})
                  </option>
                ))}
              </select>
            </Field>
            <Field label="保留天数" help="超过天数的记录将被清理">
              <input
                className="input"
                min={1}
                name="retention_days"
                onChange={(e) => setFormDays(e.target.value === "" ? "" : Number(e.target.value))}
                placeholder="例如 90"
                type="number"
                value={formDays}
              />
            </Field>
            <Field label="最少保留条数" help="达到时间条件时仍保留的最小记录数，0 表示不设置保底">
              <input
                className="input"
                min={0}
                name="retention_rows"
                onChange={(e) => setFormKeepMinCount(e.target.value === "" ? "" : Number(e.target.value))}
                placeholder="例如 1000"
                type="number"
                value={formKeepMinCount}
              />
            </Field>
            <div className="flex items-end gap-2">
              <CheckLabel checked={formEnabled} onChange={setFormEnabled}>启用</CheckLabel>
              <Button tone="primary" type="submit">{editing ? "保存修改" : "创建"}</Button>
              <Button onClick={resetForm} type="button">取消</Button>
            </div>
          </div>
          {formError ? <Notice tone="danger"><strong>参数错误：</strong>{formError}</Notice> : null}
        </form>
      ) : null}

      {applyResult ? (
        <Notice tone="warn">
          <strong>上次清理结果：</strong>
          {Object.entries(applyResult.deleted_by_target || {}).map(([k, v]) => `${scopeLabel(k)}=${v}`).join("，") || "0 条记录被删除"}
          （合计 {applyResult.total_deleted} 条）
        </Notice>
      ) : null}

      {loading && rules.length === 0 ? (
        <EmptyState body="正在加载保留策略规则。" title="加载中" />
      ) : rules.length === 0 ? (
        <EmptyState body="尚未配置保留策略。点击右上角「添加规则」开始配置。" title="暂无保留策略" />
      ) : (
        <div className="rounded-lg border border-[var(--line)] overflow-hidden">
          <table className="table">
            <thead>
              <tr>
                <th>清理目标</th>
                <th>状态</th>
                <th>保留天数</th>
                <th>最少保留</th>
                <th>上次清理</th>
                <th>更新</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((r) => (
                <tr key={r.id}>
                  <td>
                    <strong>{scopeLabel(r.target_kind)}</strong>
                    <div className="muted text-xs">{r.target_kind}</div>
                  </td>
                  <td>{r.enabled ? <Pill tone="good">启用</Pill> : <Pill tone="neutral">停用</Pill>}</td>
                  <td>{r.days > 0 ? `${r.days} 天` : "—"}</td>
                  <td>{r.keep_min_count != null ? r.keep_min_count.toLocaleString() : "—"}</td>
                  <td className="mono text-xs">
                    {r.last_run_at_iso ? formatDate(r.last_run_at_iso) : "—"}
                    {r.last_pruned_count ? <div className="muted">清理 {r.last_pruned_count} 条</div> : null}
                    {r.last_error ? <div className="text-[var(--danger)]">{r.last_error}</div> : null}
                  </td>
                  <td className="mono text-xs">{formatDate(r.updated_at_iso || r.created_at_iso || "")}</td>
                  <td>
                    <div className="flex gap-1.5">
                      <Button onClick={() => startEdit(r)}>编辑</Button>
                      <Button onClick={() => void handleDelete(r)} tone="danger">删除</Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {dangerConfirmDialog}
    </Panel>
  );
}

function scopeLabel(scope: string): string {
  return RETENTION_SCOPES.find((s) => s.value === scope)?.label || scope;
}

// ============================================================================
// ---- Backup Section (Manual + Schedules) -----------------------------------
// ============================================================================

function BackupSection({ actions, reload, data: _data }: { actions: AppActions; reload: () => Promise<void>; data: AppData }) {
  const [subTab, setSubTab] = useState<BackupSubTab>("manual");
  const tabs = [
    { id: "manual", label: "手动备份", href: "#", badge: undefined },
    { id: "schedules", label: "定时计划", href: "#", badge: undefined },
  ];
  return (
    <Panel subtitle="备份范围：仅配置、完整数据（含索引），以及定时计划。" title="备份">
      <SubTabs
        activeId={subTab}
        onChange={(id) => setSubTab(id as BackupSubTab)}
        tabs={tabs}
        ariaLabel="备份子标签"
      />
      <div className="mt-3">
        {subTab === "manual" ? <BackupManual actions={actions} reload={reload} /> : <BackupSchedules actions={actions} reload={reload} />}
      </div>
    </Panel>
  );
}

function BackupManual({ actions, reload }: { actions: AppActions; reload: () => Promise<void> }) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const [backups, setBackups] = useState<MailBackup[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [scope, setScope] = useState<MailBackupCreateReq["scope"]>("config");
  const [note, setNote] = useState("");
  const [filterScope, setFilterScope] = useState<string>("all");

  const loadBackups = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await mailBackupList(filterScope === "all" ? undefined : filterScope, 20, 0);
      setBackups(resp.items);
      setTotal(resp.total);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions, filterScope]);

  useEffect(() => {
    void loadBackups();
  }, [loadBackups]);

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCreating(true);
    try {
      const created = await mailBackupCreate({ scope, note: note.trim() || undefined });
      actions.setToast(`备份创建成功：${created.id.slice(0, 8)}（${formatBytesZero(created.size_bytes || 0)}）`, "good");
      setNote("");
      await loadBackups();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setCreating(false);
    }
  }

  function handleDownload(b: MailBackup) {
    window.location.href = mailBackupDownloadUrl(b.id);
  }

  async function handleDelete(b: MailBackup) {
    const ok = await confirmDanger({
      title: "删除备份？",
      body: `删除备份 ${b.id.slice(0, 12)}… （${formatBytesZero(b.size_bytes || 0)}）。此操作不可恢复。`,
      confirmLabel: "确认删除备份",
    });
    if (!ok) return;
    try {
      await mailBackupDelete(b.id);
      actions.setToast("备份已删除", "good");
      await loadBackups();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  return (
    <div className="grid gap-3">
      <form className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-md:grid-cols-1" onSubmit={handleCreate}>
        <Field label="备份范围" help="config 仅配置文件；data_full 为完整数据（含邮件索引）">
          <select
            className="select"
            name="backup_scope"
            value={scope}
            onChange={(e) => setScope(e.target.value as MailBackupCreateReq["scope"])}
          >
            <option value="config">仅配置 (config)</option>
            <option value="data_full">完整数据 (data_full)</option>
          </select>
        </Field>
        <Field label="备注" help="可选，标记此备份用途（如升级前、迁移前）">
          <input
            className="input"
            maxLength={140}
            name="backup_note"
            onChange={(e) => setNote(e.target.value)}
            placeholder="例如：升级 0.9 前快照"
            value={note}
          />
        </Field>
        <Field label="过滤显示" help="显示过滤列表范围">
          <select
            className="select"
            name="backup_filter"
            value={filterScope}
            onChange={(e) => setFilterScope(e.target.value)}
          >
            <option value="all">全部</option>
            <option value="config">仅 config</option>
            <option value="data_full">仅 data_full</option>
          </select>
        </Field>
        <div className="flex items-end">
          <Button disabled={creating} tone="primary" type="submit">
            {creating ? "创建中…" : "创建备份"}
          </Button>
        </div>
      </form>

      {loading && backups.length === 0 ? (
        <EmptyState body="正在加载备份列表。" title="加载中" />
      ) : backups.length === 0 ? (
        <EmptyState body="尚无备份。使用上方表单创建第一个备份。" title="暂无备份" />
      ) : (
        <div className="rounded-lg border border-[var(--line)] overflow-hidden">
          <div className="px-3 py-2 bg-[var(--surface-soft)] border-b border-[var(--line)] text-xs muted">
            共 {total} 份备份
          </div>
          <table className="table">
            <thead>
              <tr>
                <th>ID</th>
                <th>范围</th>
                <th>状态</th>
                <th>大小</th>
                <th>创建时间</th>
                <th>SHA256</th>
                <th>过期</th>
                <th>备注</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id}>
                  <td className="mono text-xs">{b.id.slice(0, 12)}…</td>
                  <td>
                    <Pill tone={b.scope === "data_full" ? "warn" : "good"}>
                      {b.scope === "config" ? "配置" : "完整数据"}
                    </Pill>
                  </td>
                  <td><Pill tone={b.state === "failed" ? "danger" : b.state === "pending" ? "warn" : "neutral"}>{b.state || "completed"}</Pill></td>
                  <td>{formatBytesZero(b.size_bytes || 0)}</td>
                  <td className="mono text-xs">{formatDate(b.created_at_iso)}</td>
                  <td className="mono text-xs">{b.checksum_sha256 ? b.checksum_sha256.slice(0, 8) + "…" : "—"}</td>
                  <td className="mono text-xs">{b.expires_at_iso ? formatDate(b.expires_at_iso) : "永久"}</td>
                  <td className="text-xs">{b.note || "—"}</td>
                  <td>
                    <div className="flex gap-1.5">
                      <Button onClick={() => handleDownload(b)}>下载</Button>
                      <Button onClick={() => void handleDelete(b)} tone="danger">删除</Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {dangerConfirmDialog}
    </div>
  );
}

function BackupSchedules({ actions, reload }: { actions: AppActions; reload: () => Promise<void> }) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const [schedules, setSchedules] = useState<MailBackupSchedule[]>([]);
  const [loading, setLoading] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<MailBackupSchedule | null>(null);

  const [formName, setFormName] = useState("");
  const [formScope, setFormScope] = useState("config");
  const [formEnabled, setFormEnabled] = useState(true);
  const [formCron, setFormCron] = useState("0 2 * * *");
  const [formRetention, setFormRetention] = useState<number>(30);
  const [formError, setFormError] = useState("");

  const loadSchedules = useCallback(async () => {
    setLoading(true);
    try {
      const list = await mailBackupScheduleList();
      setSchedules(list);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions]);

  useEffect(() => {
    void loadSchedules();
  }, [loadSchedules]);

  function resetForm() {
    setShowForm(false);
    setEditing(null);
    setFormName("");
    setFormScope("config");
    setFormEnabled(true);
    setFormCron("0 2 * * *");
    setFormRetention(30);
    setFormError("");
  }

  function startEdit(s: MailBackupSchedule) {
    setEditing(s);
    setShowForm(true);
    setFormName(s.name || "");
    setFormScope(s.scope);
    setFormEnabled(s.enabled);
    setFormCron(s.cron_expr);
    setFormRetention(s.retention_days);
    setFormError("");
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    if (!/^(\S+\s+){4}\S+$/.test(formCron.trim())) {
      setFormError("Cron 表达式需要 5 段（minute hour dom month dow）");
      return;
    }
    if (formRetention <= 0) {
      setFormError("保留天数必须大于 0");
      return;
    }
    try {
      const payload = editing
        ? { ...editing, name: formName.trim() || editing.name, scope: formScope, enabled: formEnabled, cron_expr: formCron.trim(), retention_days: formRetention }
        : { name: formName.trim() || undefined, scope: formScope, enabled: formEnabled, cron_expr: formCron.trim(), retention_days: formRetention };
      const saved = await mailBackupScheduleUpsert(payload);
      setSchedules((prev) => {
        const filtered = prev.filter((s) => s.id !== saved.id);
        return [...filtered, saved];
      });
      actions.setToast(`备份计划已保存：${saved.scope} @ ${saved.cron_expr}`, "good");
      resetForm();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleDelete(s: MailBackupSchedule) {
    const ok = await confirmDanger({
      title: "删除备份计划？",
      body: `将删除 ${s.scope} @ ${s.cron_expr} 的定时计划。已创建的备份不会被删除。`,
      confirmLabel: "确认删除计划",
    });
    if (!ok) return;
    try {
      await mailBackupScheduleDelete(s.id);
      setSchedules((prev) => prev.filter((x) => x.id !== s.id));
      actions.setToast("备份计划已删除", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  return (
    <div className="grid gap-3">
      <div className="flex justify-end">
        <Button onClick={() => { resetForm(); setShowForm(true); }}>添加计划</Button>
      </div>
      {showForm ? (
        <form className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" onSubmit={handleSubmit}>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto_auto] gap-2 max-lg:grid-cols-2 max-md:grid-cols-1">
            <Field label="计划名称" help="可选；为空时后端按范围生成默认名称">
              <input className="input" name="schedule_name" onChange={(e) => setFormName(e.target.value)} value={formName} />
            </Field>
            <Field label="范围">
              <select className="select" name="schedule_scope" value={formScope} onChange={(e) => setFormScope(e.target.value)}>
                <option value="config">仅配置 (config)</option>
                <option value="data_full">完整数据 (data_full)</option>
              </select>
            </Field>
            <Field label="Cron 表达式" help="5 段：分 时 日 月 周，例 0 2 * * *">
              <input className="input mono" name="schedule_cron" onChange={(e) => setFormCron(e.target.value)} value={formCron} />
            </Field>
            <Field label="保留天数">
              <input
                className="input"
                min={1}
                name="schedule_retention"
                onChange={(e) => setFormRetention(Number(e.target.value) || 0)}
                type="number"
                value={formRetention}
              />
            </Field>
            <div className="flex items-end">
              <CheckLabel checked={formEnabled} onChange={setFormEnabled}>启用</CheckLabel>
            </div>
            <div className="flex items-end gap-2">
              <Button tone="primary" type="submit">{editing ? "保存" : "创建"}</Button>
              <Button onClick={resetForm} type="button">取消</Button>
            </div>
          </div>
          {formError ? <Notice tone="danger"><strong>参数错误：</strong>{formError}</Notice> : null}
        </form>
      ) : null}

      {loading && schedules.length === 0 ? (
        <EmptyState body="正在加载备份计划列表。" title="加载中" />
      ) : schedules.length === 0 ? (
        <EmptyState body="尚未配置定时备份计划。" title="暂无备份计划" />
      ) : (
        <div className="rounded-lg border border-[var(--line)] overflow-hidden">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>范围</th>
                <th>状态</th>
                <th>Cron</th>
                <th>保留天数</th>
                <th>上次执行</th>
                <th>下次</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {schedules.map((s) => (
                <tr key={s.id}>
                  <td>
                    <strong>{s.name || s.id.slice(0, 8)}</strong>
                    <div className="mono muted text-xs">{s.id.slice(0, 12)}…</div>
                  </td>
                  <td>{s.scope === "config" ? "配置" : "完整数据"}</td>
                  <td>{s.enabled ? <Pill tone="good">启用</Pill> : <Pill tone="neutral">停用</Pill>}</td>
                  <td className="mono text-xs">{s.cron_expr}</td>
                  <td>{s.retention_days} 天</td>
                  <td className="text-xs">
                    {s.last_run_at_iso ? (
                      <>
                        <span className="mono">{formatDate(s.last_run_at_iso)}</span>
                        {s.last_error ? <Pill tone="danger">失败</Pill> : <Pill tone="good">OK</Pill>}
                        {s.last_error ? <div className="muted text-xs">{s.last_error}</div> : null}
                        {s.last_backup_id ? <div className="mono muted text-xs">{s.last_backup_id.slice(0, 12)}…</div> : null}
                      </>
                    ) : "—"}
                  </td>
                  <td className="mono text-xs">{s.next_run_at_iso ? formatDate(s.next_run_at_iso) : "—"}</td>
                  <td>
                    <div className="flex gap-1.5">
                      <Button onClick={() => startEdit(s)}>编辑</Button>
                      <Button onClick={() => void handleDelete(s)} tone="danger">删除</Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {dangerConfirmDialog}
    </div>
  );
}

// ============================================================================
// ---- Danger Zone: Hard Delete ----------------------------------------------
// ============================================================================

function DangerZone({ actions, reload }: { actions: AppActions; reload: () => Promise<void> }) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const [requirements, setRequirements] = useState<DangerRequirementsResp | null>(null);
  const [generated, setGenerated] = useState<DangerGenerateCodeResp | null>(null);
  const [expiresAt, setExpiresAt] = useState<number>(0); // epoch ms
  const [countdownStart, setCountdownStart] = useState<number>(0);
  const [now, setNow] = useState<number>(Date.now());

  const [accountName, setAccountName] = useState("");
  const [verificationCode, setVerificationCode] = useState("");
  const [boxes, setBoxes] = useState<[boolean, boolean, boolean]>([false, false, false]);

  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<{ deleted_scope: string; backups_kept: boolean; warning: string } | null>(null);

  // Timer tick
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  // Fetch requirements on mount
  useEffect(() => {
    void (async () => {
      try {
        const req = await mailDangerRequirements();
        setRequirements(req);
      } catch (e) {
        actions.setToast(friendlyError(e), "warn");
      }
    })();
  }, [actions]);

  const remainingCodeMs = Math.max(0, expiresAt - now);
  const remainingCodeSec = Math.ceil(remainingCodeMs / 1000);
  const countdownRequired = requirements?.required_elapsed_seconds ?? 60;
  const countdownElapsedSec = countdownStart ? Math.floor((now - countdownStart) / 1000) : 0;
  const countdownRemaining = Math.max(0, countdownRequired - countdownElapsedSec);
  const countdownDone = countdownStart > 0 && countdownRemaining === 0;
  const codeValid = generated && remainingCodeMs > 0;
  const allBoxes = boxes[0] && boxes[1] && boxes[2];
  const inputsFilled = accountName.trim().length > 0 && verificationCode.trim().length > 0;
  const canSubmit = countdownDone && allBoxes && inputsFilled && codeValid && !submitting;

  async function handleGenerate() {
    setResult(null);
    try {
      const resp = await mailDangerGenerateCode();
      setGenerated(resp);
      const exp = new Date(resp.expires_at_iso).getTime();
      const cdStart = new Date(resp.countdown_started_iso).getTime();
      setExpiresAt(exp);
      setCountdownStart(cdStart);
      actions.setToast(`验证码已生成（120 秒内有效，最少等待 ${countdownRequired} 秒）`, "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleSubmit() {
    if (!canSubmit) return;
    const first = await confirmDanger({
      title: "一级确认：硬删除 Mox 全部数据",
      body: "请再次确认你清楚此操作的含义。该操作将彻底擦除 Mox 控制面与数据目录中的全部内容。",
      confirmationText: accountName.trim(),
      confirmationLabel: "请再次输入当前账户名",
    });
    if (!first) return;
    const second = await confirmDanger({
      title: "二级确认：最终确认（不可撤销）",
      body: "这是最后一道防线。点击确认后，所有 Mox 数据将在毫秒级被清除，且无法在 Phantom Lancer 内部恢复。",
      confirmLabel: "最终确认执行",
    });
    if (!second) return;
    setSubmitting(true);
    try {
      const resp = await mailDangerHardDelete({
        account_name: accountName.trim(),
        checkboxes: [...boxes],
        verification_code: verificationCode.trim(),
        countdown_elapsed_seconds: countdownElapsedSec,
      });
      setResult(resp);
      actions.setToast("硬删除操作已完成", "danger");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <CollapsibleSection
      defaultOpen={false}
      subtitle="危险操作区：保留策略立即清理、完整数据硬删除（三级确认）"
      title={<span className="text-[var(--danger)]">Danger Zone</span>}
    >
      {/* 1. Retention apply-now reminder, repeated */}
      <Notice tone="warn">
        <strong>危险操作：</strong> 本区域包含立即清理与不可撤销的硬删除。所有操作会立刻写入磁盘，无法通过 UI 回滚。
      </Notice>

      {result ? (
        <div className="rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-3 text-sm text-[var(--danger)]">
          <strong>硬删除已完成。</strong>
          <div>作用域：{result.deleted_scope}</div>
          <div>备份目录：{result.backups_kept ? "已保留（位于 /backups）" : "未保留"}</div>
          <div className="mt-1">{result.warning}</div>
        </div>
      ) : null}

      {/* HARD DELETE */}
      <section className="grid gap-3 rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--surface)] p-3">
        <header className="flex items-center justify-between gap-2">
          <h3 className="m-0 text-[15px] font-semibold text-[var(--danger)]">
            重置全部 Mail 数据（不可逆）
          </h3>
          <Pill tone="danger">Hard Delete</Pill>
        </header>

        {/* Requirements card */}
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed">
          <strong className="text-sm">安全要求</strong>
          <ul className="mt-2 mb-0 grid gap-1 pl-4">
            <li>生成验证码后必须等待至少 <span className="mono">{countdownRequired} 秒</span>（冷静期）</li>
            <li>验证码在生成后 120 秒内有效</li>
            <li>必须勾选全部 3 个确认复选框</li>
            <li>必须输入完整账户名与 6 位验证码</li>
            {requirements?.note ? <li>{requirements.note}</li> : null}
          </ul>
        </div>

        {/* Step 1: Generate code */}
        <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
          <strong>步骤 1：生成验证码</strong>
          <div className="flex flex-wrap items-center gap-3">
            <Button disabled={submitting} onClick={() => void handleGenerate()} tone="danger">
              生成验证码
            </Button>
            {generated ? (
              <div className="flex items-center gap-2">
                <span className="mono text-2xl font-bold tracking-widest text-[var(--danger)]">
                  {generated.code}
                </span>
                <Pill tone={remainingCodeSec <= 10 ? "danger" : remainingCodeSec <= 30 ? "warn" : "good"}>
                  {remainingCodeSec > 0 ? `剩余 ${remainingCodeSec}s` : "已过期"}
                </Pill>
              </div>
            ) : (
              <span className="muted text-xs">生成后将显示 6 位验证码（仅本次可见）</span>
            )}
          </div>
          <div className="text-xs">
            {countdownStart > 0 ? (
              countdownDone ? (
                <Pill tone="good">60 秒等待期已结束 — 可进入最终确认</Pill>
              ) : (
                <Pill tone="warn">最少 60 秒等待中，还剩 {countdownRemaining} 秒</Pill>
              )
            ) : (
              <span className="muted">等待验证码生成后开始计时</span>
            )}
          </div>
        </div>

        {/* Step 2: Inputs */}
        <div className="grid grid-cols-2 gap-2 max-sm:grid-cols-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
          <strong className="col-span-2">步骤 2：身份验证</strong>
          <Field label="账户全名（邮箱）" help="完整邮箱地址，例如 admin@example.com">
            <input
              className="input mono"
              autoComplete="off"
              name="danger_account"
              onChange={(e) => setAccountName(e.target.value)}
              placeholder="admin@yourdomain.com"
              value={accountName}
            />
          </Field>
          <Field label="6 位验证码" help="来自步骤 1 中的 6 位数字">
            <input
              className="input mono text-lg tracking-[0.4em]"
              autoComplete="off"
              inputMode="numeric"
              maxLength={6}
              name="danger_code"
              onChange={(e) => setVerificationCode(e.target.value.replace(/\D/g, ""))}
              placeholder="000000"
              value={verificationCode}
            />
          </Field>
        </div>

        {/* Step 3: Three checkboxes */}
        <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
          <strong>步骤 3：确认理解（全部 3 项）</strong>
          <CheckLabel checked={boxes[0]} onChange={(v) => setBoxes((b) => [v, b[1], b[2]])}>
            我理解此操作完全不可逆，在 Phantom Lancer 内部没有任何数据恢复手段。
          </CheckLabel>
          <CheckLabel checked={boxes[1]} onChange={(v) => setBoxes((b) => [b[0], v, b[2]])}>
            所有 Mox 收件箱 / 配置 / 域名 / 证书 / DKIM 密钥将被永久删除。/backups 目录中的备份将被保留，我已单独核验其有效性。
          </CheckLabel>
          <CheckLabel checked={boxes[2]} onChange={(v) => setBoxes((b) => [b[0], b[1], v])}>
            Phantom mail 模块所有状态将被重置为空白，需要重新创建账户 / 别名 / 域名 / 证书。
          </CheckLabel>
        </div>

        {/* Step 4: Countdown status */}
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>等待期：</span>
            {countdownDone ? (
              <Pill tone="good">倒计时已完成 — 准备确认</Pill>
            ) : (
              <Pill tone="warn">最小 60 秒等待中…… 剩余 {countdownRemaining} 秒</Pill>
            )}
          </div>
          <div className="mt-2 muted">
            <div>验证码有效：{codeValid ? `是（剩 ${remainingCodeSec}s）` : "否"}</div>
            <div>三项复选框：{allBoxes ? "已全部勾选" : `3/${boxes.filter(Boolean).length}`}</div>
            <div>输入完整：{inputsFilled ? "是" : "否"}</div>
          </div>
        </div>

        {/* Step 5: Final button */}
        <div className="flex justify-end">
          <Button disabled={!canSubmit} onClick={() => void handleSubmit()} tone="danger">
            {submitting ? "执行中…" : "CONFIRM HARD DELETE"}
          </Button>
        </div>
      </section>
      {dangerConfirmDialog}
    </CollapsibleSection>
  );
}
