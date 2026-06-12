import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, MailDomain } from "../../app/types";
import {
  mailAccountList,
  mailAccountCreate,
  mailAccountUpdate,
  mailAccountDelete,
  mailAccountResetPassword,
  mailAccountDisable,
  mailAccountResyncImap,
  type MailAccount,
  type AccountCreateReq,
  type AccountCreateResp,
} from "../../api/client";
import { friendlyError } from "../../api/client";
import { Button, CheckLabel, EmptyState, Field, Metric, Notice, Panel, Pill, SubTabs, Toggle, useDangerConfirm } from "../../components/ui";

type AccountFilterId = "all" | "active" | "disabled" | "admins";

const ACCOUNT_FILTER_TABS: Array<{ id: AccountFilterId; label: string }> = [
  { id: "all", label: "全部" },
  { id: "active", label: "启用" },
  { id: "disabled", label: "已禁用" },
  { id: "admins", label: "管理员" },
];

export function MailAccountsTab({
  actions,
  reload,
  data,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  data: AppData;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const [accounts, setAccounts] = useState<MailAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [domainId, setDomainId] = useState<string>("__all__");
  const [filter, setFilter] = useState<AccountFilterId>("all");

  // Modal state.
  const [createOpen, setCreateOpen] = useState(false);
  const [createdResult, setCreatedResult] = useState<AccountCreateResp | null>(null);
  const [editId, setEditId] = useState<string | null>(null);

  async function load() {
    try {
      setLoading(true);
      const list = await mailAccountList();
      setAccounts(list || []);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const domains = data.mail.domains || [];
  const domainsById = useMemo(() => {
    const map = new Map<string, MailDomain>();
    for (const d of domains) if (d.id) map.set(d.id, d);
    return map;
  }, [domains]);

  function domainLabel(id?: string): string {
    if (!id) return "—";
    const d = domainsById.get(id);
    return d?.domain || id;
  }

  // Filters
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return accounts.filter((a) => {
      if (domainId !== "__all__" && a.domain_id !== domainId) return false;
      if (filter === "active" && a.status !== "active") return false;
      if (filter === "disabled" && a.status !== "disabled") return false;
      if (filter === "admins" && !a.is_admin) return false;
      if (q) {
        const hay = `${a.address || ""} ${a.display_name || ""} ${a.local_part || ""}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [accounts, search, domainId, filter]);

  // Summary metrics
  const total = accounts.length;
  const activeCount = accounts.filter((a) => a.status === "active").length;
  const adminCount = accounts.filter((a) => a.is_admin).length;
  const quotaUsed = accounts.reduce((acc, a) => acc + (a.quota_mb || 0), 0);

  const editingAccount = editId ? accounts.find((a) => a.id === editId) || null : null;

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}

      {/* Summary cards row */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Metric label="总账户数" value={total} detail={`${activeCount} 启用 · ${total - activeCount} 禁用`} />
        <Metric label="启用账户" value={activeCount} tone="good" detail={`占比 ${total ? Math.round((activeCount / total) * 100) : 0}%`} />
        <Metric label="管理员" value={adminCount} tone="danger" detail="拥有邮件域管理与投递权限" />
        <Metric label="配额总和" value={`${quotaUsed} MB`} tone="neutral" detail={quotaUsed === 0 ? "未设定配额（无限）" : "每账户独立配额累加"} />
      </div>

      {/* Toolbar */}
      <div className="flex items-stretch justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <input
            className="input"
            placeholder="搜索邮箱 / 显示名称 / local-part…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <select
            className="input"
            value={domainId}
            onChange={(e) => setDomainId(e.target.value)}
            aria-label="按域名筛选"
          >
            <option value="__all__">全部域名</option>
            {domains.map((d) => (
              <option key={d.id} value={d.id}>{d.domain}</option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          <Button tone="primary" onClick={() => setCreateOpen(true)}>
            + 创建账户
          </Button>
        </div>
      </div>

      {/* SubTabs filters */}
      <SubTabs
        ariaLabel="账户筛选"
        activeId={filter}
        onChange={(id) => setFilter(id as AccountFilterId)}
        tabs={ACCOUNT_FILTER_TABS.map((t) => ({
          id: t.id,
          label: t.label,
          badge:
            t.id === "all" ? total :
            t.id === "active" ? activeCount :
            t.id === "disabled" ? total - activeCount :
            adminCount,
        }))}
      />

      <Panel title="邮箱账户" subtitle="本地邮箱账户管理。创建时会展示一次性密码，请务必在创建后立即保存。">
        {loading ? (
          <EmptyState title="加载中…" body="" />
        ) : filtered.length === 0 ? (
          <EmptyState
            title="暂无符合条件的账户"
            body="点击右上角「+ 创建账户」添加您的第一个邮箱账户。"
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm border-collapse">
              <thead>
                <tr>
                  <th className="text-left py-2 px-3 border-b">账户</th>
                  <th className="text-left py-2 px-3 border-b">域名</th>
                  <th className="text-left py-2 px-3 border-b">密码模式</th>
                  <th className="text-left py-2 px-3 border-b">状态</th>
                  <th className="text-left py-2 px-3 border-b">配额</th>
                  <th className="text-left py-2 px-3 border-b">IMAP 同步</th>
                  <th className="text-left py-2 px-3 border-b">最后登录</th>
                  <th className="py-2 px-3 border-b text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((a) => (
                  <tr key={a.id} className="hover:bg-[var(--surface-soft)]">
                    <td className="py-2 px-3 border-b">
                      <div className="flex items-center gap-2 flex-wrap">
                        <div>
                          <strong>{a.address || `${a.local_part}@${domainLabel(a.domain_id)}`}</strong>
                          {a.display_name ? (
                            <div className="muted text-xs">{a.display_name}</div>
                          ) : null}
                        </div>
                        {a.is_admin ? <Pill tone="danger">ADMIN</Pill> : null}
                      </div>
                    </td>
                    <td className="py-2 px-3 border-b muted text-xs">{domainLabel(a.domain_id)}</td>
                    <td className="py-2 px-3 border-b">
                      <Pill tone={passwordModeTone(a.password_mode)}>{passwordModeLabel(a.password_mode)}</Pill>
                    </td>
                    <td className="py-2 px-3 border-b">
                      <Pill tone={a.status === "active" ? "good" : "neutral"}>
                        {a.status === "active" ? "启用" : "已禁用"}
                      </Pill>
                    </td>
                    <td className="py-2 px-3 border-b text-xs">
                      {a.quota_mb ? `${a.quota_mb} MB` : <span className="muted">不限</span>}
                    </td>
                    <td className="py-2 px-3 border-b">
                      <Pill tone={imapTone(a.imap_sync_state)}>{imapLabel(a.imap_sync_state, a.imap_sync_enabled)}</Pill>
                      {a.imap_error ? (
                        <div className="muted text-[10px] mt-1 max-w-[180px] truncate" title={a.imap_error}>
                          {a.imap_error}
                        </div>
                      ) : null}
                    </td>
                    <td className="py-2 px-3 border-b muted text-xs">
                      {a.last_login_at ? relativeDate(a.last_login_at) : "—"}
                    </td>
                    <td className="py-2 px-3 border-b text-right">
                      <div className="inline-flex gap-1 justify-end flex-wrap">
                        <Button tone="neutral" onClick={() => void handleReset(a)} title="重置密码并生成一次性登录凭证">
                          重置密码
                        </Button>
                        <Button tone="neutral" onClick={() => setEditId(a.id)} title="编辑显示名、配额、管理员等">
                          编辑
                        </Button>
                        <Button tone="neutral" onClick={() => void handleResync(a)} title="强制 IMAP 双向同步重新对齐">
                          重新同步
                        </Button>
                        <Button tone="danger" onClick={() => void handleDelete(a)}>
                          删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {/* Create modal */}
      {createOpen ? (
        <CreateAccountModal
          domains={domains}
          onClose={() => setCreateOpen(false)}
          onCreated={async (resp) => {
            setCreateOpen(false);
            actions.setToast(`已创建账户 ${resp.address}`, "good");
            setCreatedResult(resp);
            await load();
            await reload();
          }}
          csrf={actions.csrf}
          setToast={actions.setToast}
        />
      ) : null}

      {/* Edit drawer */}
      {editingAccount ? (
        <EditAccountDrawer
          account={editingAccount}
          domains={domains}
          onClose={() => setEditId(null)}
          onSaved={async () => {
            setEditId(null);
            await load();
            await reload();
          }}
          csrf={actions.csrf}
          setToast={actions.setToast}
        />
      ) : null}

      {/* Result modal: password shown once */}
      {createdResult ? (
        <AccountCreatedModal result={createdResult} onClose={() => setCreatedResult(null)} />
      ) : null}
    </div>
  );

  // ---- Account actions ----

  async function handleReset(a: MailAccount) {
    const ok = await confirmDanger({
      title: `重置账户 ${a.address || a.id} 的密码`,
      body: (
        <div className="grid gap-2">
          <p>系统将为该账户生成一个新的一次性密码。旧密码会立即失效。</p>
          {a.password_mode === "external" ? (
            <Notice tone="warn">
              <strong>外部认证账户</strong>：该账户当前使用外部认证，重置本地密码可能不会生效，
              建议在外部系统中修改密码，或先切换密码模式为本地。
            </Notice>
          ) : null}
        </div>
      ),
      confirmLabel: "确认重置密码",
      confirmationText: a.address || a.id,
      confirmationLabel: `请输入账户地址 ${a.address || a.id} 以继续`,
    });
    if (!ok) return;
    try {
      const r = await mailAccountResetPassword(a.id, actions.csrf);
      actions.setToast(`密码已重置`, "good");
      // Show the one-time password via a result modal.
      setCreatedResult({
        id: a.id,
        address: a.address || `${a.local_part}@${domainLabel(a.domain_id)}`,
        display_name: a.display_name,
        one_time_password: r.one_time_password,
        created_at: a.updated_at || a.created_at,
      });
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleResync(a: MailAccount) {
    try {
      await mailAccountResyncImap(a.id, actions.csrf);
      actions.setToast(`已请求 IMAP 重新同步：${a.address || a.id}`, "good");
      await load();
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function handleDelete(a: MailAccount) {
    const target = a.address || a.id;
    const ok = await confirmDanger({
      title: `删除账户 ${target}`,
      body: "该账户下的所有邮件、文件夹与投递历史将被移除。此操作无法撤销。",
      confirmLabel: "确认删除",
      confirmationText: `DELETE ${target}`,
      confirmationLabel: `请输入 DELETE ${target} 以继续`,
      impact: ["删除收件箱、已发送、草稿、垃圾邮件等所有文件夹内容",
               "从别名中移除该账户作为收件人的引用（保留别名，仅移除该收件人）",
               "同步到 mox.conf 并 reload 服务"],
    });
    if (!ok) return;
    try {
      await mailAccountDelete(a.id, actions.csrf);
      actions.setToast(`已删除 ${target}`, "good");
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }
}

// ============================================================================
// Subcomponents
// ============================================================================

function CreateAccountModal({
  domains,
  onClose,
  onCreated,
  csrf,
  setToast,
}: {
  domains: MailDomain[];
  onClose: () => void;
  onCreated: (resp: AccountCreateResp) => Promise<void>;
  csrf?: string;
  setToast: (m: string, tone?: "good" | "warn" | "danger" | "neutral") => void;
}) {
  const [domainId, setDomainId] = useState<string>(domains[0]?.id || "");
  const [localPart, setLocalPart] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [passwordMode, setPasswordMode] = useState<"generated" | "external" | "disabled">("generated");
  const [isAdmin, setIsAdmin] = useState(false);
  const [quotaMb, setQuotaMb] = useState<string>("0");
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    if (!domainId) {
      setToast("请选择一个域名", "warn");
      return;
    }
    if (!localPart.trim()) {
      setToast("local-part 不能为空", "warn");
      return;
    }
    const quotaNum = Number(quotaMb);
    if (Number.isNaN(quotaNum) || quotaNum < 0) {
      setToast("配额必须是非负整数（0 = 不限）", "warn");
      return;
    }
    const req: AccountCreateReq = {
      domain_id: domainId,
      local_part: localPart.trim(),
      display_name: displayName.trim() || undefined,
      password_mode: passwordMode,
      quota_mb: quotaNum,
      is_admin: isAdmin,
    };
    setSubmitting(true);
    try {
      const resp = await mailAccountCreate(req, csrf);
      await onCreated(resp);
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  const selectedDomain = domains.find((d) => d.id === domainId);
  const addressPreview = `${localPart.trim() || "<local-part>"}@${selectedDomain?.domain || "<domain>"}`;

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel max-w-xl w-[94%] max-h-[94vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <h3 className="m-0">创建邮箱账户</h3>
          <button type="button" className="text-lg muted hover:text-neutral-12" onClick={onClose} aria-label="关闭">✕</button>
        </div>
        <div className="panel-body grid gap-4">
          <div className="rounded-lg border p-3 text-sm bg-neutral-2 dark:bg-neutral-2-dark">
            <strong className="block mb-1">邮箱地址预览</strong>
            <code className="text-sm break-all">{addressPreview}</code>
          </div>

          <Field label="域名" help="该账户所属的收发域名。">
            <select
              className="input w-full"
              value={domainId}
              onChange={(e) => setDomainId(e.target.value)}
            >
              {domains.length === 0 ? (
                <option value="">（暂无域名，请先在「域名」Tab 添加）</option>
              ) : domains.map((d) => (
                <option key={d.id} value={d.id}>{d.domain}</option>
              ))}
            </select>
          </Field>

          <Field label="用户名 (local-part)" help="示例：alice、support、postmaster。仅允许 ASCII 字母、数字、点、连字符与下划线。">
            <input
              className="input w-full"
              placeholder="例如：alice"
              value={localPart}
              onChange={(e) => setLocalPart(e.target.value)}
            />
          </Field>

          <Field label="显示名称（可选）" help="收件人客户端会展示的友好姓名，例如「张三」。">
            <input
              className="input w-full"
              placeholder="例如：Alice Wang"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </Field>

          <Field label="密码模式">
            <div className="grid gap-2 text-sm">
              <label className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  checked={passwordMode === "generated"}
                  onChange={() => setPasswordMode("generated")}
                />
                <span>
                  <strong>生成一次性密码</strong>
                  <span className="muted text-xs block">Phantom 生成高强度临时密码，登录后由用户自行修改。推荐用于首次创建。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  checked={passwordMode === "external"}
                  onChange={() => setPasswordMode("external")}
                />
                <span>
                  <strong>外部认证</strong>
                  <span className="muted text-xs block">交由外部 LDAP / PAM / OAuth 负责认证。Phantom 不会管理密码。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  checked={passwordMode === "disabled"}
                  onChange={() => setPasswordMode("disabled")}
                />
                <span>
                  <strong>禁用本地登录</strong>
                  <span className="muted text-xs block">仅接收邮件 / 投递出（如 catch-all 投递桶），无法 IMAP/POP/SUB 登录。</span>
                </span>
              </label>
            </div>
          </Field>

          <Field label="配额 (MB)" help="单账户存储上限。输入 0 表示不限配额。">
            <input
              type="number"
              min={0}
              className="input w-full"
              value={quotaMb}
              onChange={(e) => setQuotaMb(e.target.value)}
            />
          </Field>

          <Toggle
            checked={isAdmin}
            onChange={setIsAdmin}
            label={
              <span>
                <strong>管理员账户</strong>
                <span className="muted text-xs block">可登录 webmail 管理域、DKIM、证书等。普通员工请关闭。</span>
              </span>
            }
          />

          <div className="flex justify-end gap-2 pt-2 border-t">
            <Button tone="neutral" onClick={onClose} disabled={submitting}>取消</Button>
            <Button tone="primary" onClick={() => void submit()} disabled={submitting || domains.length === 0}>
              {submitting ? "创建中…" : "创建账户"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function EditAccountDrawer({
  account,
  domains,
  onClose,
  onSaved,
  csrf,
  setToast,
}: {
  account: MailAccount;
  domains: MailDomain[];
  onClose: () => void;
  onSaved: () => Promise<void>;
  csrf?: string;
  setToast: (m: string, tone?: "good" | "warn" | "danger" | "neutral") => void;
}) {
  const [displayName, setDisplayName] = useState(account.display_name || "");
  const [status, setStatus] = useState<string>(account.status || "active");
  const [passwordMode, setPasswordMode] = useState<string>(account.password_mode || "generated");
  const [quotaMb, setQuotaMb] = useState<string>(String(account.quota_mb ?? 0));
  const [isAdmin, setIsAdmin] = useState(!!account.is_admin);
  const [imapSyncEnabled, setImapSyncEnabled] = useState(!!account.imap_sync_enabled);
  const [saving, setSaving] = useState(false);

  async function save() {
    const quotaNum = Number(quotaMb);
    if (Number.isNaN(quotaNum) || quotaNum < 0) {
      setToast("配额必须是非负整数", "warn");
      return;
    }
    setSaving(true);
    try {
      await mailAccountUpdate(account.id, {
        display_name: displayName.trim() || undefined,
        status,
        password_mode: passwordMode,
        quota_mb: quotaNum,
        is_admin: isAdmin,
        imap_sync_enabled: imapSyncEnabled,
      }, csrf);
      setToast("账户配置已更新", "good");
      await onSaved();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setSaving(false);
    }
  }

  async function disable() {
    try {
      await mailAccountDisable(account.id, csrf);
      setToast("已禁用账户，所有登录会话已失效", "warn");
      await onSaved();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-end sm:place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel w-full sm:max-w-xl sm:w-[94%] max-h-[92vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <div>
            <h3 className="m-0">编辑账户</h3>
            <p className="muted text-xs mt-1 mb-0">{account.address || `${account.local_part}@${domains.find((d) => d.id === account.domain_id)?.domain || ""}`}</p>
          </div>
          <button type="button" className="text-lg muted hover:text-neutral-12" onClick={onClose} aria-label="关闭">✕</button>
        </div>
        <div className="panel-body grid gap-4">
          <Field label="显示名称（可选）">
            <input
              className="input w-full"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </Field>

          <Field label="状态">
            <select className="input w-full" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="active">启用</option>
              <option value="disabled">禁用（禁止投递与登录）</option>
            </select>
          </Field>

          <Field label="密码模式">
            <select
              className="input w-full"
              value={passwordMode}
              onChange={(e) => setPasswordMode(e.target.value)}
            >
              <option value="generated">本地（生成一次性密码）</option>
              <option value="external">外部认证</option>
              <option value="disabled">禁用登录</option>
            </select>
          </Field>

          <Field label="配额 (MB)" help="0 表示不限。">
            <input
              type="number"
              min={0}
              className="input w-full"
              value={quotaMb}
              onChange={(e) => setQuotaMb(e.target.value)}
            />
          </Field>

          <Toggle checked={isAdmin} onChange={setIsAdmin} label="管理员账户（可管理域、证书等）" />
          <Toggle checked={imapSyncEnabled} onChange={setImapSyncEnabled} label="启用 IMAP 双向同步" />

          <div className="flex justify-between gap-2 pt-2 border-t flex-wrap">
            <Button tone="neutral" onClick={() => void disable()} disabled={saving}>
              立即禁用（失效所有会话）
            </Button>
            <div className="flex gap-2">
              <Button tone="neutral" onClick={onClose} disabled={saving}>取消</Button>
              <Button tone="primary" onClick={() => void save()} disabled={saving}>
                {saving ? "保存中…" : "保存修改"}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function AccountCreatedModal({
  result,
  onClose,
}: {
  result: AccountCreateResp;
  onClose: () => void;
}) {
  const [savedCheck, setSavedCheck] = useState(false);
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard?.writeText(result.one_time_password);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/50 backdrop-blur-sm" onClick={() => savedCheck && onClose()}>
      <div
        role="dialog"
        className="panel max-w-lg w-[94%]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header" style={{ backgroundColor: "var(--danger-soft)" }}>
          <div>
            <div className="mb-2"><Pill tone="danger">ACCOUNT CREATED — SAVE THIS PASSWORD NOW</Pill></div>
            <h3 className="m-0" style={{ color: "var(--danger)" }}>
              账户 {result.address} 已创建
            </h3>
            {result.display_name ? (
              <p className="muted text-xs mt-1 mb-0">{result.display_name}</p>
            ) : null}
          </div>
        </div>
        <div className="panel-body grid gap-4">
          <Notice tone="danger">
            <div className="grid gap-2">
              <strong>请立即保存下方密码</strong>
              <p className="text-xs mb-0">
                该密码<strong>仅展示一次</strong>，关闭后将无法再次查看。
                若丢失，只能通过「重置密码」重新生成一次性密码。
              </p>
            </div>
          </Notice>

          <div className="rounded-lg border p-4 bg-neutral-2 dark:bg-neutral-2-dark grid gap-3">
            <div>
              <div className="muted text-xs mb-1">邮箱地址</div>
              <code className="text-sm break-all block">{result.address}</code>
            </div>
            <div>
              <div className="muted text-xs mb-1">一次性密码</div>
              <div className="flex items-center gap-2 flex-wrap">
                <code className="text-lg break-all font-mono bg-neutral-1 dark:bg-neutral-0-dark rounded px-2 py-1 border flex-1 min-w-[200px]">
                  {result.one_time_password}
                </code>
                <Button tone="primary" onClick={() => void copy()}>
                  {copied ? "已复制 ✓" : "📋 复制密码"}
                </Button>
              </div>
            </div>
          </div>

          <CheckLabel checked={savedCheck} onChange={setSavedCheck}>
            <strong>我已安全保存该密码（如密码管理器、离线加密笔记等）</strong>
          </CheckLabel>

          <div className="flex justify-end pt-1">
            <Button tone="primary" disabled={!savedCheck} onClick={onClose}>
              关闭
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ============================================================================
// Helpers
// ============================================================================

function passwordModeTone(m?: MailAccount["password_mode"]): "good" | "neutral" | "warn" {
  switch (m) {
    case "generated": return "good";
    case "external": return "neutral";
    case "disabled": return "warn";
    default: return "neutral";
  }
}
function passwordModeLabel(m?: MailAccount["password_mode"]): string {
  switch (m) {
    case "generated": return "本地 · 生成";
    case "external": return "外部认证";
    case "disabled": return "登录已禁用";
    default: return m || "—";
  }
}
function imapTone(s?: MailAccount["imap_sync_state"]): "good" | "warn" | "danger" | "neutral" {
  switch (s) {
    case "idle": return "good";
    case "syncing": return "neutral";
    case "error": return "danger";
    case "paused": return "warn";
    default: return "neutral";
  }
}
function imapLabel(s?: MailAccount["imap_sync_state"], enabled?: boolean): string {
  if (!enabled) return "未启用";
  switch (s) {
    case "idle": return "空闲";
    case "syncing": return "同步中";
    case "error": return "出错";
    case "paused": return "已暂停";
    default: return s || "—";
  }
}
function relativeDate(s?: string): string {
  if (!s) return "—";
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return s;
  const diffMs = Date.now() - d.getTime();
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return "刚刚";
  if (mins < 60) return `${mins} 分钟前`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs} 小时前`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days} 天前`;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}
