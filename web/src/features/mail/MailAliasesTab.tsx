import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, MailDomain } from "../../app/types";
import {
  mailAliasList,
  mailAliasUpsert,
  mailAliasUpdate,
  mailAliasDelete,
  type MailAlias,
  type AliasUpsertReq,
} from "../../api/client";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Field, Metric, Panel, Pill, SubTabs, Toggle, useDangerConfirm } from "../../components/ui";

type AliasFilterId = "all" | "alias" | "catchall" | "list" | "drop";

const ALIAS_FILTER_TABS: Array<{ id: AliasFilterId; label: string }> = [
  { id: "all", label: "全部" },
  { id: "alias", label: "别名" },
  { id: "catchall", label: "Catch-all" },
  { id: "list", label: "邮件列表" },
  { id: "drop", label: "丢弃" },
];

export function MailAliasesTab({
  actions,
  reload,
  data,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  data: AppData;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const [items, setItems] = useState<MailAlias[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [domainId, setDomainId] = useState<string>("__all__");
  const [filter, setFilter] = useState<AliasFilterId>("all");

  // Modal state
  const [createOpen, setCreateOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);

  async function load() {
    try {
      setLoading(true);
      const list = await mailAliasList();
      setItems(list || []);
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

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return items.filter((a) => {
      if (domainId !== "__all__" && a.domain_id !== domainId) return false;
      if (filter !== "all" && a.mode !== filter) return false;
      if (q) {
        const rec = (a.recipients || []).join(" ").toLowerCase();
        const hay = `${a.source || ""} ${a.list_name || ""} ${a.description || ""} ${rec}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [items, search, domainId, filter]);

  const total = items.length;
  const catchallCount = items.filter((a) => a.mode === "catchall").length;
  const listCount = items.filter((a) => a.mode === "list").length;
  const dropCount = items.filter((a) => a.mode === "drop").length;

  const editingAlias = editId ? items.find((a) => a.id === editId) || null : null;

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}

      {/* Summary */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Metric label="别名总数" value={total} detail={`${total - catchallCount - listCount - dropCount} 个普通 alias`} />
        <Metric label="Catch-all" value={catchallCount} tone="warn" detail="命中未匹配账户时投递的兜底别名" />
        <Metric label="邮件列表" value={listCount} tone="neutral" detail="按列表名转发到多人" />
        <Metric label="丢弃" value={dropCount} tone="danger" detail="直接丢弃收件人的别名" />
      </div>

      {/* Toolbar */}
      <div className="flex items-stretch justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <input
            className="input"
            placeholder="搜索别名、收件人、描述…"
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
            + 创建别名
          </Button>
        </div>
      </div>

      <SubTabs
        ariaLabel="别名模式筛选"
        activeId={filter}
        onChange={(id) => setFilter(id as AliasFilterId)}
        tabs={ALIAS_FILTER_TABS.map((t) => ({
          id: t.id,
          label: t.label,
          badge:
            t.id === "all" ? total :
            t.id === "alias" ? total - catchallCount - listCount - dropCount :
            t.id === "catchall" ? catchallCount :
            t.id === "list" ? listCount :
            dropCount,
        }))}
      />

      <Panel title="别名与转发" subtitle="维护 alias（一对多转发）、catch-all、邮件列表及丢弃规则。">
        {loading ? (
          <EmptyState title="加载中…" body="" />
        ) : filtered.length === 0 ? (
          <EmptyState
            title="暂无符合条件的别名"
            body="点击右上角「+ 创建别名」添加您的第一条别名规则。"
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm border-collapse">
              <thead>
                <tr>
                  <th className="text-left py-2 px-3 border-b">别名 / 源</th>
                  <th className="text-left py-2 px-3 border-b">模式</th>
                  <th className="text-left py-2 px-3 border-b">收件人</th>
                  <th className="text-left py-2 px-3 border-b">状态</th>
                  <th className="text-left py-2 px-3 border-b">描述</th>
                  <th className="py-2 px-3 border-b text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((a) => (
                  <tr key={a.id} className="hover:bg-[var(--surface-soft)]">
                    <td className="py-2 px-3 border-b">
                      <div className="flex items-center gap-2 flex-wrap">
                        <strong>{a.source || "(未设置)"}</strong>
                        <span className="muted text-xs">@{domainLabel(a.domain_id)}</span>
                      </div>
                      {a.mode === "list" && a.list_name ? (
                        <div className="muted text-xs">列表：{a.list_name}</div>
                      ) : null}
                    </td>
                    <td className="py-2 px-3 border-b">
                      <Pill tone={modeTone(a.mode)}>{modeLabel(a.mode)}</Pill>
                    </td>
                    <td className="py-2 px-3 border-b">
                      {a.mode === "drop" ? (
                        <span className="muted text-xs">— (丢弃)</span>
                      ) : (
                        <div
                          className="inline-flex flex-col"
                          title={(a.recipients || []).join("\n")}
                        >
                          <Pill tone="neutral">{(a.recipients || []).length} 个收件人</Pill>
                          {(a.recipients || []).length > 0 ? (
                            <div className="muted text-[11px] mt-1 max-w-[260px] truncate">
                              {a.recipients[0]}
                              {a.recipients.length > 1 ? ` +${a.recipients.length - 1}` : ""}
                            </div>
                          ) : null}
                        </div>
                      )}
                    </td>
                    <td className="py-2 px-3 border-b">
                      <label className="inline-flex items-center gap-2 text-xs cursor-pointer">
                        <input
                          type="checkbox"
                          checked={!!a.enabled}
                          onChange={(e) => void toggleEnabled(a, e.target.checked)}
                        />
                        <Pill tone={a.enabled ? "good" : "neutral"}>{a.enabled ? "启用" : "已禁用"}</Pill>
                      </label>
                    </td>
                    <td className="py-2 px-3 border-b muted text-xs max-w-[240px] truncate" title={a.description}>
                      {a.description || "—"}
                    </td>
                    <td className="py-2 px-3 border-b text-right">
                      <div className="inline-flex gap-1 justify-end flex-wrap">
                        <Button tone="neutral" onClick={() => setEditId(a.id)}>编辑</Button>
                        <Button tone="danger" onClick={() => void handleDelete(a)}>删除</Button>
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
        <AliasUpsertModal
          alias={null}
          domains={domains}
          onClose={() => setCreateOpen(false)}
          onSaved={async () => {
            setCreateOpen(false);
            await load();
            await reload();
          }}
          csrf={actions.csrf}
          setToast={actions.setToast}
        />
      ) : null}

      {/* Edit drawer */}
      {editingAlias ? (
        <AliasUpsertModal
          alias={editingAlias}
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
    </div>
  );

  // ---- Alias actions ----

  async function toggleEnabled(a: MailAlias, enabled: boolean) {
    try {
      await mailAliasUpdate(a.id, { enabled }, actions.csrf);
      setItems((old) => old.map((x) => (x.id === a.id ? { ...x, enabled } : x)));
      actions.setToast(`${enabled ? "已启用" : "已禁用"} ${a.source}`, "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
      await load(); // Re-sync state.
    }
  }

  async function handleDelete(a: MailAlias) {
    const target = a.source || a.id;
    const ok = await confirmDanger({
      title: `删除别名 ${target}`,
      body: `删除后，发往 ${target} 的邮件将不再按该规则转发。`,
      confirmLabel: "确认删除",
      confirmationText: target.toUpperCase(),
      confirmationLabel: `请输入 ${target.toUpperCase()} 以继续`,
    });
    if (!ok) return;
    try {
      await mailAliasDelete(a.id, actions.csrf);
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

function AliasUpsertModal({
  alias,
  domains,
  onClose,
  onSaved,
  csrf,
  setToast,
}: {
  alias: MailAlias | null;
  domains: MailDomain[];
  onClose: () => void;
  onSaved: () => Promise<void>;
  csrf?: string;
  setToast: (m: string, tone?: "good" | "warn" | "danger" | "neutral") => void;
}) {
  const initialDomainId = alias?.domain_id || domains[0]?.id || "";
  const initialMode: MailAlias["mode"] = alias?.mode || "alias";
  const initialSource = alias ? (alias.source || "").replace(/^@/, "") : "";
  const [domainId, setDomainId] = useState<string>(initialDomainId);
  const [mode, setMode] = useState<MailAlias["mode"]>(initialMode);
  const [source, setSource] = useState<string>(initialSource);
  const [recipientsText, setRecipientsText] = useState<string>(
    alias ? (alias.recipients || []).join(", ") : "",
  );
  const [listName, setListName] = useState<string>(alias?.list_name || "");
  const [listReplyTo, setListReplyTo] = useState<string>(alias?.list_reply_to || "");
  const [description, setDescription] = useState<string>(alias?.description || "");
  const [enabled, setEnabled] = useState<boolean>(alias?.enabled ?? true);
  const [saving, setSaving] = useState(false);

  const selectedDomain = domains.find((d) => d.id === domainId);
  const sourceDisabled = mode === "catchall";

  async function submit() {
    if (!domainId) {
      setToast("请选择域名", "warn");
      return;
    }
    const finalSource = mode === "catchall" ? `@${selectedDomain?.domain || ""}` : source.trim();
    if (mode !== "catchall" && mode !== "drop" && !finalSource) {
      setToast("别名源不能为空", "warn");
      return;
    }
    if (mode === "catchall" && !selectedDomain) {
      setToast("Catch-all 需要选定一个域名", "warn");
      return;
    }
    const recipients = parseRecipients(recipientsText);
    if (mode !== "drop" && recipients.length === 0) {
      setToast("需要至少一个有效收件人（drop 模式除外）", "warn");
      return;
    }

    const req: AliasUpsertReq = {
      id: alias ? alias.id : undefined,
      domain_id: domainId,
      source: finalSource,
      recipients,
      mode,
      list_name: mode === "list" ? listName.trim() || undefined : undefined,
      list_reply_to: mode === "list" ? listReplyTo.trim() || undefined : undefined,
      description: description.trim() || undefined,
      enabled,
    };

    setSaving(true);
    try {
      if (alias) {
        await mailAliasUpdate(alias.id, req, csrf);
        setToast("别名已更新", "good");
      } else {
        await mailAliasUpsert(req, csrf);
        setToast("别名已创建", "good");
      }
      await onSaved();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setSaving(false);
    }
  }

  const sourcePreview =
    mode === "catchall"
      ? `@${selectedDomain?.domain || "<domain>"}`
      : mode === "drop"
      ? (source.trim() ? `${source.trim()}@${selectedDomain?.domain || ""}` : "<drop-match>")
      : `${source.trim() || "<source>"}@${selectedDomain?.domain || ""}`;

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel max-w-xl w-[94%] max-h-[94vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <h3 className="m-0">{alias ? "编辑别名" : "创建别名"}</h3>
          <button type="button" className="text-lg muted hover:text-neutral-12" onClick={onClose} aria-label="关闭">关闭</button>
        </div>
        <div className="panel-body grid gap-4">
          <div className="rounded-lg border p-3 text-sm bg-neutral-2 dark:bg-neutral-2-dark">
            <strong className="block mb-1">别名预览</strong>
            <code className="text-sm break-all block">{sourcePreview}</code>
          </div>

          <Field label="域名">
            <select
              className="input w-full"
              value={domainId}
              onChange={(e) => setDomainId(e.target.value)}
              disabled={!!alias}
            >
              {domains.length === 0 ? (
                <option value="">（请先在「域名」Tab 添加域名）</option>
              ) : domains.map((d) => (
                <option key={d.id} value={d.id}>{d.domain}</option>
              ))}
            </select>
          </Field>

          <Field label="模式">
            <div className="grid gap-2 text-sm">
              <label className="flex items-start gap-2 cursor-pointer">
                <input type="radio" checked={mode === "alias"} onChange={() => setMode("alias")} />
                <span>
                  <strong>Alias（别名转发）</strong>
                  <span className="muted text-xs block">发往 <code>source@domain</code> 的邮件自动转发到收件人列表。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input type="radio" checked={mode === "catchall"} onChange={() => setMode("catchall")} />
                <span>
                  <strong>Catch-all（兜底投递）</strong>
                  <span className="muted text-xs block">命中该域名下未被任何账户 / 别名匹配的邮件，转发到收件人。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input type="radio" checked={mode === "list"} onChange={() => setMode("list")} />
                <span>
                  <strong>邮件列表</strong>
                  <span className="muted text-xs block">对外展示为列表名称，回复会自动 Reply-To 列表地址。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input type="radio" checked={mode === "drop"} onChange={() => setMode("drop")} />
                <span>
                  <strong>Drop（丢弃）</strong>
                  <span className="muted text-xs block">静默丢弃匹配的邮件，不生成退信。常用于反垃圾。</span>
                </span>
              </label>
            </div>
          </Field>

          <Field label={mode === "catchall" ? "源 (catch-all 自动设置为 @domain)" : "源 (local-part)"} help={mode === "catchall" ? "Catch-all 不要求填写源字段。" : "仅需要 local-part，例如 postmaster、support、noreply。"}>
            <input
              className="input w-full"
              placeholder={mode === "catchall" ? "(自动为 @domain)" : "例如：postmaster"}
              value={source}
              onChange={(e) => setSource(e.target.value)}
              disabled={sourceDisabled}
            />
          </Field>

          {mode !== "drop" ? (
            <Field label="收件人" help="每行一个邮箱地址，或用英文逗号分隔。空行与空白会被忽略。">
              <textarea
                className="input w-full min-h-[120px] font-mono text-xs"
                placeholder={
                  mode === "catchall"
                    ? "admin@example.com\nmonitor@example.com"
                    : "alice@example.com, bob@example.com"
                }
                value={recipientsText}
                onChange={(e) => setRecipientsText(e.target.value)}
              />
            </Field>
          ) : null}

          {mode === "list" ? (
            <>
              <Field label="列表显示名">
                <input
                  className="input w-full"
                  placeholder="例如：产品组技术周报"
                  value={listName}
                  onChange={(e) => setListName(e.target.value)}
                />
              </Field>
              <Field label="列表 Reply-To（可选）" help="用户在列表邮件中点击「回复全部」时，默认回复到此地址。留空表示回复到 source。">
                <input
                  className="input w-full"
                  placeholder="tech-weekly@example.com"
                  value={listReplyTo}
                  onChange={(e) => setListReplyTo(e.target.value)}
                />
              </Field>
            </>
          ) : null}

          <Field label="描述（可选）">
            <input
              className="input w-full"
              placeholder="例如：合作伙伴对接 alias"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>

          <Toggle checked={enabled} onChange={setEnabled} label="启用该别名（关闭后该别名匹配但不生效，用于临时禁用）" />

          <div className="flex justify-end gap-2 pt-2 border-t">
            <Button tone="neutral" onClick={onClose} disabled={saving}>取消</Button>
            <Button tone="primary" onClick={() => void submit()} disabled={saving || domains.length === 0}>
              {saving ? "保存中…" : alias ? "保存修改" : "创建别名"}
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

function modeTone(m?: MailAlias["mode"]): "good" | "warn" | "danger" | "neutral" {
  switch (m) {
    case "alias": return "good";
    case "catchall": return "warn";
    case "list": return "neutral";
    case "drop": return "danger";
    default: return "neutral";
  }
}
function modeLabel(m?: MailAlias["mode"]): string {
  switch (m) {
    case "alias": return "Alias";
    case "catchall": return "Catch-all";
    case "list": return "邮件列表";
    case "drop": return "Drop";
    default: return m || "—";
  }
}

function parseRecipients(text: string): string[] {
  return Array.from(
    new Set(
      text
        .split(/[\n,]+/)
        .map((s) => s.trim())
        .filter((s) => s && s.includes("@"))
        .map((s) => s.replace(/^["'<]+|["'>]+$/g, "").trim())
        .filter(Boolean),
    ),
  );
}
