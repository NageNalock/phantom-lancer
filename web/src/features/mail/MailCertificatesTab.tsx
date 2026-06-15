import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import {
  mailCertificateList,
  mailCertificateIssue,
  mailCertificateRenew,
  mailCertificateRollback,
  mailCertificateDelete,
  mailDNSProviderList,
  mailDNSProviderUpsert,
  mailDNSProviderDelete,
  mailDNSProviderTest,
  mailManualChallengeList,
  mailManualChallengeConfirm,
  mailManualChallengeCancel,
  mailResolveDrift,
  type MailCertificate,
  type MailCertificateIssueRequest,
  type MailDNSProvider,
  type MailDNSProviderUpsertRequest,
  type MailManualChallenge,
  type MailCertPipelineResult,
  type MailCertPipelineStep,
} from "../../api/client";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";

// DNS provider kinds the UI supports.  Manual is always a read-only row that
// appears as an option in the Issue modal dropdown and triggers the manual
// challenge flow.
const DNS_KIND_OPTIONS = [
  { value: "cloudflare", label: "Cloudflare API Token", fields: ["api_token", "zone_id"] },
  { value: "dnspod", label: "DNSPod / Tencent Cloud API", fields: ["secret_id", "secret_key", "zone_id"] },
  { value: "route53", label: "AWS Route53", fields: ["access_key_id", "secret_access_key", "hosted_zone_id"] },
  { value: "manual", label: "Manual (no token — operator creates TXT by hand)", fields: [] },
] as const;

type DNSKind = typeof DNS_KIND_OPTIONS[number]["value"];

// The 11 CertManager step names for the pipeline progress overlay.
const CERT_PIPELINE_STEP_NAMES = [
  "Validate inputs",
  "Load DNS provider",
  "Create ACME account",
  "Place DNS-01 orders",
  "Create TXT records",
  "Propagation wait",
  "ACME validation",
  "Sign CSR",
  "Persist PEM files",
  "Generate TLSA",
  "Cleanup TXT records",
];

const DEFAULT_ACME_DIRECTORIES = [
  { value: "https://acme-staging-v02.api.letsencrypt.org/directory", label: "Let's Encrypt Staging (推荐先测试)" },
  { value: "https://acme-v02.api.letsencrypt.org/directory", label: "Let's Encrypt Production" },
  { value: "__custom__", label: "自定义 ACME 目录 URL" },
];

export function MailCertificatesTab({
  actions,
  reload,
}: {
  actions: AppActions;
  reload: () => Promise<void>;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const [providers, setProviders] = useState<MailDNSProvider[]>([]);
  const [certs, setCerts] = useState<MailCertificate[]>([]);
  const [summary, setSummary] = useState<{ count: number; expiring: number; expired: number; active: number; manual: number }>({
    count: 0, expiring: 0, expired: 0, active: 0, manual: 0,
  });
  const [challenges, setChallenges] = useState<MailManualChallenge[]>([]);
  const [loading, setLoading] = useState(true);
  const [drifted, setDrifted] = useState(false);

  // modals
  const [providerOpen, setProviderOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<MailDNSProvider | null>(null);
  const [issueOpen, setIssueOpen] = useState(false);
  const [pipeline, setPipeline] = useState<MailCertPipelineResult | null>(null);
  const [expandedCertId, setExpandedCertId] = useState<string | null>(null);

  async function load() {
    try {
      const [pResp, cResp, chResp] = await Promise.all([
        mailDNSProviderList(),
        mailCertificateList(),
        mailManualChallengeList(),
      ]);
      setProviders(pResp.items || []);
      setCerts(cResp.items || []);
      setSummary({
        count: cResp.count ?? 0,
        expiring: cResp.expiring_count ?? 0,
        expired: cResp.expired_count ?? 0,
        active: cResp.active_count ?? 0,
        manual: cResp.manual_pending_count ?? 0,
      });
      setDrifted(!!cResp.drifted);
      setChallenges(chResp.items || []);
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

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}

      {/* Drift banner */}
      {drifted ? (
        <section className="panel" style={{ borderColor: "var(--danger)" }}>
          <div className="panel-header" style={{ backgroundColor: "var(--danger-soft)" }}>
            <div>
              <h2 className="m-0 text-sm font-semibold" style={{ color: "var(--danger)" }}>
                检测到配置漂移
              </h2>
              <p className="muted mt-1 mb-0 text-xs">
                磁盘配置与 Phantom 基线不一致，证书写操作（签发/续期/回滚/删除）将被阻止。
              </p>
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
        </section>
      ) : null}

      {/* Manual challenge alert banner */}
      {challenges.length > 0 ? (
        <Notice tone="warn">
          <div className="grid gap-2">
            <strong>有 {challenges.length} 个待处理的手动 DNS-01 挑战</strong>
            <p className="muted text-xs mb-0">
              请登录您的 DNS 管理面板，为每条记录创建 TXT 记录，完成后点击下方确认按钮。
            </p>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr>
                    <th className="text-left py-1 px-2">域名</th>
                    <th className="text-left py-1 px-2">FQDN （TXT 记录名）</th>
                    <th className="text-left py-1 px-2">值</th>
                    <th className="text-left py-1 px-2">过期</th>
                    <th className="py-1 px-2 text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {challenges.map((c) => (
                    <tr key={c.id}>
                      <td className="py-1 px-2 font-mono">{c.domain}</td>
                      <td className="py-1 px-2">
                        <code className="bg-neutral-1 dark:bg-neutral-0-dark rounded px-1 py-0.5 text-[11px] font-mono break-all">
                          {c.fqdn}
                        </code>
                        <button
                          type="button"
                          className="ml-1 text-xs text-neutral-11 hover:text-neutral-12"
                          onClick={() => void navigator.clipboard?.writeText(c.fqdn)}
                        >
                          复制
                        </button>
                      </td>
                      <td className="py-1 px-2">
                        <code className="bg-neutral-1 dark:bg-neutral-0-dark rounded px-1 py-0.5 text-[11px] font-mono break-all">
                          {c.value}
                        </code>
                        <button
                          type="button"
                          className="ml-1 text-xs text-neutral-11 hover:text-neutral-12"
                          onClick={() => void navigator.clipboard?.writeText(c.value)}
                        >
                          复制
                        </button>
                      </td>
                      <td className="py-1 px-2 text-xs muted">{c.expires_at}</td>
                      <td className="py-1 px-2 text-right">
                        <div className="inline-flex gap-1 justify-end">
                          <Button
                            tone="primary"
                            onClick={() => void confirmChallenge(c)}
                          >
                            我已创建记录
                          </Button>
                          <Button
                            tone="danger"
                            onClick={() => void cancelChallenge(c)}
                          >
                            取消
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </Notice>
      ) : null}

      {/* Top action row */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div>
          <strong>证书管理</strong>
          <div className="muted text-xs mt-1">
            ACME DNS-01 自动化签发。共 <strong>{summary.count}</strong> 张，
            <span style={{ color: "var(--good)" }}> 有效 {summary.active}</span>，
            <span style={{ color: "var(--warn)" }}> 临近到期 {summary.expiring}</span>，
            <span style={{ color: "var(--danger)" }}> 已过期 {summary.expired}</span>。
          </div>
        </div>
        <div className="flex gap-2 flex-wrap">
          <Button
            tone="primary"
            onClick={() => {
              setEditingProvider(null);
              setProviderOpen(true);
            }}
          >
            添加 DNS 提供商
          </Button>
          <Button
            tone="primary"
            onClick={() => setIssueOpen(true)}
          >
            签发证书
          </Button>
        </div>
      </div>

      {/* DNS Providers panel */}
      <DNSProvidersPanel
        loading={loading}
        providers={providers}
        onAdd={() => {
          setEditingProvider(null);
          setProviderOpen(true);
        }}
        onEdit={(p) => {
          setEditingProvider(p);
          setProviderOpen(true);
        }}
        onDelete={(p) => void removeProvider(p)}
        onTest={(p) => void testProvider(p)}
      />

      {/* Certificates panel */}
      <Panel title="已签发证书" subtitle="所有由 Phantom 管理的证书。临近到期（<30 天）的证书会由后台自动续期。">
        {loading ? (
          <EmptyState title="加载中…" body="" />
        ) : certs.length === 0 ? (
          <EmptyState
            title="尚未签发任何证书"
            body="点击右上角「签发证书」按钮，选择一个 DNS 提供商开始自动化签发。"
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm border-collapse">
              <thead>
                <tr>
                  <th className="text-left py-2 px-3 border-b">域名</th>
                  <th className="text-left py-2 px-3 border-b">SANs</th>
                  <th className="text-left py-2 px-3 border-b">到期</th>
                  <th className="text-left py-2 px-3 border-b">状态</th>
                  <th className="text-left py-2 px-3 border-b">提供商</th>
                  <th className="text-left py-2 px-3 border-b">TLSA (3 1 1)</th>
                  <th className="py-2 px-3 border-b text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {certs.map((c) => (
                  <>
                    <tr
                      key={c.id}
                      className={expandedCertId === c.id ? "bg-neutral-2 dark:bg-neutral-2-dark" : ""}
                    >
                      <td className="py-2 px-3 border-b">
                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            className="text-xs muted hover:text-neutral-12"
                            onClick={() => setExpandedCertId(expandedCertId === c.id ? null : c.id)}
                          >
                            {expandedCertId === c.id ? "▼" : "▶"}
                          </button>
                          <span className="font-medium font-mono">{c.domain}</span>
                        </div>
                      </td>
                      <td className="py-2 px-3 border-b">
                        {c.sans?.length ? (
                          <span className="muted text-xs">
                            +{c.sans.length} {c.sans.length === 1 ? "个 SAN" : "个 SAN"}
                          </span>
                        ) : (
                          <span className="muted text-xs">—</span>
                        )}
                      </td>
                      <td className="py-2 px-3 border-b">
                        <Pill tone={daysLeftTone(c.days_left)}>
                          {c.days_left} 天
                          <span className="muted ml-1 text-[10px]">· {formatDate(c.not_after)}</span>
                        </Pill>
                      </td>
                      <td className="py-2 px-3 border-b">
                        <Pill tone={statusTone(c.status)}>{statusLabel(c.status)}</Pill>
                      </td>
                      <td className="py-2 px-3 border-b text-xs">
                        {providerLabel(providers, c.dns_provider_id)}
                      </td>
                      <td className="py-2 px-3 border-b">
                        {c.tlsa_record ? (
                          <div className="flex items-center gap-1">
                            <code className="bg-neutral-1 dark:bg-neutral-0-dark rounded px-1 py-0.5 text-[11px] font-mono break-all max-w-[180px] truncate">
                              {extractTLSAValue(c.tlsa_record)}
                            </code>
                            <button
                              type="button"
                              className="text-xs muted hover:text-neutral-12"
                              title="复制完整 TLSA 记录"
                              onClick={() => void navigator.clipboard?.writeText(c.tlsa_record || "")}
                            >
                              复制
                            </button>
                          </div>
                        ) : (
                          <span className="muted text-xs">—</span>
                        )}
                      </td>
                      <td className="py-2 px-3 border-b text-right">
                        <div className="inline-flex gap-1 justify-end flex-wrap">
                          <Button
                            tone="neutral"
                            onClick={() => void renewCert(c)}
                            title="立即手动续期"
                          >
                            续期
                          </Button>
                          <Button
                            tone="neutral"
                            onClick={() => void rollbackCert(c)}
                            title="回滚到上一份备份"
                          >
                            回滚
                          </Button>
                          <Button
                            tone="neutral"
                            onClick={() => void deleteCert(c)}
                          >
                            删除
                          </Button>
                        </div>
                      </td>
                    </tr>
                    {expandedCertId === c.id ? (
                      <tr>
                        <td colSpan={7} className="py-3 px-3 border-b bg-neutral-2 dark:bg-neutral-2-dark">
                          <TLSAExpandRow cert={c} />
                        </td>
                      </tr>
                    ) : null}
                  </>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {/* Provider modal */}
      {providerOpen ? (
        <ProviderModal
          provider={editingProvider}
          onClose={() => setProviderOpen(false)}
          onSaved={() => {
            setProviderOpen(false);
            void load().then(() => reload());
          }}
          csrf={actions.csrf}
          setToast={actions.setToast}
        />
      ) : null}

      {/* Issue modal */}
      {issueOpen ? (
        <IssueModal
          providers={providers}
          onClose={() => setIssueOpen(false)}
          onDone={async (req) => {
            setIssueOpen(false);
            try {
              const result = await mailCertificateIssue(req, actions.csrf);
              setPipeline(result);
              actions.setToast(
                result.success ? `签发成功：${result.cert_id || ""}` : `签发失败：${result.summary}`,
                result.success ? "good" : "danger",
              );
              await load();
              await reload();
            } catch (e) {
              actions.setToast(friendlyError(e), "danger");
            }
          }}
          csrf={actions.csrf}
          setToast={actions.setToast}
        />
      ) : null}

      {/* Pipeline overlay */}
      {pipeline ? (
        <PipelineOverlay result={pipeline} onClose={() => setPipeline(null)} />
      ) : null}
    </div>
  );

  // ---- Certificate actions ----

  async function renewCert(c: MailCertificate) {
    try {
      const r = await mailCertificateRenew(c.id, false, actions.csrf);
      if (r.pipeline) setPipeline(r.pipeline);
      actions.setToast(
        r.renewed ? `续期成功：${c.domain}` : `续期失败：${r.message}`,
        r.renewed ? "good" : "danger",
      );
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function rollbackCert(c: MailCertificate) {
    const ok = await confirmDanger({
      title: `回滚证书 ${c.domain}`,
      body: "恢复到上一次签发之前的 PEM 备份。这是破坏性操作，需要重新确认。",
      confirmLabel: "确认回滚",
      confirmationText: c.domain,
      confirmationLabel: `请输入域名 ${c.domain} 以继续`,
    });
    if (!ok) return;
    try {
      const r = await mailCertificateRollback(c.id, actions.csrf);
      actions.setToast(r.restored ? `已回滚：${r.message}` : `回滚失败：${r.message}`, r.restored ? "good" : "warn");
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function deleteCert(c: MailCertificate) {
    const ok = await confirmDanger({
      title: `删除证书 ${c.domain}`,
      body: "删除证书后，Mox 将无法继续为该域名提供 TLS。引用该证书的域名需要重新签发。",
      confirmLabel: "确认删除",
      confirmationText: c.domain,
      confirmationLabel: `请输入域名 ${c.domain} 以继续`,
      impact: ["删除 PEM 文件", "移除 Mox 配置中的 TLS 引用"],
    });
    if (!ok) return;
    try {
      await mailCertificateDelete(c.id, actions.csrf);
      actions.setToast(`已删除 ${c.domain}`, "good");
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  // ---- Provider actions ----

  async function testProvider(p: MailDNSProvider) {
    try {
      const r = await mailDNSProviderTest(p.id, actions.csrf);
      actions.setToast(
        `${p.display_name || p.id}：${r.message}`,
        r.ok ? "good" : "danger",
      );
      await load();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function removeProvider(p: MailDNSProvider) {
    const ok = await confirmDanger({
      title: `删除 DNS 提供商 ${p.display_name || p.id}`,
      body: "删除后，使用该提供商的证书将无法自动续期。",
      confirmLabel: "确认删除",
      confirmationText: p.display_name || p.id,
      confirmationLabel: `请输入名称以继续`,
    });
    if (!ok) return;
    try {
      await mailDNSProviderDelete(p.id, actions.csrf);
      actions.setToast(`已删除 ${p.display_name || p.id}`, "good");
      await load();
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  // ---- Manual challenge actions ----

  async function confirmChallenge(c: MailManualChallenge) {
    try {
      const r = await mailManualChallengeConfirm(c.id, actions.csrf);
      actions.setToast(r.accepted ? "已确认 TXT 记录" : `确认失败：${r.message}`, r.accepted ? "good" : "warn");
      await load();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function cancelChallenge(c: MailManualChallenge) {
    try {
      const r = await mailManualChallengeCancel(c.id, actions.csrf);
      actions.setToast(r.accepted ? "已取消" : `取消失败：${r.message}`, r.accepted ? "good" : "warn");
      await load();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }
}

// ============================================================================
// Subcomponents
// ============================================================================

// --- DNSProvidersPanel ---

function DNSProvidersPanel({
  loading,
  providers,
  onAdd,
  onEdit,
  onDelete,
  onTest,
}: {
  loading: boolean;
  providers: MailDNSProvider[];
  onAdd: () => void;
  onEdit: (p: MailDNSProvider) => void;
  onDelete: (p: MailDNSProvider) => void;
  onTest: (p: MailDNSProvider) => void;
}) {
  return (
    <Panel
      title="DNS 提供商"
      subtitle="为 ACME DNS-01 挑战配置凭据。所有凭据在写入数据库前都会被 XOR 包装，避免 SQLite 明文泄露。"
      actions={
        <Button tone="neutral" onClick={onAdd}>
          + 添加
        </Button>
      }
    >
      {loading ? (
        <EmptyState title="加载中…" body="" />
      ) : providers.length === 0 ? (
        <EmptyState
          title="尚未配置 DNS 提供商"
          body="点击右上角「+ 添加」以添加 Cloudflare / DNSPod / Route53 凭据，或在签发证书时选择 Manual 模式手动创建 TXT 记录。"
        />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm border-collapse">
            <thead>
              <tr>
                <th className="text-left py-2 px-3 border-b">类型</th>
                <th className="text-left py-2 px-3 border-b">显示名称</th>
                <th className="text-left py-2 px-3 border-b">配置字段</th>
                <th className="text-left py-2 px-3 border-b">测试</th>
                <th className="text-left py-2 px-3 border-b">最后测试</th>
                <th className="py-2 px-3 border-b text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {providers.map((p) => {
                const isManual = p.kind === "manual";
                return (
                  <tr key={p.id}>
                    <td className="py-2 px-3 border-b">
                      <Pill tone={isManual ? "neutral" : "good"}>{kindLabel(p.kind)}</Pill>
                    </td>
                    <td className="py-2 px-3 border-b">
                      <span className="font-medium">{p.display_name}</span>
                      {isManual ? (
                        <span className="ml-2" style={{ fontSize: 10 }}><Pill tone="neutral">只读</Pill></span>
                      ) : null}
                    </td>
                    <td className="py-2 px-3 border-b">
                      {p.config_keys.length ? (
                        <div className="flex flex-wrap gap-1">
                          {p.config_keys.map((k) => (
                            <span
                              key={k}
                              className="text-[11px] rounded px-1.5 py-0.5 bg-neutral-2 dark:bg-neutral-2-dark font-mono"
                            >
                              {k}
                            </span>
                          ))}
                          {p.has_token ? <span className="ml-1" style={{ fontSize: 10 }}><Pill tone="good">已保存凭据</Pill></span> : null}
                        </div>
                      ) : (
                        <span className="muted text-xs">{isManual ? "—" : "未配置"}</span>
                      )}
                    </td>
                    <td className="py-2 px-3 border-b">
                      {isManual ? (
                        <span className="muted text-xs">n/a</span>
                      ) : p.tested ? (
                        <Pill tone={p.last_error ? "danger" : "good"}>
                          {p.last_error ? "失败" : "通过"}
                        </Pill>
                      ) : (
                        <Pill tone="neutral">未测试</Pill>
                      )}
                    </td>
                    <td className="py-2 px-3 border-b muted text-xs">
                      {p.last_tested_at ? formatDate(p.last_tested_at) : "—"}
                    </td>
                    <td className="py-2 px-3 border-b text-right">
                      <div className="inline-flex gap-1 justify-end flex-wrap">
                        {!isManual ? (
                          <Button tone="neutral" onClick={() => onTest(p)}>
                            测试
                          </Button>
                        ) : null}
                        {!isManual ? (
                          <Button tone="neutral" onClick={() => onEdit(p)}>
                            编辑
                          </Button>
                        ) : null}
                        {!isManual ? (
                          <Button tone="neutral" onClick={() => onDelete(p)}>
                            删除
                          </Button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

// --- ProviderModal ---

function ProviderModal({
  provider,
  onClose,
  onSaved,
  csrf,
  setToast,
}: {
  provider: MailDNSProvider | null;
  onClose: () => void;
  onSaved: () => void;
  csrf?: string;
  setToast: (m: string, tone: "good" | "warn" | "danger" | "neutral") => void;
}) {
  const initialKind = (provider?.kind || "cloudflare") as DNSKind;
  const [kind, setKind] = useState<DNSKind>(initialKind);
  const [displayName, setDisplayName] = useState(provider?.display_name || "");
  const [config, setConfig] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  const kindOpt = DNS_KIND_OPTIONS.find((o) => o.value === kind);
  const fields = kindOpt?.fields || [];

  async function save() {
    if (kind !== "manual" && !displayName.trim()) {
      setToast("显示名称不能为空", "warn");
      return;
    }
    setSaving(true);
    try {
      const req: MailDNSProviderUpsertRequest = {
        id: provider?.id || undefined,
        kind,
        display_name: displayName.trim(),
        config,
      };
      await mailDNSProviderUpsert(req, csrf);
      setToast(provider ? "提供商已更新" : "提供商已添加", "good");
      onSaved();
    } catch (e) {
      setToast(friendlyError(e), "danger");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel max-w-lg w-[92%] max-h-[92vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <h3 className="m-0">{provider ? "编辑 DNS 提供商" : "添加 DNS 提供商"}</h3>
          <button
            type="button"
            className="text-lg muted hover:text-neutral-12"
            onClick={onClose}
            aria-label="关闭"
          >关闭</button>
        </div>
        <div className="panel-body grid gap-4">
          <Field label="提供商类型（必填）">
            <select
              className="input w-full"
              value={kind}
              onChange={(e) => setKind(e.target.value as DNSKind)}
              disabled={!!provider}
            >
              {DNS_KIND_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            {provider ? <p className="muted text-xs mt-1 mb-0">已保存的提供商无法更改类型。</p> : null}
          </Field>

          {kind !== "manual" ? (
            <Field label="显示名称（必填）">
              <input
                className="input w-full"
                placeholder="例如：公司 Cloudflare 主账号"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            </Field>
          ) : null}

          {fields.length > 0 ? (
            <div className="rounded-lg border p-3 text-sm bg-neutral-2 dark:bg-neutral-2-dark">
              <strong className="block mb-2">{kindOpt?.label} 凭据</strong>
              <div className="grid gap-3">
                {fields.map((f) => (
                  <Field key={f} label={`${snakeToLabel(f)}${provider ? "" : "（必填）"}`}>
                    <input
                      type={f.includes("secret") || f.includes("token") || f.includes("key") ? "password" : "text"}
                      className="input w-full"
                      placeholder={provider ? "(留空以保留旧值)" : `输入 ${f}`}
                      value={config[f] || ""}
                      onChange={(e) => setConfig({ ...config, [f]: e.target.value })}
                    />
                  </Field>
                ))}
              </div>
            </div>
          ) : null}

          {kind === "manual" ? (
            <div className="rounded-lg border p-3 text-sm bg-neutral-2 dark:bg-neutral-2-dark">
              <strong className="block mb-2">Manual 模式说明</strong>
              <p className="text-xs mb-0">
                Manual 模式下，Phantom 在签发/续期时不会自动调用 DNS API，
                而是在证书管理页顶部弹出「手动 TXT 创建」横幅。
                您需要手动到 DNS 提供商后台创建 <code>_acme-challenge.&lt;domain&gt;</code> 的 TXT 记录后，点击确认。
              </p>
            </div>
          ) : null}

          <div className="flex justify-end gap-2 pt-2">
            <Button tone="neutral" onClick={onClose} disabled={saving}>取消</Button>
            <Button tone="primary" onClick={() => void save()} disabled={saving}>
              {saving ? "保存中…" : provider ? "保存修改" : "添加"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

// --- IssueModal ---

function IssueModal({
  providers,
  onClose,
  onDone,
  csrf,
  setToast,
}: {
  providers: MailDNSProvider[];
  onClose: () => void;
  onDone: (req: MailCertificateIssueRequest) => Promise<void>;
  csrf?: string;
  setToast: (m: string, tone: "good" | "warn" | "danger" | "neutral") => void;
}) {
  const [domain, setDomain] = useState("");
  const [sansText, setSansText] = useState("");
  const [dnsProviderId, setDnsProviderId] = useState("__manual__");
  const [acmeUrl, setAcmeUrl] = useState(DEFAULT_ACME_DIRECTORIES[0].value);
  const [customAcmeUrl, setCustomAcmeUrl] = useState("");
  const [acceptTOS, setAcceptTOS] = useState(false);
  const [contactEmail, setContactEmail] = useState("");
  const [tlsaEnabled, setTlsaEnabled] = useState(true);
  const [mxHost, setMxHost] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    const d = domain.trim();
    if (!d) {
      setToast("主域名不能为空", "warn");
      return;
    }
    if (!contactEmail.trim() || !contactEmail.includes("@")) {
      setToast("请填写有效的 ACME 联系邮箱", "warn");
      return;
    }
    if (!acceptTOS) {
      setToast("请先同意 ACME 服务条款", "warn");
      return;
    }
    const finalAcmeUrl =
      acmeUrl === "__custom__" ? customAcmeUrl.trim() : acmeUrl;
    if (acmeUrl === "__custom__" && !finalAcmeUrl.startsWith("https://")) {
      setToast("自定义 ACME URL 必须以 https:// 开头", "warn");
      return;
    }
    const sans = sansText
      .split(/[,\s]+/)
      .map((s) => s.trim())
      .filter((s) => s && s !== d);

    const req: MailCertificateIssueRequest = {
      domain: d,
      sans,
      dns_provider_id: dnsProviderId === "__manual__" ? "" : dnsProviderId,
      accept_tos: acceptTOS,
      contact_email: contactEmail.trim(),
      acme_directory_url: finalAcmeUrl,
      tlsa_enabled: tlsaEnabled,
      mx_host: mxHost.trim() || undefined,
    };
    setSubmitting(true);
    try {
      await onDone(req);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel max-w-xl w-[94%] max-h-[94vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <h3 className="m-0">签发新证书</h3>
          <button type="button" className="text-lg muted hover:text-neutral-12" onClick={onClose} aria-label="关闭">关闭</button>
        </div>
        <div className="panel-body grid gap-4">
          <Field label="主域名 (CN)（必填）">
            <input
              className="input w-full"
              placeholder="mail.example.com"
              value={domain}
              onChange={(e) => {
                setDomain(e.target.value);
                if (!mxHost) setMxHost(e.target.value);
              }}
            />
          </Field>

          <Field label="SANs （可选，用逗号或空格分隔）" help="例如：*.example.com, smtp.example.com, imap.example.com">
            <input
              className="input w-full"
              placeholder="*.example.com, smtp.example.com"
              value={sansText}
              onChange={(e) => setSansText(e.target.value)}
            />
          </Field>

          <Field label="DNS 提供商（必填）" help="选择管理该域名的 DNS 服务商，或 Manual 手动模式。">
            <select
              className="input w-full"
              value={dnsProviderId}
              onChange={(e) => setDnsProviderId(e.target.value)}
            >
              <option value="__manual__">Manual (无 Token，手动创建 TXT)</option>
              {providers
                .filter((p) => p.kind !== "manual")
                .map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.display_name} · {kindLabel(p.kind)}
                  </option>
                ))}
            </select>
          </Field>

          <Field label="ACME 目录 URL（必填）">
            <select
              className="input w-full"
              value={acmeUrl}
              onChange={(e) => setAcmeUrl(e.target.value)}
            >
              {DEFAULT_ACME_DIRECTORIES.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            {acmeUrl === "__custom__" ? (
              <input
                className="input w-full mt-2"
                placeholder="https://acme.example.com/directory"
                value={customAcmeUrl}
                onChange={(e) => setCustomAcmeUrl(e.target.value)}
              />
            ) : null}
          </Field>

          <Field label="ACME 联系邮箱（必填）" help="用于接收过期与续期提醒，也会写入 ACME 账户信息。">
            <input
              type="email"
              className="input w-full"
              placeholder="postmaster@example.com"
              value={contactEmail}
              onChange={(e) => setContactEmail(e.target.value)}
            />
          </Field>

          <Field label="MX 主机名 (TLSA 用)" help="生成 TLSA 记录时使用的主机名前缀。默认等于主域名。">
            <input
              className="input w-full"
              placeholder="mail.example.com"
              value={mxHost}
              onChange={(e) => setMxHost(e.target.value)}
            />
          </Field>

          <label className="flex items-start gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={tlsaEnabled}
              onChange={(e) => setTlsaEnabled(e.target.checked)}
              className="mt-1"
            />
            <span>
              签发后自动生成并显示 TLSA (3 1 1) 记录
              <span className="muted text-xs block">3 1 1 = DANE-EE，证书公钥的 SHA-256 哈希。用于 SMTP TLS 提升安全性。</span>
            </span>
          </label>

          <label className="flex items-start gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={acceptTOS}
              onChange={(e) => setAcceptTOS(e.target.checked)}
              className="mt-1"
            />
            <span>
              <strong>我已阅读并同意所选 ACME 服务器的服务条款（TOS）</strong>
              <span className="muted text-xs block">
                Let's Encrypt TOS：https://letsencrypt.org/repository/ — 其他提供商请自行查阅。
              </span>
            </span>
          </label>

          <div className="flex justify-end gap-2 pt-2 border-t">
            <Button tone="neutral" onClick={onClose} disabled={submitting}>取消</Button>
            <Button tone="primary" onClick={() => void submit()} disabled={submitting}>
              {submitting ? "签发中…（请稍候）" : "签发证书"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

// --- PipelineOverlay ---

function PipelineOverlay({
  result,
  onClose,
}: {
  result: MailCertPipelineResult;
  onClose: () => void;
}) {
  const steps: MailCertPipelineStep[] =
    result.steps && result.steps.length > 0
      ? result.steps
      : CERT_PIPELINE_STEP_NAMES.map((name, i) => ({
          step: i + 1,
          total: CERT_PIPELINE_STEP_NAMES.length,
          name,
          percent: result.success ? 100 : i < (result.failure_step || 0) ? 100 : 0,
          message: result.success ? "ok" : i + 1 === (result.failure_step || 0) ? (result.summary || "failed") : "",
          state:
            i + 1 === (result.failure_step || 0)
              ? "failed"
              : result.success
              ? "done"
              : i < (result.failure_step || 0)
              ? "done"
              : "running",
        }));

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/40 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        className="panel max-w-2xl w-[94%] max-h-[92vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="panel-header flex items-center justify-between">
          <div>
            <h3 className="m-0">
              {result.success ? "签发完成" : result.rolled_back ? "签发失败（已回滚）" : "签发失败"}
            </h3>
            <p className="muted text-xs mt-1 mb-0">
              {result.summary}
              {result.cert_id ? (
                <span className="ml-2 font-mono">cert_id: {result.cert_id}</span>
              ) : null}
            </p>
          </div>
          <button type="button" className="text-lg muted hover:text-neutral-12" onClick={onClose} aria-label="关闭">关闭</button>
        </div>
        <div className="panel-body">
          <StepProgress steps={steps} />
          {result.rolled_back && result.rollback_err ? (
            <Notice tone="danger">
              <strong className="block mb-1">回滚错误</strong>
              <code className="text-xs">{result.rollback_err}</code>
            </Notice>
          ) : null}
          <div className="flex justify-end pt-2">
            <Button tone="primary" onClick={onClose}>关闭</Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function StepProgress({ steps }: { steps: MailCertPipelineStep[] }) {
  return (
    <ol className="grid gap-2">
      {steps.map((s) => {
        const barColor =
          s.state === "failed"
            ? "var(--danger)"
            : s.state === "rollback"
            ? "var(--warn)"
            : s.state === "done"
            ? "var(--good)"
            : "var(--primary)";
        return (
          <li key={s.step} className="grid grid-cols-[32px_1fr_auto] gap-2 items-start">
            <div
              className="grid place-items-center rounded-full text-xs font-bold w-6 h-6"
              style={{
                backgroundColor: s.state === "failed" ? "var(--danger-soft)" : s.state === "done" ? "var(--good-soft)" : "var(--primary-soft)",
                color: s.state === "failed" ? "var(--danger)" : s.state === "done" ? "var(--good)" : "var(--primary)",
              }}
            >
              {s.state === "failed" ? "失败" : s.state === "done" ? "完成" : s.step}
            </div>
            <div>
              <div className="flex items-baseline justify-between gap-2">
                <strong className="text-sm">{s.name}</strong>
                <span className="text-[11px] muted">
                  Step {s.step}/{s.total}
                </span>
              </div>
              <div className="h-1.5 rounded-full bg-neutral-3 dark:bg-neutral-3-dark mt-1 overflow-hidden">
                <div
                  className="h-full transition-all duration-300"
                  style={{ width: `${s.percent}%`, backgroundColor: barColor }}
                />
              </div>
              {s.message ? <p className="text-xs muted mt-1 mb-0">{s.message}</p> : null}
              {s.output ? (
                <pre className="text-[10px] mt-1 mb-0 p-2 rounded bg-neutral-2 dark:bg-neutral-2-dark whitespace-pre-wrap font-mono">
                  {s.output}
                </pre>
              ) : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

// --- TLSA expand row ---

function TLSAExpandRow({ cert }: { cert: MailCertificate }) {
  const full = cert.tlsa_record || "";
  const hash = extractTLSAValue(full);
  return (
    <div className="grid gap-3">
      <div className="flex items-baseline justify-between gap-2 flex-wrap">
        <div>
          <strong className="text-sm">TLSA 记录 (DANE)</strong>
          <span className="muted text-xs ml-2">
            用法 3 (DANE-EE) · 选择器 1 (SPKI) · 匹配类型 1 (SHA-256)
          </span>
        </div>
        <button
          type="button"
          className="text-xs muted hover:text-neutral-12"
          onClick={() => void navigator.clipboard?.writeText(full)}
        >
          复制完整记录
        </button>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-xs border-collapse">
          <tbody>
            <tr>
              <td className="py-1 px-2 w-28 muted">记录名</td>
              <td className="py-1 px-2 font-mono break-all">
                _25._tcp.{cert.domain}.
              </td>
            </tr>
            <tr>
              <td className="py-1 px-2 muted">TTL</td>
              <td className="py-1 px-2 font-mono">300 (5 分钟)</td>
            </tr>
            <tr>
              <td className="py-1 px-2 muted">Type</td>
              <td className="py-1 px-2 font-mono">TLSA</td>
            </tr>
            <tr>
              <td className="py-1 px-2 muted">Usage · Selector · Matching</td>
              <td className="py-1 px-2 font-mono">
                <Pill tone="good">3 1 1</Pill>
                <span className="muted ml-2 text-[11px]">
                  3=DANE-EE（证书本身作为信任锚，无需 CA）· 1=取 SubjectPublicKeyInfo · 1=SHA-256
                </span>
              </td>
            </tr>
            <tr>
              <td className="py-1 px-2 muted">证书数据 (64 hex)</td>
              <td className="py-1 px-2">
                <div className="flex items-center gap-1">
                  <code className="bg-neutral-1 dark:bg-neutral-0-dark rounded px-2 py-1 text-[11px] font-mono break-all">
                    {hash || "(无)"}
                  </code>
                  <button
                    type="button"
                    className="text-xs muted hover:text-neutral-12"
                    onClick={() => void navigator.clipboard?.writeText(hash || "")}
                  >
                    复制
                  </button>
                </div>
              </td>
            </tr>
            <tr>
              <td className="py-1 px-2 muted">完整 BIND 格式</td>
              <td className="py-1 px-2">
                <code className="bg-neutral-1 dark:bg-neutral-0-dark rounded px-2 py-1 text-[11px] font-mono break-all block w-full">
                  {full || "(无)"}
                </code>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div className="rounded-lg border p-3 text-sm bg-neutral-2 dark:bg-neutral-2-dark">
        <strong className="block mb-2">如何验证 TLSA 生效？</strong>
        <div className="text-xs space-y-1 mb-0">
          <div>
            1. 创建记录后使用 <code>dig TLSA _25._tcp.{cert.domain} +short</code> 查询。
          </div>
          <div>
            2. 使用 <a className="link" href="https://www.huque.com/bin/danecheck" target="_blank" rel="noreferrer">DANE Checker</a>{" "}
            或 <a className="link" href="https://dane.sys4.de/" target="_blank" rel="noreferrer">sys4 DANE</a> 等在线工具验证。
          </div>
          <div>
            3. 证书续期后 <strong>旧的 TLSA 仍然有效 24 小时</strong>（旧证书公钥未过期），请先把新 TLSA 加入，等待 1× TTL，再删除旧记录。
          </div>
        </div>
      </div>
    </div>
  );
}

// ============================================================================
// Helpers
// ============================================================================

function daysLeftTone(d: number): "good" | "warn" | "danger" | "neutral" {
  if (d <= 0) return "danger";
  if (d < 7) return "danger";
  if (d < 30) return "warn";
  return "good";
}

function statusTone(s: MailCertificate["status"]): "good" | "warn" | "danger" | "neutral" {
  switch (s) {
    case "active": return "good";
    case "renewing": return "warn";
    case "expiring_soon": return "warn";
    case "expired": return "danger";
    case "error": return "danger";
    case "manual_pending": return "warn";
    default: return "neutral";
  }
}

function statusLabel(s: MailCertificate["status"]): string {
  switch (s) {
    case "active": return "有效";
    case "renewing": return "续期中";
    case "expiring_soon": return "临近到期";
    case "expired": return "已过期";
    case "error": return "错误";
    case "manual_pending": return "等待手动 TXT";
    default: return s;
  }
}

function kindLabel(k: string): string {
  return DNS_KIND_OPTIONS.find((o) => o.value === k)?.label || k;
}

function providerLabel(providers: MailDNSProvider[], id?: string): string {
  if (!id) return "Manual";
  const p = providers.find((x) => x.id === id);
  if (!p) return "—";
  return `${p.display_name} · ${kindLabel(p.kind)}`;
}

function formatDate(s?: string): string {
  if (!s) return "";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function snakeToLabel(s: string): string {
  return s
    .split("_")
    .map((w) => w[0]?.toUpperCase() + w.slice(1))
    .join(" ");
}

// Extract the SHA-256 hex value from a full TLSA record like:
//   "_25._tcp.mail.x. 300 IN TLSA 3 1 1 <hex>"
// returns the trailing hex portion.
function extractTLSAValue(full: string): string {
  if (!full) return "";
  const parts = full.trim().split(/\s+/);
  const last = parts[parts.length - 1] || "";
  if (/^[0-9a-fA-F]+$/.test(last)) return last;
  return full;
}
