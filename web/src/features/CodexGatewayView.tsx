import { useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexGatewayAPIKey, CodexGatewayAccount, CodexGatewayRequestLog, CodexGatewaySettings, Tone } from "../app/types";
import { friendlyError, readCookie } from "../api/client";
import { Button, CollapsibleSection, ContextList, EmptyState, Field, Metric, Notice, Panel, Pill, SubTabs, Toggle, useDangerConfirm } from "../components/ui";
import { codexGatewayAccountStatusLabel, codexGatewayStatusLabel, defaultCodexGatewaySettings, formatDate } from "../domain/labels";
import { shouldHandleQueryLinkClick, useQueryParamState } from "../hooks/useQueryParamState";

type GatewayAccountDraft = {
  label: string;
  status: string;
  accessToken: string;
  refreshToken: string;
  expiresAt: string;
  plan: string;
};

type GatewayTab = "overview" | "accounts" | "keys" | "models" | "logs" | "settings";

const GATEWAY_TABS: Array<{ id: GatewayTab; label: string }> = [
  { id: "overview", label: "概览" },
  { id: "accounts", label: "账号" },
  { id: "keys", label: "API Keys" },
  { id: "models", label: "模型" },
  { id: "logs", label: "请求日志" },
  { id: "settings", label: "设置" },
];
const GATEWAY_TAB_IDS: GatewayTab[] = GATEWAY_TABS.map((item) => item.id);
const GATEWAY_CLEAR_KEYS = ["codex", "codexInbox", "codexRuntime", "images", "docker", "settings"];

const emptyAccountDraft: GatewayAccountDraft = {
  label: "",
  status: "active",
  accessToken: "",
  refreshToken: "",
  expiresAt: "",
  plan: "",
};

export function CodexGatewayView({ actions, data }: { actions: AppActions; data: AppData }) {
  const gateway = data.codexGateway;
  const status = gateway.status || data.dashboard.codexGateway;
  const settings = useMemo(() => ({ ...defaultCodexGatewaySettings(), ...(gateway.settings || {}) }), [gateway.settings]);
  const [draft, setDraft] = useState(settings);
  const [apiKeyName, setAPIKeyName] = useState("");
  const [oneTimeToken, setOneTimeToken] = useState("");
  const [accountDraft, setAccountDraft] = useState<GatewayAccountDraft>(emptyAccountDraft);
  const [busy, setBusy] = useState("");
  const [tab, setTab, tabHref] = useQueryParamState<GatewayTab>("gateway", GATEWAY_TAB_IDS, "overview", { clearKeys: GATEWAY_CLEAR_KEYS });
  const [oauthStarted, setOauthStarted] = useState(false);
  const [oauthRedirectUri, setOauthRedirectUri] = useState("");
  const [oauthCallbackUrl, setOauthCallbackUrl] = useState("");
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const activeAccounts = gateway.accounts?.filter((account) => account.status === "active").length || 0;
  const activeKeys = gateway.apiKeys?.filter((key) => key.status === "active").length || 0;
  const gatewayReady = Boolean(status?.enabled && activeAccounts && activeKeys);
  const overviewActions = [
    !status?.enabled
      ? { label: "Gateway 未启用", body: "启用 OpenAI-compatible /v1 转发入口。", action: "打开设置", tab: "settings" as GatewayTab }
      : null,
    !activeAccounts
      ? { label: "缺少上游账号", body: "导入 OAuth 账号或 token 摘要后才能转发请求。", action: "打开账号", tab: "accounts" as GatewayTab }
      : null,
    !activeKeys
      ? { label: "缺少公开 API Key", body: "为外部 OpenAI SDK 客户端创建访问 key。", action: "打开 API Keys", tab: "keys" as GatewayTab }
      : null,
    status?.recentFailureCount
      ? { label: "最近请求失败", body: "查看错误码、延迟和上游响应摘要。", action: "打开日志", tab: "logs" as GatewayTab }
      : null,
  ].filter(Boolean) as Array<{ label: string; body: string; action: string; tab: GatewayTab }>;

  useEffect(() => {
    setDraft(settings);
  }, [settings]);

  useEffect(() => {
    function handleMessage(event: MessageEvent) {
      const payload = event.data as { type?: string; error?: string };
      if (payload?.type === "codex-gateway-oauth-success") {
        void actions.refreshCodexGateway();
        actions.setToast("OAuth 账号已导入", "good");
      } else if (payload?.type === "codex-gateway-oauth-error") {
        actions.setToast(payload.error || "OAuth 登录失败", "danger");
      }
    }
    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [actions]);

  function updateSetting(key: keyof CodexGatewaySettings, value: string | number | boolean) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  async function saveSettings() {
    setBusy("settings");
    try {
      const normalized = normalizeGatewaySettings(draft);
      const patch = changedGatewaySettings(normalized, settings);
      if (Object.keys(patch).length === 0) {
        actions.setToast("没有需要保存的修改", "warn");
        return;
      }
      await actions.api("/api/codex-gateway/settings", {
        method: "PUT",
        csrf: actions.csrf,
        body: patch,
      });
      await actions.refreshCodexGateway();
      actions.setToast("Gateway 设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function createAPIKey() {
    setBusy("api-key");
    try {
      const result = await actions.api<{ key?: CodexGatewayAPIKey; token?: string }>("/api/codex-gateway/api-keys", {
        method: "POST",
        csrf: actions.csrf,
        body: { name: apiKeyName.trim() || "OpenAI client" },
      });
      setAPIKeyName("");
      setOneTimeToken(result.token || "");
      await actions.refreshCodexGateway();
      actions.setToast("API key 已创建", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function rotateAPIKey(key: CodexGatewayAPIKey) {
    const confirmed = await confirmDanger({
      title: "轮换 Gateway API key",
      objectName: key.name || key.id,
      body: "该操作会立即生成新的 token，旧 token 将不再适合继续分发给客户端。",
      confirmLabel: "轮换 key",
      impact: ["新 token 只显示一次。", "依赖旧 token 的外部客户端需要更新配置。"],
      recovery: "如果客户端还未切换，请在轮换前准备好更新窗口。",
    });
    if (!confirmed) return;
    setBusy(`key-rotate:${key.id}`);
    try {
      const result = await actions.api<{ key?: CodexGatewayAPIKey; token?: string }>(`/api/codex-gateway/api-keys/${encodeURIComponent(key.id)}/rotate`, {
        method: "POST",
        csrf: actions.csrf,
      });
      setOneTimeToken(result.token || "");
      await actions.refreshCodexGateway();
      actions.setToast("API key 已轮换", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function updateAPIKeyStatus(key: CodexGatewayAPIKey, status: "active" | "disabled") {
    setBusy(`key-status:${key.id}`);
    try {
      await actions.api(`/api/codex-gateway/api-keys/${encodeURIComponent(key.id)}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: { status },
      });
      await actions.refreshCodexGateway();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function deleteAPIKey(key: CodexGatewayAPIKey) {
    const confirmed = await confirmDanger({
      title: "删除 Gateway API key",
      objectName: key.name || key.id,
      body: "该操作会删除公开 /v1 端点使用的 API key。",
      confirmLabel: "删除 key",
      impact: ["外部客户端会立即失去访问权限。", "请求日志和审计摘要会保留。"],
      recovery: "删除不可恢复；需要访问时只能重新创建并分发新 token。",
    });
    if (!confirmed) return;
    setBusy(`key-delete:${key.id}`);
    try {
      await actions.api(`/api/codex-gateway/api-keys/${encodeURIComponent(key.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await actions.refreshCodexGateway();
      actions.setToast("API key 已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function startOAuth() {
    setBusy("oauth");
    try {
      const result = await actions.api<{ authUrl?: string; redirectUri?: string }>("/api/codex-gateway/accounts/oauth/start", {
        method: "POST",
        csrf: actions.csrf,
        body: {},
      });
      if (result.authUrl) {
        window.open(result.authUrl, "codex_gateway_oauth", "popup=yes,width=520,height=720");
        setOauthRedirectUri(result.redirectUri || "");
        setOauthStarted(true);
      }
      actions.setToast("OAuth 登录窗口已打开，登录后粘贴回调 URL 完成导入", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function relayOAuth() {
    const callbackUrl = oauthCallbackUrl.trim();
    if (!callbackUrl) {
      actions.setToast("请粘贴登录后浏览器跳转到的回调 URL", "warn");
      return;
    }
    setBusy("oauth-relay");
    try {
      await actions.api("/api/codex-gateway/accounts/oauth/relay", {
        method: "POST",
        csrf: actions.csrf,
        body: { callbackUrl },
      });
      setOauthCallbackUrl("");
      setOauthStarted(false);
      setOauthRedirectUri("");
      await actions.refreshCodexGateway();
      actions.setToast("OAuth 账号已导入", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function exportAccounts() {
    setBusy("account-export");
    try {
      const response = await fetch("/api/codex-gateway/accounts/export", { headers: { Accept: "application/json" } });
      if (!response.ok) {
        throw new Error(`导出失败：${response.status}`);
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `codex-gateway-accounts-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      actions.setToast("账号配置已导出", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function importAccounts(file: File) {
    setBusy("account-import");
    try {
      const text = await file.text();
      const response = await fetch("/api/codex-gateway/accounts/import", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": actions.csrf || readCookie("pl_csrf") },
        body: text,
      });
      const result = (await response.json().catch(() => ({}))) as {
        added?: number;
        updated?: number;
        failed?: number;
        error?: { message?: string };
      };
      if (!response.ok) {
        throw new Error(result.error?.message || `导入失败：${response.status}`);
      }
      await actions.refreshCodexGateway();
      const failed = result.failed || 0;
      const summary = `导入完成：新增 ${result.added || 0}，更新 ${result.updated || 0}${failed ? `，失败 ${failed}` : ""}`;
      actions.setToast(summary, failed ? "warn" : "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }


  async function createAccount() {
    if (!accountDraft.accessToken.trim() && !accountDraft.refreshToken.trim()) {
      actions.setToast("access token 或 refresh token 至少填写一个", "warn");
      return;
    }
    setBusy("account");
    try {
      await actions.api("/api/codex-gateway/accounts", {
        method: "POST",
        csrf: actions.csrf,
        body: accountDraft,
      });
      setAccountDraft(emptyAccountDraft);
      await actions.refreshCodexGateway();
      actions.setToast("Gateway 账号已添加", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function updateAccountStatus(account: CodexGatewayAccount, status: "active" | "disabled") {
    setBusy(`account-status:${account.id}`);
    try {
      await actions.api(`/api/codex-gateway/accounts/${encodeURIComponent(account.id)}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: { status },
      });
      await actions.refreshCodexGateway();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function checkAccount(account: CodexGatewayAccount) {
    setBusy(`account-check:${account.id}`);
    try {
      await actions.api(`/api/codex-gateway/accounts/${encodeURIComponent(account.id)}/check`, {
        method: "POST",
        csrf: actions.csrf,
      });
      await actions.refreshCodexGateway();
      actions.setToast("账号检查已完成", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      await actions.refreshCodexGateway();
    } finally {
      setBusy("");
    }
  }

  async function refreshAccount(account: CodexGatewayAccount) {
    setBusy(`account-refresh:${account.id}`);
    try {
      await actions.api(`/api/codex-gateway/accounts/${encodeURIComponent(account.id)}/refresh`, {
        method: "POST",
        csrf: actions.csrf,
      });
      await actions.refreshCodexGateway();
      actions.setToast("账号 token 已刷新", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      await actions.refreshCodexGateway();
    } finally {
      setBusy("");
    }
  }

  async function deleteAccount(account: CodexGatewayAccount) {
    const confirmed = await confirmDanger({
      title: "删除 Codex Gateway 账号",
      objectName: account.label || account.id,
      body: "该操作会删除上游账号凭据摘要，Gateway 将不能继续使用该账号转发请求。",
      confirmLabel: "删除账号",
      impact: ["依赖该账号的模型路由会减少可用上游。", "历史请求日志和审计记录会保留。"],
      recovery: "删除不可恢复；需要恢复时必须重新走 OAuth 登录或 token 导入。",
    });
    if (!confirmed) return;
    setBusy(`account-delete:${account.id}`);
    try {
      await actions.api(`/api/codex-gateway/accounts/${encodeURIComponent(account.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await actions.refreshCodexGateway();
      actions.setToast("账号已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function refreshModels() {
    setBusy("models");
    try {
      await actions.api("/api/codex-gateway/models/refresh", {
        method: "POST",
        csrf: actions.csrf,
      });
      await actions.refreshCodexGateway();
      actions.setToast("模型目录已刷新", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      await actions.refreshCodexGateway();
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_320px] max-2xl:grid-cols-1">
        <section className="grid content-start gap-4 p-4">
          <SubTabs
            activeId={tab}
            onChange={(id) => setTab(id as GatewayTab)}
	            tabs={GATEWAY_TABS.map((item) => ({
	              ...item,
	              href: tabHref(item.id),
	              badge: item.id === "logs" && status?.recentFailureCount ? (
                <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{status?.recentFailureCount || 0}</span>
              ) : undefined,
            }))}
          />

          {tab === "overview" ? (
            <div className="grid gap-4">
              <div className="grid grid-cols-5 gap-3 max-xl:grid-cols-3 max-md:grid-cols-2 max-sm:grid-cols-1">
                <Metric detail={status?.enabled ? "OpenAI compatible /v1" : "设置未启用"} label="Gateway" tone={gatewayReady ? "good" : "warn"} value={codexGatewayStatusLabel(status)} />
                <Metric detail={`${activeKeys} 可用`} label="API keys" tone={activeKeys ? "good" : "warn"} value={gateway.apiKeys?.length || 0} />
                <Metric detail={`${activeAccounts} 可用`} label="Codex 账号" tone={activeAccounts ? "good" : "warn"} value={gateway.accounts?.length || 0} />
                <Metric detail="静态 + 上游目录" label="模型" value={status?.models || gateway.models?.length || 0} />
                <Metric detail={`${status?.recentFailureCount || 0} 失败`} label="24h 请求" tone={status?.recentFailureCount ? "warn" : "neutral"} value={status?.recentRequestCount || 0} />
              </div>
              {overviewActions.length ? (
                <Panel title="下一步" subtitle="只在未配置或出现风险时显示，常态操作使用顶部二级导航。">
                  <div className="grid grid-cols-3 gap-3 max-xl:grid-cols-1">
                    {overviewActions.map((item) => (
                      <GatewayActionCard key={item.tab} label={item.label} body={item.body} action={item.action} href={tabHref(item.tab)} onClick={() => setTab(item.tab)} />
                    ))}
                  </div>
                </Panel>
              ) : null}
            </div>
          ) : null}

          {tab === "settings" ? (
            <Panel
              actions={
                <Button disabled={busy === "settings"} onClick={() => void saveSettings()} tone="primary">
                  保存设置
                </Button>
              }
              subtitle="仅保留 Codex OAuth 到 OpenAI 兼容 API 的转换能力；不包含 V2Ray 代理。"
              title="Gateway 设置"
            >
              <div className="grid gap-4">
                <Toggle
                  variant="row"
                  checked={Boolean(draft.enabled)}
                  label="启用公开 /v1 端点"
                  onChange={(checked) => updateSetting("enabled", checked)}
                />

                <Field label="默认指令">
                  <textarea
                    autoComplete="off"
                    className="textarea"
                    name="gateway_default_instructions"
                    onChange={(event) => updateSetting("defaultInstructions", event.target.value)}
                    value={draft.defaultInstructions || ""}
                  />
                </Field>

                <CollapsibleSection
                  subtitle="一般无需修改；如需调整请使用管理接口或数据库。"
                  title="高级设置"
                >
                  <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                    <Field label="请求超时秒数">
                      <input className="input mono" disabled readOnly type="number" value={settings.requestTimeoutSeconds || 600} />
                    </Field>
                    <Field label="刷新提前量秒数">
                      <input className="input mono" disabled readOnly type="number" value={settings.refreshMarginSeconds || 300} />
                    </Field>
                    <Field label="账号健康检查间隔 (秒, 0=禁用)">
                      <input className="input mono" disabled readOnly type="number" value={settings.accountHealthCheckIntervalSeconds ?? 43200} />
                    </Field>
                  </div>

                  <Field label="Codex API Base URL">
                    <input className="input mono" disabled readOnly value={settings.baseUrl || ""} />
                  </Field>

                  <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                    <Field label="OAuth Auth URL">
                      <input className="input mono" disabled readOnly value={settings.oauthAuthUrl || ""} />
                    </Field>
                    <Field label="OAuth Token URL">
                      <input className="input mono" disabled readOnly value={settings.oauthTokenUrl || ""} />
                    </Field>
                    <Field label="OAuth Client ID">
                      <input className="input mono" disabled readOnly value={settings.oauthClientId || ""} />
                    </Field>
                    <Field label="OAuth Redirect URI">
                      <input className="input mono" disabled readOnly value={settings.oauthRedirectUri || ""} />
                    </Field>
                  </div>

                  <Field label="Installation ID">
                    <input className="input mono" disabled readOnly value={settings.installationId || ""} />
                  </Field>
                </CollapsibleSection>
              </div>
            </Panel>
          ) : null}

          {tab === "keys" ? (
            <Panel title="公开 API 密钥" subtitle="公开 /v1 端点只接受这里创建的 key；完整 token 只在创建或轮换后返回一次。">
              <div className="grid gap-4">
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 max-sm:grid-cols-1">
                  <Field label="名称">
                    <input autoComplete="off" className="input" name="gateway_api_key_name" onChange={(event) => setAPIKeyName(event.target.value)} value={apiKeyName} />
                  </Field>
                  <div className="flex items-end">
                    <Button disabled={busy === "api-key"} onClick={() => void createAPIKey()} tone="primary">
                      创建 key
                    </Button>
                  </div>
                </div>
                {oneTimeToken ? (
                  <Notice>
                    <div className="grid gap-2">
                      <strong className="text-sm">新 token</strong>
                      <code className="mono block overflow-auto rounded-md border border-[rgba(199,85,8,0.24)] bg-[var(--surface)] px-2 py-1 text-xs">{oneTimeToken}</code>
                    </div>
                  </Notice>
                ) : null}
                {gateway.apiKeys?.length ? (
                  <div className="grid gap-2">
                    {gateway.apiKeys.map((key) => (
                      <APIKeyRow
                        busy={busy}
                        key={key.id}
                        apiKey={key}
                        onDelete={() => void deleteAPIKey(key)}
                        onRotate={() => void rotateAPIKey(key)}
                        onStatus={(status) => void updateAPIKeyStatus(key, status)}
                      />
                    ))}
                  </div>
                ) : (
                  <EmptyState title="还没有公开 API key" body="创建 key 后，OpenAI SDK 可以访问本服务的 /v1 端点。" />
                )}
              </div>
            </Panel>
          ) : null}

          {tab === "accounts" ? (
            <Panel
              actions={
                <div className="flex flex-wrap items-center gap-2">
	                  <input
	                    accept="application/json,.json,.txt,text/plain"
	                    className="hidden"
	                    name="gateway_accounts_import"
	                    onChange={(event: ChangeEvent<HTMLInputElement>) => {
                      const file = event.target.files?.[0];
                      event.target.value = "";
                      if (file) void importAccounts(file);
                    }}
                    ref={importInputRef}
                    type="file"
                  />
                  <Button disabled={busy === "account-import"} onClick={() => importInputRef.current?.click()}>
                    导入
                  </Button>
                  <Button disabled={busy === "account-export"} onClick={() => void exportAccounts()}>
                    导出
                  </Button>
                  <Button disabled={busy === "oauth"} onClick={() => void startOAuth()}>
                    OAuth 登录
                  </Button>
                </div>
              }
              subtitle="账号凭据保存在本地 SQLite；列表只展示凭据是否存在，不回显 token。"
              title="Codex OAuth 账号"
            >
              <div className="grid gap-4">
                {oauthStarted ? (
                  <Notice>
                    <div className="grid gap-2">
                      <strong className="text-sm">完成 OAuth 登录导入</strong>
                      <p className="muted m-0 text-xs">
                        在弹出窗口完成登录后，浏览器会跳转到回调地址
                        {oauthRedirectUri ? <code className="mono mx-1 break-all">{oauthRedirectUri}</code> : " "}
                        （可能因本机未监听而打不开）。复制该地址栏的完整 URL 粘贴到下方，点击「完成导入」。
                      </p>
                      <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 max-sm:grid-cols-1">
	                        <input autoComplete="off" className="input mono" name="gateway_oauth_callback_url" onChange={(event) => setOauthCallbackUrl(event.target.value)} placeholder="http://localhost:1455/auth/callback?code=AUTH_CODE&state=STATE" spellCheck={false} type="url" value={oauthCallbackUrl} />
                        <Button disabled={busy === "oauth-relay"} onClick={() => void relayOAuth()} tone="primary">
                          完成导入
                        </Button>
                      </div>
                    </div>
                  </Notice>
                ) : null}
                <div className="grid grid-cols-3 gap-3 max-xl:grid-cols-2 max-md:grid-cols-1">
	                  <Field label="标签">
	                    <input autoComplete="off" className="input" name="gateway_account_label" onChange={(event) => setAccountDraft((current) => ({ ...current, label: event.target.value }))} value={accountDraft.label} />
	                  </Field>
	                  <Field label="状态">
	                    <select className="select" name="gateway_account_status" onChange={(event) => setAccountDraft((current) => ({ ...current, status: event.target.value }))} value={accountDraft.status}>
                      <option value="active">active</option>
                      <option value="disabled">disabled</option>
                    </select>
                  </Field>
	                  <Field label="Plan">
	                    <input autoComplete="off" className="input mono" name="gateway_account_plan" onChange={(event) => setAccountDraft((current) => ({ ...current, plan: event.target.value }))} spellCheck={false} value={accountDraft.plan} />
	                  </Field>
	                  <Field label="Access Token">
	                    <input autoComplete="new-password" className="input mono" name="gateway_account_access_token" onChange={(event) => setAccountDraft((current) => ({ ...current, accessToken: event.target.value }))} spellCheck={false} type="password" value={accountDraft.accessToken} />
	                  </Field>
	                  <Field label="Refresh Token">
	                    <input autoComplete="new-password" className="input mono" name="gateway_account_refresh_token" onChange={(event) => setAccountDraft((current) => ({ ...current, refreshToken: event.target.value }))} spellCheck={false} type="password" value={accountDraft.refreshToken} />
	                  </Field>
	                  <Field label="Expires At">
	                    <input autoComplete="off" className="input mono" name="gateway_account_expires_at" onChange={(event) => setAccountDraft((current) => ({ ...current, expiresAt: event.target.value }))} spellCheck={false} value={accountDraft.expiresAt} />
	                  </Field>
                </div>
                <div className="flex justify-end">
                  <Button disabled={busy === "account"} onClick={() => void createAccount()} tone="primary">
                    添加账号
                  </Button>
                </div>

                {gateway.accounts?.length ? (
                  <div className="grid gap-2">
                    {gateway.accounts.map((account) => (
                      <AccountRow
                        account={account}
                        busy={busy}
                        key={account.id}
                        onCheck={() => void checkAccount(account)}
                        onDelete={() => void deleteAccount(account)}
                        onRefresh={() => void refreshAccount(account)}
                        onStatus={(status) => void updateAccountStatus(account, status)}
                      />
                    ))}
                  </div>
                ) : (
                  <EmptyState title="还没有 Codex 账号" body="添加 OAuth 账号后，Gateway 会按模型和账号状态路由请求。" />
                )}
              </div>
            </Panel>
          ) : null}

          {tab === "models" ? (
            <Panel
              actions={
                <Button disabled={busy === "models"} onClick={() => void refreshModels()}>
                  刷新模型
                </Button>
              }
              title="模型目录"
              subtitle="静态模型和上游账号探测得到的模型统一展示在这里。"
            >
              {gateway.models?.length ? (
                <div className="grid gap-2">
                  {gateway.models.map((model) => (
                    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={model.id}>
                      <div className="flex items-center justify-between gap-2">
                        <strong className="mono truncate text-sm">{model.id}</strong>
                        <Pill>{model.source || "静态"}</Pill>
                      </div>
                      <span className="muted mt-2 block text-xs">{model.plans?.length ? model.plans.join(", ") : "全部 plan"}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState title="暂无模型" body="静态模型会在服务启动时写入，账号检查后可补充上游模型。" />
              )}
            </Panel>
          ) : null}

          {tab === "logs" ? (
            <Panel title="请求日志" subtitle="公开 /v1 端点收到请求后会记录摘要、错误来源和延迟。">
              {gateway.requestLogs?.length ? (
                <div className="grid max-h-[calc(100dvh-260px)] gap-2 overflow-auto pr-1">
                  {gateway.requestLogs.map((log) => (
                    <RequestLogRow key={log.id || log.requestId} log={log} />
                  ))}
                </div>
              ) : (
                <EmptyState title="还没有请求日志" body="外部客户端调用 /v1 后，这里会显示请求摘要。" />
              )}
            </Panel>
          ) : null}
        </section>

        <aside className="grid content-start gap-4 border-l border-[var(--line)] bg-[var(--surface-soft)] p-4 max-2xl:border-l-0 max-2xl:border-t">
          <Panel title="端点">
            <ContextList
              items={[
                ["Base URL", <code className="mono break-all">/v1</code>],
                ["模型", <code className="mono">GET /v1/models</code>],
                ["Chat", <code className="mono">POST /v1/chat/completions</code>],
                ["Responses", <code className="mono">POST /v1/responses</code>],
                ["状态", <Pill tone={gatewayReady ? "good" : "warn"}>{codexGatewayStatusLabel(status)}</Pill>],
              ]}
            />
          </Panel>
          <Panel title="边界">
            <ContextList
              items={[
                ["执行能力", "不绑定工作区，不执行 shell"],
                ["上游账号", `${activeAccounts} 可用 / ${gateway.accounts?.length || 0} 总数`],
                ["公开 key", `${activeKeys} 可用 / ${gateway.apiKeys?.length || 0} 总数`],
                ["模型", String(status?.models || gateway.models?.length || 0)],
                ["24h 请求", `${status?.recentRequestCount || 0} 请求 / ${status?.recentFailureCount || 0} 失败`],
              ]}
            />
          </Panel>
        </aside>
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function GatewayActionCard({ label, body, action, href, onClick }: { label: string; body: string; action: string; href: string; onClick: () => void }) {
  return (
    <div className="grid content-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div>
        <strong className="block text-sm">{label}</strong>
        <p className="muted mt-1 mb-0 text-xs leading-relaxed">{body}</p>
      </div>
      <div>
        <a
          className="button min-h-8 px-2 text-xs"
          href={href}
          onClick={(event) => {
            if (!shouldHandleQueryLinkClick(event)) return;
            event.preventDefault();
            onClick();
          }}
        >
          {action}
        </a>
      </div>
    </div>
  );
}

function APIKeyRow({
  apiKey,
  busy,
  onDelete,
  onRotate,
  onStatus,
}: {
  apiKey: CodexGatewayAPIKey;
  busy: string;
  onDelete: () => void;
  onRotate: () => void;
  onStatus: (status: "active" | "disabled") => void;
}) {
  const active = apiKey.status === "active";
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-lg:grid-cols-1">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="truncate text-sm">{apiKey.name || apiKey.id}</strong>
          <Pill tone={active ? "good" : "warn"}>{apiKey.status || "unknown"}</Pill>
        </div>
        <div className="muted mono mt-2 grid gap-1 text-xs">
          <span>id: {apiKey.id}</span>
          <span>最近使用: {formatDate(apiKey.lastUsedAt) || "-"}</span>
        </div>
      </div>
      <div className="flex flex-wrap items-start justify-end gap-2 max-lg:justify-start">
        <Button disabled={busy === `key-status:${apiKey.id}`} onClick={() => onStatus(active ? "disabled" : "active")}>
          {active ? "禁用" : "启用"}
        </Button>
        <Button disabled={busy === `key-rotate:${apiKey.id}`} onClick={onRotate}>
          轮换
        </Button>
        <Button disabled={busy === `key-delete:${apiKey.id}`} onClick={onDelete} tone="danger">
          删除
        </Button>
      </div>
    </div>
  );
}

function AccountRow({
  account,
  busy,
  onCheck,
  onDelete,
  onRefresh,
  onStatus,
}: {
  account: CodexGatewayAccount;
  busy: string;
  onCheck: () => void;
  onDelete: () => void;
  onRefresh: () => void;
  onStatus: (status: "active" | "disabled") => void;
}) {
  const active = account.status === "active";
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-xl:grid-cols-1">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="truncate text-sm">{account.label || account.id}</strong>
          <Pill tone={accountTone(account.status)}>{codexGatewayAccountStatusLabel(account.status)}</Pill>
          {account.plan ? <Pill>{account.plan}</Pill> : null}
          {account.hasAccessToken ? <Pill tone="good">access</Pill> : <Pill tone="warn">无 access</Pill>}
          {account.hasRefreshToken ? <Pill tone="good">refresh</Pill> : null}
        </div>
        <div className="muted mono mt-2 grid gap-1 text-xs">
          <span>id: {account.id}</span>
          <span>过期: {formatDate(account.expiresAt) || "-"}</span>
          <span>检查: {formatDate(account.checkedAt) || "-"}</span>
          {account.lastError ? <span className="text-[var(--danger)]">错误: {account.lastError}</span> : null}
        </div>
      </div>
      <div className="flex flex-wrap items-start justify-end gap-2 max-xl:justify-start">
        <Button disabled={busy === `account-status:${account.id}`} onClick={() => onStatus(active ? "disabled" : "active")}>
          {active ? "禁用" : "启用"}
        </Button>
        <Button disabled={busy === `account-check:${account.id}`} onClick={onCheck}>
          检查
        </Button>
        <Button disabled={!account.hasRefreshToken || busy === `account-refresh:${account.id}`} onClick={onRefresh}>
          刷新
        </Button>
        <Button disabled={busy === `account-delete:${account.id}`} onClick={onDelete} tone="danger">
          删除
        </Button>
      </div>
    </div>
  );
}

function RequestLogRow({ log }: { log: CodexGatewayRequestLog }) {
  const failed = (log.statusCode || 0) >= 400;
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <strong className="mono">{log.apiKind || "request"}</strong>
        <Pill tone={failed ? "danger" : "good"}>{log.statusCode || 0}</Pill>
        {log.streamed ? <Pill>stream</Pill> : null}
      </div>
      <div className="muted mono mt-2 grid gap-1">
        <span>{log.model || "-"}</span>
        <span>{formatDate(log.createdAt) || "-"} / {log.latencyMs || 0}ms</span>
        {log.errorCode ? <span className="text-[var(--danger)]">{log.errorCode}: {log.errorMessage || "-"}</span> : null}
      </div>
    </div>
  );
}


function normalizeGatewaySettings(draft: Required<CodexGatewaySettings>): CodexGatewaySettings {
  // Only the two user-facing knobs are persisted from the web UI; advanced
  // fields are rendered as read-only in the CollapsibleSection and must be
  // changed via an admin endpoint or the database directly.
  return {
    enabled: Boolean(draft.enabled),
    defaultInstructions: draft.defaultInstructions.trim(),
  };
}

function changedGatewaySettings(next: CodexGatewaySettings, current: Required<CodexGatewaySettings>): CodexGatewaySettings {
  const patch: CodexGatewaySettings = {};
  if (next.enabled !== current.enabled) patch.enabled = next.enabled;
  if (next.defaultInstructions !== current.defaultInstructions) patch.defaultInstructions = next.defaultInstructions;
  return patch;
}

function accountTone(status?: string): Tone {
  if (status === "active") return "good";
  if (status === "invalid" || status === "rate_limited") return "danger";
  if (status === "disabled") return "warn";
  return "neutral";
}
