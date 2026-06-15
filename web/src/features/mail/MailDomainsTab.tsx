import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import {
  mailDomainList,
  mailDomainCreate,
  mailDomainUpdate,
  mailDomainDelete,
  mailDomainEnable,
  mailDomainDNSCheck,
  mailDomainDNSRecords,
  mailResolveDrift,
  type MailDomain,
  type MailDomainDNSStatus,
  type MailDNSRecord,
} from "../../api/client";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Panel, Pill, useDangerConfirm } from "../../components/ui";

export function MailDomainsTab({
  actions,
  status,
  reload,
}: {
  actions: AppActions;
  status: { drifted?: boolean } | null;
  reload: () => Promise<void>;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const [items, setItems] = useState<MailDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [dnsRecords, setDnsRecords] = useState<MailDNSRecord[]>([]);
  const [drifted, setDrifted] = useState<boolean>(false);

  async function load() {
    try {
      const resp = await mailDomainList();
      setItems(resp.items || []);
      setDrifted(!!resp.drifted);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function resolveDrift(action: "overwrite" | "reimport") {
    const ok = await confirmDanger({
      title: action === "overwrite" ? "以 Phantom 配置覆盖磁盘" : "以磁盘配置回导 Phantom",
      body:
        action === "overwrite"
          ? "Phantom 将重新 apply 配置到磁盘，覆盖手工改动。"
          : "Phantom 将读取磁盘配置并覆盖 SQLite 中的配置记录。",
      confirmLabel: action === "overwrite" ? "确认覆盖磁盘" : "确认回导",
      confirmationText: action.toUpperCase(),
      confirmationLabel: `请输入 ${action.toUpperCase()} 以继续`,
    });
    if (!ok) return;
    try {
      const r = await mailResolveDrift({ action }, actions.csrf);
      actions.setToast(
        `${r.accepted ? "已消解漂移" : "漂移消解失败"}：${r.message || ""}`,
        r.accepted ? "good" : "danger",
      );
      if (r.accepted) {
        setDrifted(false);
        await Promise.all([load(), reload()]);
      }
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function loadDNSRecords(id: string) {
    try {
      const r = await mailDomainDNSRecords(id);
      setDnsRecords(r.items);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function runDNSCheck(d: MailDomain) {
    try {
      const s = await mailDomainDNSCheck(d.id || "", actions.csrf);
      setItems((old) => old.map((x) => (x.id === d.id ? { ...x, dns_status: s } : x)));
      const overall = s.overall || "unknown";
      actions.setToast(
        `DNS 检查完成：${overall}`,
        overall === "good" ? "good" : overall === "critical" || overall === "error" ? "danger" : "warn",
      );
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function toggleEnabled(d: MailDomain, enable: boolean) {
    try {
      const r = await mailDomainEnable(d.id || "", enable, actions.csrf);
      setItems((old) => old.map((x) => (x.id === d.id ? r : x)));
      actions.setToast(`${enable ? "已启用" : "已禁用"} 域名 ${r.domain}`, "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function removeDomain(d: MailDomain) {
    const ok = await confirmDanger({
      title: `删除域名 ${d.domain}`,
      body: "此操作会同步删除该域名下所有邮箱账户。Mox reload 成功后不可撤销。",
      confirmLabel: "确认删除",
      confirmationText: d.domain,
      confirmationLabel: `请输入域名 ${d.domain} 以继续`,
      impact: ["删除该域名下的所有邮箱账户", "相关 DNS 记录清单不再维护"],
    });
    if (!ok) return;
    try {
      await mailDomainDelete(d.id || "", actions.csrf);
      actions.setToast(`已删除 ${d.domain}`, "good");
      await Promise.all([load(), reload()]);
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  const detailDomain = detailId ? items.find((d) => d.id === detailId) : undefined;

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}

      {/* Drift banner */}
      {status?.drifted ?? drifted ? (
        <section className="panel" style={{ borderColor: "var(--danger)" }}>
          <div className="panel-header" style={{ backgroundColor: "var(--danger-soft)" }}>
            <div>
              <h2 className="m-0 text-sm font-semibold" style={{ color: "var(--danger)" }}>
                检测到配置漂移
              </h2>
              <p className="muted mt-1 mb-0 text-xs">磁盘上的 mox.conf 与 Phantom 最后同步的版本不一致。请选择一种处理方式后再进行写操作。</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button tone="danger" onClick={() => resolveDrift("overwrite")}>
                以 Phantom 覆盖磁盘
              </Button>
              <Button tone="danger" onClick={() => resolveDrift("reimport")}>
                以磁盘回导
              </Button>
            </div>
          </div>
          <div className="panel-body">
            <div className="flex items-center gap-2">
              <Pill tone="danger">CONFIG DRIFTED</Pill>
              <span className="muted text-xs">选择上方操作按钮以消解漂移。</span>
            </div>
          </div>
        </section>
      ) : null}

      {/* Table header row */}
      <div className="flex items-center justify-between gap-2">
        <strong>域名管理</strong>
        <Button tone="primary" onClick={() => setCreateOpen(true)}>
          添加域名
        </Button>
      </div>

      {/* Table panel */}
      <Panel
        title=""
        subtitle="添加收发域名后，Phantom 会自动生成 8 项推荐 DNS 记录清单。"
      >
        {loading || items.length === 0 ? (
          <EmptyState
            title={loading ? "加载中…" : "暂无域名"}
            body="添加收发域名后，Phantom 会自动生成 8 项推荐 DNS 记录清单。"
          />
        ) : (
          <DomainsTable
            items={items}
            onDetail={(d) => {
              setDetailId(d.id || "");
              void loadDNSRecords(d.id || "");
            }}
            onCheck={(d) => void runDNSCheck(d)}
            onEdit={(d) => setEditId(d.id || "")}
            onToggle={(d, enable) => void toggleEnabled(d, enable)}
            onDelete={(d) => void removeDomain(d)}
          />
        )}
      </Panel>

      {/* Create Modal */}
      {createOpen ? (
        <DomainModal
          initial={defaultDomain()}
          title="添加域名"
          onClose={() => setCreateOpen(false)}
          onSubmit={async (d) => {
            const r = await mailDomainCreate(d, actions.csrf);
            actions.setToast(`已添加域名 ${r.domain}`, "good");
            await Promise.all([load(), reload()]);
            setCreateOpen(false);
          }}
        />
      ) : null}

      {/* Edit Modal */}
      {editId ? (
        <DomainModal
          initial={items.find((d) => d.id === editId) || defaultDomain()}
          title="编辑域名"
          onClose={() => setEditId(null)}
          onSubmit={async (d) => {
            const r = await mailDomainUpdate(editId, d, actions.csrf);
            actions.setToast(`已更新 ${r.domain}`, "good");
            await Promise.all([load(), reload()]);
            setEditId(null);
          }}
        />
      ) : null}

      {/* DNS Records Modal */}
      {detailId ? (
        <DNSRecordsModal
          title={`DNS 记录 — ${detailDomain?.domain || detailId}`}
          records={dnsRecords}
          onClose={() => setDetailId(null)}
        />
      ) : null}
    </div>
  );
}

function DomainsTable({
  items, onDetail, onCheck, onEdit, onToggle, onDelete }: {
  items: MailDomain[];
  onDetail: (d: MailDomain) => void;
  onCheck: (d: MailDomain) => void;
  onEdit: (d: MailDomain) => void;
  onToggle: (d: MailDomain, enable: boolean) => void;
  onDelete: (d: MailDomain) => void;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-[var(--line)]">
            <th className="muted px-2 py-2 text-xs font-medium">域名</th>
            <th className="muted px-2 py-2 text-xs font-medium">DKIM 选择器</th>
            <th className="muted px-2 py-2 text-xs font-medium">DNS</th>
            <th className="muted px-2 py-2 text-xs font-medium">邮箱</th>
            <th className="muted px-2 py-2 text-xs font-medium">同步</th>
            <th className="muted px-2 py-2 text-xs font-medium">状态</th>
            <th className="muted px-2 py-2 text-xs font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          {items.map((d) => (
            <tr className="border-b border-[var(--line)] last:border-b-0" key={d.id || d.domain}>
              <td className="px-2 py-2 align-top">
                <code className="text-xs">{d.domain}</code>
              </td>
              <td className="px-2 py-2 align-top">
                <code className="text-xs">{d.dkim_selector || "mox"}</code>
              </td>
              <td className="px-2 py-2 align-top">
                <DNSDots status={d.dns_status} />
              </td>
              <td className="px-2 py-2 align-top">{d.account_count || 0}</td>
              <td className="px-2 py-2 align-top">
                {d.synced ? <Pill tone="good">已同步</Pill> : <Pill tone="warn">待应用</Pill>}
              </td>
              <td className="px-2 py-2 align-top">
                {d.enabled ? <Pill tone="good">启用</Pill> : <Pill tone="neutral">禁用</Pill>}
              </td>
              <td className="px-2 py-2 align-top">
                <div className="flex flex-wrap items-center gap-1">
                  <button
                    className="button text-xs"
                    onClick={() => onDetail(d)}
                    type="button"
                  >
                    DNS 记录
                  </button>
                  <button
                    className="button text-xs"
                    onClick={() => onCheck(d)}
                    type="button"
                  >
                    检查
                  </button>
                  <button
                    className="button text-xs"
                    onClick={() => onEdit(d)}
                    type="button"
                  >
                    编辑
                  </button>
                  <button
                    className={`button text-xs ${d.enabled ? "" : "button-primary"}`}
                    onClick={() => onToggle(d, !d.enabled)}
                    type="button"
                  >
                    {d.enabled ? "禁用" : "启用"}
                  </button>
                  <button
                    className="button button-danger text-xs"
                    onClick={() => onDelete(d)}
                    type="button"
                  >
                    删除
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function DNSDots({ status }: { status: MailDomainDNSStatus }) {
  const rows: Array<[keyof MailDomainDNSStatus, string]> = [
    ["mx_ok", "MX"],
    ["spf_ok", "SPF"],
    ["dkim_ok", "DKIM"],
    ["dmarc_ok", "DMARC"],
    ["tlsrpt_ok", "TLSRPT"],
    ["ptr_ok", "PTR"],
    ["tlsa_ok", "TLSA"],
    ["autoconfig_ok", "AUTO"],
  ];
  return (
    <div className="flex flex-wrap gap-1">
      {rows.map(([k, label]) => {
        const v = status?.[k];
        const bgColor =
          v === undefined ? "var(--muted)" : v ? "var(--good)" : "var(--danger)";
        return (
          <span
            key={label}
            className="group relative inline-flex items-center"
            title={`${label}: ${v === undefined ? "未检查" : v ? "正确" : "缺失/错误"}`}
          >
            <span
              className="inline-block h-3 w-3 rounded-full border border-[var(--line)]"
              style={{ backgroundColor: bgColor }}
            />
            <span className="ml-0.5 text-[10px] uppercase tracking-tight text-[var(--muted-strong)]">
              {label}
            </span>
          </span>
        );
      })}
    </div>
  );
}

function defaultDomain(): MailDomain {
  return {
    domain: "",
    dkim_selector: "mox",
    dmarc_policy: "quarantine",
    dmarc_rua: "",
    spf_include: "",
    enabled: true,
    synced: false,
    dns_status: { overall: "unknown" },
    dns_records: [],
  };
}

function DomainModal({
  initial,
  title,
  onClose,
  onSubmit,
}: {
  initial: MailDomain;
  title: string;
  onClose: () => void;
  onSubmit: (d: MailDomain) => Promise<void>;
}) {
  const [form, setForm] = useState<MailDomain>(initial);
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof MailDomain>(k: K, v: MailDomain[K]) =>
    setForm((old) => ({ ...old, [k]: v }));

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.56)] p-4"
      onClick={onClose}
    >
      <div
        aria-modal="true"
        className="w-full max-w-lg overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="border-b border-[var(--line)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">{title}</h2>
        </div>
        <div className="grid gap-3 p-4 text-sm">
          <label className="field">
            <span>域名</span>
            <input
              autoFocus
              className="input"
              name="domain"
              onChange={(e) => set("domain", e.target.value)}
              placeholder="mail.example.com"
              value={form.domain}
            />
          </label>
          <label className="field">
            <span>DKIM 选择器</span>
            <input
              className="input"
              name="dkim_selector"
              onChange={(e) => set("dkim_selector", e.target.value)}
              placeholder="mox"
              value={form.dkim_selector}
            />
          </label>
          <label className="field">
            <span>DMARC 策略 (none / quarantine / reject)</span>
            <input
              className="input"
              name="dmarc_policy"
              onChange={(e) =>
                set(
                  "dmarc_policy",
                  (e.target.value as "none" | "quarantine" | "reject") || "quarantine",
                )
              }
              placeholder="quarantine"
              value={form.dmarc_policy}
            />
          </label>
          <label className="field">
            <span>DMARC RUA (聚合报告邮件地址)</span>
            <input
              className="input"
              name="dmarc_rua"
              onChange={(e) => set("dmarc_rua", e.target.value)}
              placeholder="mailto:dmarc@example.com"
              value={form.dmarc_rua}
            />
          </label>
          <label className="field">
            <span>SPF include</span>
            <input
              className="input"
              name="spf_include"
              onChange={(e) => set("spf_include", e.target.value)}
              placeholder="_spf.mail.example.com"
              value={form.spf_include}
            />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              checked={form.enabled}
              onChange={(e) => set("enabled", e.target.checked)}
              type="checkbox"
            />
            启用域名
          </label>
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-4 py-3">
          <Button onClick={onClose}>取消</Button>
          <Button
            disabled={busy || !form.domain}
            tone="primary"
            onClick={async () => {
              setBusy(true);
              try {
                await onSubmit(form);
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? "提交中…" : "提交"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function DNSRecordsModal({
  title, records, onClose }: { title: string; records: MailDNSRecord[]; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.56)] p-4"
      onClick={onClose}
    >
      <div
        aria-modal="true"
        className="w-full max-w-4xl max-h-[90dvh] overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="border-b border-[var(--line)] px-4 py-3 flex items-center justify-between">
          <h2 className="m-0 text-sm font-semibold">{title}</h2>
        </div>
        <div className="grid gap-2 p-4 overflow-auto">
          <p className="muted text-xs">
            将以下记录添加到域名注册商或 DNS 托管处。记录名通常需加{" "}
            <code className="px-1">.yourdomain.com.</code> 后缀（根记录名直接填{" "}
            <code>@</code>）。
          </p>
          {records.length === 0 ? (
            <EmptyState title="暂无 DNS 记录" body="Phantom 尚未生成该域名的推荐 DNS 记录清单。" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)]">
                    <th className="muted px-2 py-2 text-xs font-medium">类型</th>
                    <th className="muted px-2 py-2 text-xs font-medium">名称</th>
                    <th className="muted px-2 py-2 text-xs font-medium">值</th>
                    <th className="muted px-2 py-2 text-xs font-medium">TTL</th>
                    <th className="muted px-2 py-2 text-xs font-medium">检查</th>
                  </tr>
                </thead>
                <tbody>
                  {records.map((r, idx) => (
                    <tr className="border-b border-[var(--line)] last:border-b-0" key={`${r.type}-${r.name}-${idx}`}>
                      <td className="px-2 py-2 align-top">
                      <code className="text-xs">{r.type}</code>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <code className="break-all text-xs">{r.name}</code>
                      </td>
                      <td className="px-2 py-2 align-top">
                        <code className="break-all text-xs">{r.value}</code>
                      </td>
                      <td className="px-2 py-2 align-top muted text-xs">
                        {r.ttl ? `${r.ttl}s` : "自动"}
                      </td>
                      <td className="px-2 py-2 align-top">
                        {r.checked ? (
                          r.ok ? (
                            <Pill tone="good">匹配</Pill>
                          ) : (
                            <Pill tone="warn">未匹配</Pill>
                          )
                        ) : (
                          <Pill tone="neutral">未检查</Pill>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-4 py-3">
          <Button onClick={onClose}>关闭</Button>
        </div>
      </div>
    </div>
  );
}
