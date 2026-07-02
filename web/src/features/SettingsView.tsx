import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import type { AppActions } from "../app/App";
import type { AppData, ListenerEndpoint, LocalDatabaseFileStat, RuntimeSettings, TLSProbeResult } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, Field, Panel, Pill, SubTabs, Toggle } from "../components/ui";
import { defaultRuntime, formatDate } from "../domain/labels";
import { formatBytesIEC } from "../utils/format";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { SystemUpdatePanel } from "./settings/SystemUpdatePanel";
import { ObjectStoragePanel } from "./settings/ObjectStoragePanel";

type SettingsTab = "runtime" | "storage" | "updates";

const SETTINGS_TABS: Array<{ id: SettingsTab; label: string }> = [
  { id: "runtime", label: "运行与服务" },
  { id: "storage", label: "对象存储" },
  { id: "updates", label: "系统更新" },
];
const SETTINGS_TAB_IDS: SettingsTab[] = SETTINGS_TABS.map((item) => item.id);
const SETTINGS_CLEAR_KEYS = ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker"];
const DOWNGRADE_PHRASE = "I understand disabling HTTPS will revoke all sessions";

type ListenerRequestBody = {
  addr: string;
  tlsEnabled: boolean;
  tlsCertFile: string;
  tlsKeyFile: string;
  tlsOwnerUidCheck: boolean;
  hstsEnabled: boolean;
  hstsMaxAgeSeconds: number;
  confirm_downgrade?: boolean;
  confirm_phrase?: string;
  confirm_hsts?: boolean;
};

type ListenerApplyResponse = {
  addr: string;
  endpoint: ListenerEndpoint;
  runtime?: RuntimeSettings;
  downgradeRedirect?: string;
  splitStateWarning?: boolean;
  upgradeScheme?: "https";
};

function parseListenAddr(addr: string) {
  const idx = addr.lastIndexOf(":");
  if (idx <= 0 || idx === addr.length - 1) return null;
  const host = addr.slice(0, idx);
  const portText = addr.slice(idx + 1);
  if (!host.trim() || /\s/.test(host) || !/^\d{1,5}$/.test(portText)) return null;
  return { host, port: Number(portText) };
}

function apiErrorCode(error: unknown) {
  if (!error || typeof error !== "object" || !("code" in error)) return "";
  const code = (error as { code?: unknown }).code;
  return typeof code === "string" ? code : "";
}

export function SettingsView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab, tabHref] = useQueryParamState<SettingsTab>("settings", SETTINGS_TAB_IDS, "runtime", { clearKeys: SETTINGS_CLEAR_KEYS });
  const [runtime, setRuntime] = useState<RuntimeSettings>(data.settings.runtime || defaultRuntime());
  const [allowedRootsText, setAllowedRootsText] = useState((data.settings.runtime?.allowedRoots || []).join("\n"));
  const [busy, setBusy] = useState("");

  const currentRuntime: RuntimeSettings = (data.settings.runtime || defaultRuntime()) as RuntimeSettings;
  const currentEndpoint: ListenerEndpoint = (data.settings.listener ?? {}) as ListenerEndpoint;
  const prevEndpoint: ListenerEndpoint | null = data.settings.listener || null;
  const sqliteFile: LocalDatabaseFileStat = data.settings.storage?.sqlite || {
    kind: "sqlite",
    label: "SQLite 主库",
    path: data.settings.file?.dbPath,
    exists: Boolean(data.settings.file?.dbPath),
    sizeBytes: data.settings.file?.dbSizeBytes || 0,
  };
  const duckDBFiles = data.settings.storage?.duckdb || [];
  const embeddingMigration = data.settings.storage?.embeddingVectorMigration;
  const embeddingMigrationTotal = embeddingMigration?.totalVectors || 0;
  const embeddingMigrationMoved = embeddingMigration?.migratedVectors || 0;
  const embeddingMigrationRemaining = embeddingMigration?.remainingVectors || 0;
  const embeddingMigrationPercent = embeddingMigrationTotal > 0
    ? Math.min(100, Math.max(0, Math.round((embeddingMigrationMoved / embeddingMigrationTotal) * 100)))
    : embeddingMigration?.status === "completed"
      ? 100
      : 0;
  const [listenAddr, setListenAddr] = useState<string>(currentEndpoint.addr || currentRuntime.addr || data.settings.file?.addr || "");
  const [tlsEnabled, setTlsEnabled] = useState<boolean>(Boolean(currentEndpoint.tlsEnabled || currentRuntime.tlsEnabled));
  const [tlsCertFile, setTlsCertFile] = useState<string>(currentRuntime.tlsCertFile || "");
  const [tlsKeyFile, setTlsKeyFile] = useState<string>(currentRuntime.tlsKeyFile || "");
  const [tlsOwnerUidCheck, setTlsOwnerUidCheck] = useState<boolean>(currentRuntime.tlsOwnerUidCheck ?? true);
  const [hstsEnabled, setHstsEnabled] = useState<boolean>(Boolean(currentEndpoint.hstsEnabled || currentRuntime.hstsEnabled));
  const [hstsMaxAgeSeconds, setHstsMaxAgeSeconds] = useState<number>(currentEndpoint.hstsMaxAgeSeconds ?? currentRuntime.hstsMaxAgeSeconds ?? 15724800);
  const [probeBusy, setProbeBusy] = useState(false);
  const [applyBusy, setApplyBusy] = useState(false);
  const [probeResult, setProbeResult] = useState<TLSProbeResult | null>(null);
  const [eventRetentionDays, setEventRetentionDays] = useState<number>(data.settings.system?.eventRetentionDays ?? 30);
  const [systemSaving, setSystemSaving] = useState(false);

  useEffect(() => {
    if (data.settings.system?.eventRetentionDays !== undefined) {
      setEventRetentionDays(data.settings.system.eventRetentionDays);
    }
  }, [data.settings.system]);

  useEffect(() => {
    const next = data.settings.runtime || defaultRuntime();
    setRuntime(next);
    setAllowedRootsText((next.allowedRoots || []).join("\n"));
  }, [data.settings.runtime]);

  useEffect(() => {
    const nextEndpoint = (data.settings.listener ?? {}) as ListenerEndpoint;
    const nextRuntime = (data.settings.runtime || defaultRuntime()) as RuntimeSettings;
    setListenAddr(nextEndpoint.addr || nextRuntime.addr || data.settings.file?.addr || "");
    setTlsEnabled(Boolean(nextEndpoint.tlsEnabled || nextRuntime.tlsEnabled));
    setTlsCertFile(nextRuntime.tlsCertFile || "");
    setTlsKeyFile(nextRuntime.tlsKeyFile || "");
    setTlsOwnerUidCheck(nextRuntime.tlsOwnerUidCheck ?? true);
    setHstsEnabled(Boolean(nextEndpoint.hstsEnabled || nextRuntime.hstsEnabled));
    setHstsMaxAgeSeconds(nextEndpoint.hstsMaxAgeSeconds ?? nextRuntime.hstsMaxAgeSeconds ?? 15724800);
  }, [data.settings.listener, data.settings.runtime, data.settings.file?.addr]);

  const listenerDirty =
    listenAddr.trim() !== (currentRuntime.addr || currentEndpoint.addr || "").trim() ||
    tlsEnabled !== Boolean(currentRuntime.tlsEnabled || currentEndpoint.tlsEnabled) ||
    tlsCertFile.trim() !== (currentRuntime.tlsCertFile || "").trim() ||
    tlsKeyFile.trim() !== (currentRuntime.tlsKeyFile || "").trim() ||
    tlsOwnerUidCheck !== (currentRuntime.tlsOwnerUidCheck ?? true) ||
    hstsEnabled !== Boolean(currentRuntime.hstsEnabled || currentEndpoint.hstsEnabled) ||
    hstsMaxAgeSeconds !== (currentRuntime.hstsMaxAgeSeconds ?? currentEndpoint.hstsMaxAgeSeconds ?? 15724800);

  async function saveRuntime() {
    const payload: RuntimeSettings = {
      ...runtime,
      allowedRoots: allowedRootsText
        .split("\n")
        .map((item) => item.trim())
        .filter(Boolean),
    };
    setBusy("runtime");
    try {
      await actions.api("/api/settings", { method: "PUT", csrf: actions.csrf, body: payload });
      await actions.reloadData();
      actions.setToast("运行设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveSystemSettings() {
    const days = Math.max(0, Math.floor(Number(eventRetentionDays) || 0));
    setSystemSaving(true);
    try {
      await actions.api("/api/settings/system", { method: "PUT", csrf: actions.csrf, body: { eventRetentionDays: days } });
      await actions.reloadData();
      actions.setToast("系统设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSystemSaving(false);
    }
  }

  async function probeTLS() {
    if (!tlsCertFile.trim() || !tlsKeyFile.trim()) {
      actions.setToast("请先填写证书和私钥文件路径", "warn");
      return;
    }
    setProbeBusy(true);
    try {
      const res = await actions.api<TLSProbeResult>("/api/settings/tls-probe", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          certFile: tlsCertFile.trim(),
          keyFile: tlsKeyFile.trim(),
          ownerUidCheck: tlsOwnerUidCheck,
        },
      });
      setProbeResult(res);
      actions.setToast(res.ok ? `证书校验通过（剩余 ${res.daysRemaining ?? "?"} 天）` : res.error || "证书校验失败", res.ok ? "good" : "danger");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setProbeBusy(false);
    }
  }

  async function navigateToEndpoint(opts: { addr: string; tlsEnabled: boolean; forceScheme?: "https" | "http" }) {
    const parsed = parseListenAddr(opts.addr);
    if (!parsed) return;

    const current = new URL(window.location.href);
    const target = new URL(current.toString());
    if (parsed.host === "0.0.0.0" || parsed.host === "::" || parsed.host === "[::]") {
      // Wildcard binds keep the browser hostname and only switch port/scheme.
    } else {
      target.hostname = parsed.host.startsWith("[") ? parsed.host.slice(1, -1) : parsed.host;
    }
    target.port = String(parsed.port);
    target.protocol = (opts.forceScheme || (opts.tlsEnabled ? "https" : "http")) + ":";
    const targetStr = target.toString();

    let networkErr = false;
    for (let i = 0; i < 3; i += 1) {
      await new Promise<void>((resolve) => window.setTimeout(resolve, 350 + i * 250));
      try {
        const ctrl = new AbortController();
        const timeoutId = window.setTimeout(() => ctrl.abort(), 1500);
        await fetch(targetStr, { method: "HEAD", signal: ctrl.signal, credentials: "omit", mode: "no-cors" });
        window.clearTimeout(timeoutId);
        break;
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") {
          continue;
        }
        networkErr = true;
        break;
      }
    }

    const win = window.open(targetStr, "_blank", "noopener,noreferrer");
    if (!win) {
      actions.setToast("弹窗被拦截，请手动前往：" + targetStr, "warn");
    }
    if (networkErr && (opts.forceScheme === "https" || opts.tlsEnabled)) {
      actions.setToast("提示：HTTPS 已启用，若新标签页无法打开，请在浏览器中信任自签证书后刷新", "warn");
    }
  }

  async function applyListener() {
    const addr = listenAddr.trim();
    if (!addr) {
      actions.setToast("监听地址不能为空", "warn");
      return;
    }
    const parsed = parseListenAddr(addr);
    if (!parsed) {
      actions.setToast("地址格式应为 host:port（例如 127.0.0.1:8080、0.0.0.0:8443 或 [::]:8443）", "danger");
      return;
    }
    if (parsed.port < 1 || parsed.port > 65535) {
      actions.setToast("端口必须在 1-65535 之间", "danger");
      return;
    }
    if (tlsEnabled && (!tlsCertFile.trim() || !tlsKeyFile.trim())) {
      actions.setToast("启用 HTTPS 时证书和私钥路径均不能为空", "warn");
      return;
    }

    const wasHTTPS = Boolean(prevEndpoint?.tlsEnabled) || Boolean(data.settings.runtime?.tlsEnabled);
    const nowHTTPS = tlsEnabled;
    const hstsWasEnabled = Boolean(prevEndpoint?.hstsEnabled || data.settings.runtime?.hstsEnabled);

    const body: ListenerRequestBody = {
      addr,
      tlsEnabled: nowHTTPS,
      tlsCertFile: tlsCertFile.trim(),
      tlsKeyFile: tlsKeyFile.trim(),
      tlsOwnerUidCheck,
      hstsEnabled,
      hstsMaxAgeSeconds: Number(hstsMaxAgeSeconds) || 0,
    };

    if (wasHTTPS && !nowHTTPS) {
      const confirmedDowngrade = window.confirm(
        "注意：即将关闭 HTTPS。\n\n" +
          "为防止会话 cookie 在 HTTP 上泄露，所有已登录会话（包括你自己的）都将被立即撤销。\n\n" +
          "继续前需要你输入确认短语。"
      );
      if (!confirmedDowngrade) return;
      const phrase = window.prompt("请输入确认短语：", DOWNGRADE_PHRASE);
      if (phrase !== DOWNGRADE_PHRASE) {
        actions.setToast("确认短语不匹配，已取消", "warn");
        return;
      }
      body.confirm_downgrade = true;
      body.confirm_phrase = phrase;
    }

    if (hstsEnabled && !hstsWasEnabled) {
      const confirmedHsts = window.confirm(
        "注意：启用 HSTS 后，浏览器将在未来强制使用 HTTPS 访问此域名（即使手动输入 http://）。\n\n" +
          "如果后续关闭 HTTPS，浏览器可能会在 HSTS max-age 过期前拒绝通过 HTTP 访问。\n\n" +
          "确定启用？"
      );
      if (!confirmedHsts) return;
      body.confirm_hsts = true;
    }

    setApplyBusy(true);
    try {
      const resp = await actions.api<ListenerApplyResponse>("/api/settings/listener", {
        method: "POST",
        csrf: actions.csrf,
        body,
      });

      actions.setToast(resp.splitStateWarning ? "状态不同步，请刷新页面确认" : `已切换到 ${(resp.endpoint?.scheme || "http").toUpperCase()}://${resp.addr}`, resp.splitStateWarning ? "warn" : "good");

      const addrChanged = resp.addr !== (prevEndpoint?.addr || "");
      const schemeChanged = nowHTTPS !== wasHTTPS;
      if (addrChanged || schemeChanged) {
        await navigateToEndpoint({
          addr: resp.addr,
          tlsEnabled: nowHTTPS,
          forceScheme: resp.upgradeScheme,
        });
      }

      if (resp.downgradeRedirect) {
        window.setTimeout(() => {
          window.location.href = "/login";
        }, 1200);
      } else {
        await actions.reloadData();
      }
    } catch (error) {
      await actions.reloadData();
      const msg = friendlyError(error);
      const code = apiErrorCode(error);
      if (code === "confirm_required") {
        actions.setToast("请先完成二次确认：" + msg, "warn");
      } else if (code === "confirm_hsts_required") {
        actions.setToast("启用 HSTS 需要二次确认", "warn");
      } else {
        actions.setToast(msg, "danger");
      }
    } finally {
      setApplyBusy(false);
    }
  }

  const certSummaryItems: Array<[string, ReactNode]> = [];
  if (probeResult) {
    certSummaryItems.push(["主题", probeResult.subject || "-"]);
    certSummaryItems.push(["颁发者", probeResult.issuer || "-"]);
    certSummaryItems.push(["DNS 名称", (probeResult.dnsNames || prevEndpoint?.certDnsNames || []).join(", ") || "-"]);
    certSummaryItems.push(["生效", probeResult.notBefore || prevEndpoint?.certNotBefore || "-"]);
    certSummaryItems.push([
      "过期",
      <span
        style={
          typeof probeResult.daysRemaining === "number" && probeResult.daysRemaining < 30
            ? { color: "var(--danger, #c0392b)" }
            : undefined
        }
      >
        {probeResult.notAfter || prevEndpoint?.certNotAfter || "-"}
        {typeof probeResult.daysRemaining === "number" ? `（剩余 ${probeResult.daysRemaining} 天）` : ""}
      </span>,
    ]);
    certSummaryItems.push(["序列号", probeResult.serialNumber || "-"]);
    certSummaryItems.push(["文件所有者", probeResult.fileOwnerName || "-"]);
  } else if (prevEndpoint?.tlsEnabled) {
    certSummaryItems.push(["证书路径", prevEndpoint.certFile || "-"]);
    certSummaryItems.push(["DNS 名称", (prevEndpoint.certDnsNames || []).join(", ") || "-"]);
    certSummaryItems.push(["生效", prevEndpoint.certNotBefore || "-"]);
    certSummaryItems.push(["过期", prevEndpoint.certNotAfter || "-"]);
  }
  certSummaryItems.push(["最近证书重载错误", prevEndpoint?.certReloadErr || "无"]);

  return (
    <div className="grid gap-4 p-4">
      <SubTabs activeId={tab} onChange={(id) => setTab(id as SettingsTab)} tabs={SETTINGS_TABS.map((item) => ({ ...item, href: tabHref(item.id) }))} />

      {tab === "runtime" ? (
        <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(300px,0.95fr)] gap-4 max-xl:grid-cols-1">
          <div className="grid gap-4">
            <Panel title="运行设置" subtitle="影响允许工作目录和 Cookie 安全策略">
              <div className="grid gap-4">
                <Field label="允许根目录" help="每行一个绝对路径；后端会归一化并拒绝无效目录。">
                  <textarea
                    autoComplete="off"
                    className="textarea mono min-h-36"
                    name="allowed_roots"
                    onChange={(event) => setAllowedRootsText(event.target.value)}
                    spellCheck={false}
                    value={allowedRootsText}
                  />
                </Field>
                <Toggle
                  checked={Boolean(runtime.cookieSecure)}
                  label="HTTPS 部署时启用 Secure Cookie"
                  name="settings_cookie_secure"
                  onChange={(checked) => setRuntime((current) => ({ ...current, cookieSecure: checked }))}
                />
                <div className="flex flex-wrap justify-between gap-2">
                  <span className="muted text-xs">最后更新：{formatDate(runtime.updatedAt) || "-"}</span>
                  <Button disabled={busy === "runtime"} onClick={() => void saveRuntime()} tone="primary">
                    保存运行设置
                  </Button>
                </div>
              </div>
            </Panel>

            <Panel title="系统维护" subtitle="事件日志保留策略等全局配置">
              <div className="grid gap-4">
                <Field
                  label="事件日志保留期（天）"
                  help="系统事件表中超过保留期的记录将在后台自动清理。设为 0 表示永不清理（不推荐）。"
                >
                  <input
                    className="input mono"
                    inputMode="numeric"
                    min={0}
                    name="event_retention_days"
                    onChange={(event) => setEventRetentionDays(Number(event.target.value))}
                    type="number"
                    value={eventRetentionDays}
                  />
                </Field>
                <div className="flex flex-wrap justify-between gap-2">
                  <span className="muted text-xs">
                    {eventRetentionDays > 0
                      ? `保留最近 ${eventRetentionDays} 天的事件记录`
                      : "已禁用自动清理，事件数据会无限增长"}
                  </span>
                  <Button disabled={systemSaving} onClick={() => void saveSystemSettings()} tone="primary">
                    {systemSaving ? "保存中…" : "保存系统设置"}
                  </Button>
                </div>
              </div>
            </Panel>

            <Panel title="本地数据文件" subtitle="运行数据文件大小，独立于系统更新状态">
              <div className="grid gap-3">
                <StorageFileRow
                  description="配置、审计、任务记录和当前股票 V2 表"
                  file={sqliteFile}
                  title="SQLite 主库"
                />

                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-sm font-medium">DuckDB 数据文件</div>
                      <p className="muted mt-1 mb-0 text-xs">用于后续股票行情分析库，当前仅统计已存在的 .duckdb 文件。</p>
                    </div>
                    <Pill tone={duckDBFiles.length ? "good" : "neutral"}>
                      {duckDBFiles.length ? `${duckDBFiles.length} 个文件` : "未检测到"}
                    </Pill>
                  </div>
                  {duckDBFiles.length ? (
                    <div className="mt-3 grid gap-2">
                      {duckDBFiles.map((file) => (
                        <StorageFileRow
                          description={file.kind === "duckdb-wal" ? "DuckDB 写入日志文件" : "DuckDB 主数据文件"}
                          file={file}
                          key={`${file.kind || "duckdb"}:${file.path || file.label || ""}`}
                          title={file.label || "DuckDB"}
                        />
                      ))}
                    </div>
                  ) : (
                    <div className="mt-3 rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface)] p-3 text-xs leading-relaxed text-[var(--muted-strong)]">
                      <div>未检测到 DuckDB 文件。</div>
                      <div className="mt-1">当前股票 V2 数据仍在 SQLite 主库的 stockv2_* 表中。</div>
                    </div>
                  )}
                </div>

                {embeddingMigration ? (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="text-sm font-medium">Embedding 向量存储迁移</div>
                        <p className="muted mt-1 mb-0 text-xs">向量从长表搬迁到一行一个 vector_ref 的紧凑表，进度来自轻量元数据。</p>
                      </div>
                      <Pill tone={embeddingMigration.status === "completed" ? "good" : embeddingMigration.status === "failed" ? "danger" : "warn"}>
                        {embeddingMigration.status || "pending"}
                      </Pill>
                    </div>
                    <div className="mt-3 h-2 overflow-hidden rounded-full bg-[var(--surface)]">
                      <div className="h-full bg-[var(--accent)]" style={{ width: `${embeddingMigrationPercent}%` }} />
                    </div>
                    <div className="mt-2 grid gap-1 text-xs text-[var(--muted-strong)] sm:grid-cols-4">
                      <div>已搬迁 {embeddingMigrationMoved.toLocaleString()}</div>
                      <div>剩余 {embeddingMigrationRemaining.toLocaleString()}</div>
                      <div>总计 {embeddingMigrationTotal.toLocaleString()}</div>
                      <div>批次 {embeddingMigration?.batchSize || 0}</div>
                    </div>
                    {embeddingMigration.lastError ? (
                      <div className="mt-2 text-xs text-[var(--danger)]">{embeddingMigration.lastError}</div>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </Panel>
          </div>

          <Panel title="服务配置" subtitle="切换监听地址、启用 HTTPS 无需重启进程">
            <div className="grid gap-4">
              <div className="flex flex-wrap items-center gap-2">
                <Pill tone={prevEndpoint?.tlsEnabled ? "good" : "neutral"}>{(prevEndpoint?.scheme || "http").toUpperCase()}</Pill>
                <span className="mono text-sm text-[var(--muted)]">{prevEndpoint?.addr || listenAddr || "未配置"}</span>
                {prevEndpoint?.certReloadErr ? (
                  <Pill tone="danger">
                    <span title={prevEndpoint.certReloadErr}>cert err</span>
                  </Pill>
                ) : null}
                {prevEndpoint?.hstsEnabled ? <Pill tone="warn">HSTS</Pill> : null}
              </div>

              <Field label="监听地址" help="切换后旧连接在 2 秒内强制断开，新地址在进程重启后仍然生效。">
                <input
                  autoComplete="off"
                  className="input mono w-full"
                  name="settings_listener_addr"
                  onChange={(event) => setListenAddr(event.target.value)}
                  placeholder="host:port，例如 0.0.0.0:8443"
                  spellCheck={false}
                  value={listenAddr}
                />
              </Field>

              <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                <Toggle checked={tlsEnabled} label="启用 HTTPS (TLS)" name="settings_tls_enabled" onChange={setTlsEnabled} />
                {tlsEnabled ? (
                  <div className="grid gap-3 pl-6 max-sm:pl-0">
                    <Field label="证书文件路径 (fullchain.pem)" help="推荐使用绝对路径，建议放在 <data_dir>/tls/ 下。">
                      <input
                        autoComplete="off"
                        className="input mono w-full"
                        name="settings_tls_cert_file"
                        onChange={(event) => setTlsCertFile(event.target.value)}
                        placeholder="/var/lib/phantom-lancer/tls/fullchain.pem"
                        spellCheck={false}
                        value={tlsCertFile}
                      />
                    </Field>
                    <Field label="私钥文件路径 (privkey.pem)" help="文件权限必须为 600（不可组/其他可写）。">
                      <input
                        autoComplete="off"
                        className="input mono w-full"
                        name="settings_tls_key_file"
                        onChange={(event) => setTlsKeyFile(event.target.value)}
                        placeholder="/var/lib/phantom-lancer/tls/privkey.pem"
                        spellCheck={false}
                        value={tlsKeyFile}
                      />
                    </Field>
                    <Toggle
                      checked={tlsOwnerUidCheck}
                      label="验证私钥文件必须归当前进程用户所有（推荐开启）"
                      name="settings_tls_owner_uid_check"
                      onChange={setTlsOwnerUidCheck}
                    />
                    <div className="flex flex-wrap gap-2">
                      <Button disabled={probeBusy || !tlsCertFile.trim() || !tlsKeyFile.trim()} onClick={() => void probeTLS()} tone="neutral">
                        {probeBusy ? "校验中…" : "测试证书"}
                      </Button>
                    </div>
                    {probeResult || (prevEndpoint?.tlsEnabled && prevEndpoint.certDnsNames) ? <ContextList items={certSummaryItems} /> : null}
                  </div>
                ) : null}
              </div>

              <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                <Toggle checked={hstsEnabled} label="启用 HTTP Strict Transport Security (HSTS)" name="settings_hsts_enabled" onChange={setHstsEnabled} />
                {hstsEnabled ? (
                  <Field label="HSTS max-age（秒）" help="浏览器强制 HTTPS 的缓存时长。15724800 ≈ 182 天，推荐值。">
                    <div className="pl-6 max-sm:pl-0">
                      <input
                        className="input mono"
                        inputMode="numeric"
                        max={63072000}
                        min={0}
                        name="settings_hsts_max_age_seconds"
                        onChange={(event) => setHstsMaxAgeSeconds(Number(event.target.value) || 0)}
                        type="number"
                        value={hstsMaxAgeSeconds}
                      />
                    </div>
                  </Field>
                ) : null}
              </div>

              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="muted text-xs">
                  {data.settings.file?.tlsBootStrict ? "注意：PL_TLS_BOOT_STRICT 已启用，TLS 失败将导致启动失败" : ""}
                </span>
                <Button disabled={applyBusy || !listenerDirty || !listenAddr.trim()} onClick={() => void applyListener()} tone="primary">
                  {applyBusy ? "应用中…" : "应用端点配置"}
                </Button>
              </div>

              <ContextList
                items={[
                  ["配置文件", data.settings.file?.configPath || "-"],
                  ["数据目录", data.settings.file?.dataDir || "-"],
                ]}
              />
            </div>
          </Panel>
        </div>
      ) : null}

      {tab === "storage" ? <ObjectStoragePanel actions={actions} /> : null}
      {tab === "updates" ? <SystemUpdatePanel actions={actions} /> : null}
    </div>
  );
}

function StorageFileRow({ description, file, title }: { description: string; file: LocalDatabaseFileStat; title: string }) {
  const exists = Boolean(file.exists);
  const sizeText = exists ? formatBytesIEC(file.sizeBytes || 0) : "未检测到";
  return (
    <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium">{title}</div>
          <p className="muted mt-1 mb-0 text-xs">{description}</p>
        </div>
        <Pill tone={exists ? "good" : "warn"}>{sizeText}</Pill>
      </div>
      <div className="grid gap-1 text-[11px]">
        <span className="mono break-all text-[var(--muted-strong)]">{file.path || "-"}</span>
        {file.updatedAt ? <span className="muted">更新时间：{formatDate(file.updatedAt)}</span> : null}
      </div>
    </div>
  );
}
