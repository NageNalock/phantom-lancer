import { useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexGatewayAPIKey, CodexGatewayAccount, CodexGatewayRequestLog, CodexGatewaySettings, Tone } from "../app/types";
import { friendlyError, readCookie } from "../api/client";
import { Button, CollapsibleSection, ContextList, EmptyState, Field, Metric, Notice, Panel, Pill, Toggle } from "../components/ui";
import { codexGatewayAccountStatusLabel, codexGatewayStatusLabel, defaultCodexGatewaySettings, formatDate } from "../domain/labels";

type GatewayAccountDraft = {
  label: string;
  status: string;
  accessToken: string;
  refreshToken: string;
  expiresAt: string;
  plan: string;
};

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
  const [oauthStarted, setOauthStarted] = useState(false);
  const [oauthRedirectUri, setOauthRedirectUri] = useState("");
  const [oauthCallbackUrl, setOauthCallbackUrl] = useState("");
  const importInputRef = useRef<HTMLInputElement | null>(null);

  const activeAccounts = gateway.accounts?.filter((account) => account.status === "active").length || 0;
  const activeKeys = gateway.apiKeys?.filter((key) => key.status === "active").length || 0;
  const gatewayReady = Boolean(status?.enabled && activeAccounts && activeKeys);

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
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_340px] max-2xl:grid-cols-1">
      <section className="grid content-start gap-4 p-4">
        <div className="grid grid-cols-5 gap-3 max-xl:grid-cols-3 max-md:grid-cols-2 max-sm:grid-cols-1">
          <Metric detail={status?.enabled ? "OpenAI compatible /v1" : "settings disabled"} label="Gateway" tone={gatewayReady ? "good" : "warn"} value={codexGatewayStatusLabel(status)} />
          <Metric detail={`${activeKeys} active`} label="API keys" tone={activeKeys ? "good" : "warn"} value={gateway.apiKeys?.length || 0} />
          <Metric detail={`${activeAccounts} active`} label="Codex accounts" tone={activeAccounts ? "good" : "warn"} value={gateway.accounts?.length || 0} />
          <Metric detail="static + upstream catalog" label="Models" value={status?.models || gateway.models?.length || 0} />
          <Metric detail={`${status?.recentFailureCount || 0} failed`} label="24h requests" tone={status?.recentFailureCount ? "warn" : "neutral"} value={status?.recentRequestCount || 0} />
        </div>

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

            <Field label="Default Instructions">
              <textarea
                className="textarea"
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
                  <input
                    className="input mono"
                    disabled
                    readOnly
                    type="number"
                    value={settings.requestTimeoutSeconds || 600}
                  />
                </Field>
                <Field label="刷新提前量秒数">
                  <input
                    className="input mono"
                    disabled
                    readOnly
                    type="number"
                    value={settings.refreshMarginSeconds || 300}
                  />
                </Field>
                <Field label="账号健康检查间隔 (秒, 0=禁用)">
                  <input
                    className="input mono"
                    disabled
                    readOnly
                    type="number"
                    value={settings.accountHealthCheckIntervalSeconds ?? 43200}
                  />
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

        <Panel title="Public API Keys" subtitle="公开 /v1 端点只接受这里创建的 key；完整 token 只在创建或轮换后返回一次。">
          <div className="grid gap-4">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 max-sm:grid-cols-1">
              <Field label="名称">
                <input className="input" onChange={(event) => setAPIKeyName(event.target.value)} value={apiKeyName} />
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
              <EmptyState title="还没有 public API key" body="创建 key 后，OpenAI SDK 可以访问本服务的 /v1 端点。" />
            )}
          </div>
        </Panel>

        <Panel
          actions={
            <div className="flex flex-wrap items-center gap-2">
              <input
                accept="application/json,.json,.txt,text/plain"
                className="hidden"
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
                    <input
                      className="input mono"
                      onChange={(event) => setOauthCallbackUrl(event.target.value)}
                      placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                      value={oauthCallbackUrl}
                    />
                    <Button disabled={busy === "oauth-relay"} onClick={() => void relayOAuth()} tone="primary">
                      完成导入
                    </Button>
                  </div>
                </div>
              </Notice>
            ) : null}
            <div className="grid grid-cols-3 gap-3 max-xl:grid-cols-2 max-md:grid-cols-1">
              <Field label="标签">
                <input className="input" onChange={(event) => setAccountDraft((current) => ({ ...current, label: event.target.value }))} value={accountDraft.label} />
              </Field>
              <Field label="状态">
                <select className="select" onChange={(event) => setAccountDraft((current) => ({ ...current, status: event.target.value }))} value={accountDraft.status}>
                  <option value="active">active</option>
                  <option value="disabled">disabled</option>
                </select>
              </Field>
              <Field label="Plan">
                <input className="input mono" onChange={(event) => setAccountDraft((current) => ({ ...current, plan: event.target.value }))} value={accountDraft.plan} />
              </Field>
              <Field label="Access Token">
                <input className="input mono" onChange={(event) => setAccountDraft((current) => ({ ...current, accessToken: event.target.value }))} type="password" value={accountDraft.accessToken} />
              </Field>
              <Field label="Refresh Token">
                <input className="input mono" onChange={(event) => setAccountDraft((current) => ({ ...current, refreshToken: event.target.value }))} type="password" value={accountDraft.refreshToken} />
              </Field>
              <Field label="Expires At">
                <input className="input mono" onChange={(event) => setAccountDraft((current) => ({ ...current, expiresAt: event.target.value }))} value={accountDraft.expiresAt} />
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
      </section>

      <aside className="grid content-start gap-4 border-l border-[var(--line)] bg-[var(--surface-soft)] p-4 max-2xl:border-l-0 max-2xl:border-t">
        <Panel title="Endpoint">
          <ContextList
            items={[
              ["Base URL", <code className="mono break-all">/v1</code>],
              ["Models", <code className="mono">GET /v1/models</code>],
              ["Chat", <code className="mono">POST /v1/chat/completions</code>],
              ["Responses", <code className="mono">POST /v1/responses</code>],
              ["状态", <Pill tone={gatewayReady ? "good" : "warn"}>{codexGatewayStatusLabel(status)}</Pill>],
            ]}
          />
        </Panel>

        <Panel
          actions={
            <Button disabled={busy === "models"} onClick={() => void refreshModels()}>
              刷新
            </Button>
          }
          title="模型目录"
        >
          {gateway.models?.length ? (
            <div className="grid max-h-72 gap-2 overflow-auto pr-1">
              {gateway.models.map((model) => (
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3" key={model.id}>
                  <div className="flex items-center justify-between gap-2">
                    <strong className="mono truncate text-sm">{model.id}</strong>
                    <Pill>{model.source || "static"}</Pill>
                  </div>
                  <span className="muted mt-2 block text-xs">{model.plans?.length ? model.plans.join(", ") : "all plans"}</span>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无模型" body="静态模型会在服务启动时写入，账号检查后可补充上游模型。" />
          )}
        </Panel>

        <Panel title="最近请求">
          {gateway.requestLogs?.length ? (
            <div className="grid max-h-[520px] gap-2 overflow-auto pr-1">
              {gateway.requestLogs.slice(0, 12).map((log) => (
                <RequestLogRow key={log.id || log.requestId} log={log} />
              ))}
            </div>
          ) : (
            <EmptyState title="还没有请求日志" body="公开 /v1 端点收到请求后会记录摘要和错误来源。" />
          )}
        </Panel>
      </aside>
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
          <span>last used: {formatDate(apiKey.lastUsedAt) || "-"}</span>
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
          {account.hasAccessToken ? <Pill tone="good">access</Pill> : <Pill tone="warn">no access</Pill>}
          {account.hasRefreshToken ? <Pill tone="good">refresh</Pill> : null}
        </div>
        <div className="muted mono mt-2 grid gap-1 text-xs">
          <span>id: {account.id}</span>
          <span>expires: {formatDate(account.expiresAt) || "-"}</span>
          <span>checked: {formatDate(account.checkedAt) || "-"}</span>
          {account.lastError ? <span className="text-[var(--danger)]">error: {account.lastError}</span> : null}
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
  const keys: Array<keyof CodexGatewaySettings> = [
    "enabled",
    "defaultInstructions",
  ];
  for (const key of keys) {
    if (next[key] !== current[key]) {
      (patch as Record<keyof CodexGatewaySettings, string | number | boolean | undefined>)[key] = next[key];
    }
  }
  return patch;
}

function accountTone(status?: string): Tone {
  if (status === "active") return "good";
  if (status === "invalid" || status === "rate_limited") return "danger";
  if (status === "disabled") return "warn";
  return "neutral";
}
