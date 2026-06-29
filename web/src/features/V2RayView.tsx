import { useEffect, useState } from "react";
import { Toggle } from "../components/ui";
import type { AppActions } from "../app/App";
import type { AppData, V2RayExport, V2RayRemoteClient, V2RaySettings, V2RayStatus } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, EmptyState, Field, Notice, Panel, Pill, useDangerConfirm } from "../components/ui";
import { defaultV2RaySettings, formatDate, maskSecret, v2raySecurityLabel, v2rayStateLabel, v2rayTransportLabel } from "../domain/labels";

type V2RayControlAction = "start" | "stop" | "restart";

interface ValidationResult {
  ok?: boolean;
  message?: string;
  configHash?: string;
  settingsHash?: string;
  configJson?: string;
}

const emptyClient = {
  label: "",
  email: "",
  level: 0,
  alterId: 0,
  enabled: true,
};

export function V2RayView({ actions, data, exportOpen, exported }: { actions: AppActions; data: AppData; exportOpen: boolean; exported: V2RayExport | null }) {
  const savedSettings = { ...defaultV2RaySettings(), ...(data.v2ray.settings || {}) };
  const [v2ray, setV2Ray] = useState<V2RaySettings>(savedSettings);
  const [clientDraft, setClientDraft] = useState(emptyClient);
  const [clientCreateOpen, setClientCreateOpen] = useState(false);
  const [validation, setValidation] = useState<ValidationResult | null>(null);
  const [busy, setBusy] = useState("");
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const status = data.v2ray.status;
  const clients = data.v2ray.clients || [];
  const settingsDirty = !sameV2RaySettings(v2ray, savedSettings);
  const clientFormOpen = clientCreateOpen || !clients.length;

  useEffect(() => {
    setV2Ray(savedSettings);
  }, [data.v2ray.settings]);

  async function saveV2Ray() {
    setBusy("v2ray");
    try {
      await actions.api("/api/v2ray/settings", { method: "PUT", csrf: actions.csrf, body: normalizedV2Ray(v2ray) });
      await actions.refreshV2Ray();
      actions.setToast("V2Ray 设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveAndControlV2Ray() {
    const action: V2RayControlAction = status?.running ? "restart" : "start";
    setBusy("save-control");
    try {
      await actions.api("/api/v2ray/settings", { method: "PUT", csrf: actions.csrf, body: normalizedV2Ray(v2ray) });
      await actions.api<V2RayStatus>("/api/v2ray/control", { method: "POST", csrf: actions.csrf, body: { action } });
      await actions.refreshV2Ray();
      actions.setToast(status?.running ? "V2Ray 设置已保存并重启" : "V2Ray 设置已保存并启动", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function validateV2Ray() {
    setBusy("validate");
    try {
      const result = await actions.api<ValidationResult>("/api/v2ray/validate", { method: "POST", csrf: actions.csrf, body: { settings: normalizedV2Ray(v2ray) } });
      setValidation(result);
      actions.setToast(result.message || "V2Ray 配置校验通过", "good");
    } catch (error) {
      setValidation({ ok: false, message: friendlyError(error) });
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function controlV2Ray(action: V2RayControlAction) {
    if (action === "stop") {
      const confirmed = await confirmDanger({
        title: "停止 V2Ray 服务",
        objectName: status?.endpoint || "V2Ray",
        body: "该操作会停止当前 V2Ray 进程，远程设备连接会中断。",
        confirmLabel: "停止服务",
        impact: ["现有远程连接会断开。", "已保存配置和远程设备不会被删除。"],
        recovery: "需要恢复接入时，可以从服务状态重新启动。",
      });
      if (!confirmed) return;
    }
    setBusy(action);
    try {
      await actions.api<V2RayStatus>("/api/v2ray/control", { method: "POST", csrf: actions.csrf, body: { action } });
      await actions.refreshV2Ray();
      actions.setToast("V2Ray 状态已更新", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function createClient() {
    if (!clientDraft.label.trim()) {
      actions.setToast("设备名称不能为空", "warn");
      return;
    }
    setBusy("client");
    try {
      await actions.api<V2RayRemoteClient>("/api/v2ray/clients", { method: "POST", csrf: actions.csrf, body: clientDraft });
      setClientDraft(emptyClient);
      setClientCreateOpen(false);
      await actions.refreshV2Ray();
      actions.setToast("已添加远程设备", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function updateClient(client: V2RayRemoteClient, enabled: boolean) {
    setBusy(client.id);
    try {
      await actions.api<V2RayRemoteClient>(`/api/v2ray/clients/${encodeURIComponent(client.id)}`, {
        method: "PUT",
        csrf: actions.csrf,
        body: { ...client, enabled },
      });
      await actions.refreshV2Ray();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function rotateClient(client: V2RayRemoteClient) {
    const confirmed = await confirmDanger({
      title: "轮换远程设备 UUID",
      objectName: client.label || client.email || client.id,
      body: "该操作会替换该设备的接入凭据，旧配置将无法继续连接。",
      confirmLabel: "轮换 UUID",
      impact: ["需要重新导出并更新远程设备配置。", "其他设备不受影响。"],
      recovery: "如果远程设备还未更新，请暂缓轮换。",
    });
    if (!confirmed) return;
    setBusy(`rotate-${client.id}`);
    try {
      await actions.api<V2RayRemoteClient>(`/api/v2ray/clients/${encodeURIComponent(client.id)}/rotate`, { method: "POST", csrf: actions.csrf });
      await actions.refreshV2Ray();
      actions.setToast("UUID 已轮换", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function exportClient(client: V2RayRemoteClient) {
    setBusy(`export-${client.id}`);
    try {
      const result = await actions.api<V2RayExport>(`/api/v2ray/clients/${encodeURIComponent(client.id)}/export`);
      actions.setV2RayExport(result);
      actions.setV2RayExportOpen(true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function revokeClient(client: V2RayRemoteClient) {
    const confirmed = await confirmDanger({
      title: "撤销远程设备",
      objectName: client.label || client.email || client.id,
      body: "该操作会撤销设备接入凭据，设备将不能继续连接 V2Ray。",
      confirmLabel: "撤销设备",
      impact: ["设备配置会失效。", "服务端配置和其他设备不受影响。"],
      recovery: "撤销不可恢复；需要重新添加设备并导出新配置。",
    });
    if (!confirmed) return;
    setBusy(`revoke-${client.id}`);
    try {
      await actions.api(`/api/v2ray/clients/${encodeURIComponent(client.id)}`, { method: "DELETE", csrf: actions.csrf });
      await actions.refreshV2Ray();
      actions.setToast("远程设备已撤销", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  return (
    <>
    <div className="grid gap-4 p-4">
      <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-xl:grid-cols-1">
        <Panel
          actions={
            <Button disabled={busy === "validate"} onClick={() => void validateV2Ray()}>
              校验
            </Button>
          }
          subtitle="Phantom Lancer 内嵌 V2Ray core；配置面只负责校验和保存。"
          title="服务端配置"
        >
          <div className="grid gap-4">
            <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-2 max-md:grid-cols-1">
              <Field label="公网主机名或 IP">
                <input autoComplete="off" className="input mono" name="v2ray_public_host" onChange={(event) => updateV2Ray("publicHost", event.target.value)} placeholder="example.com" spellCheck={false} value={v2ray.publicHost || ""} />
              </Field>
              <Field label="监听地址">
                <input autoComplete="off" className="input mono" name="v2ray_listen_addr" onChange={(event) => updateV2Ray("listen", event.target.value)} spellCheck={false} value={v2ray.listen || ""} />
              </Field>
              <Field label="端口">
                <input className="input mono" inputMode="numeric" min={1} max={65535} name="v2ray_port" onChange={(event) => updateV2Ray("port", Number(event.target.value || 0))} type="number" value={v2ray.port || 0} />
              </Field>
              <Field label="传输">
                <select className="select" name="v2ray_transport" onChange={(event) => updateV2Ray("transport", event.target.value)} value={v2ray.transport || "tcp"}>
                  <option value="tcp">TCP</option>
                  <option value="ws">WebSocket</option>
                </select>
              </Field>
              <Field label="安全层">
                <select className="select" name="v2ray_security" onChange={(event) => updateV2Ray("security", event.target.value)} value={v2ray.security || "none"}>
                  <option value="none">None</option>
                  <option value="tls">TLS</option>
                </select>
              </Field>
              <Field label="日志等级">
                <select className="select" name="v2ray_log_level" onChange={(event) => updateV2Ray("logLevel", event.target.value)} value={v2ray.logLevel || "warning"}>
                  <option value="debug">debug</option>
                  <option value="info">info</option>
                  <option value="warning">warning</option>
                  <option value="error">error</option>
                </select>
              </Field>
            </div>

            {v2ray.transport === "ws" ? (
              <Field label="WebSocket Path">
                <input autoComplete="off" className="input mono" name="v2ray_ws_path" onChange={(event) => updateV2Ray("wsPath", event.target.value)} spellCheck={false} value={v2ray.wsPath || ""} />
              </Field>
            ) : null}

            {v2ray.security === "tls" ? (
              <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
                <Field label="TLS 证书路径">
                  <input autoComplete="off" className="input mono" name="v2ray_tls_cert_file" onChange={(event) => updateV2Ray("tlsCertFile", event.target.value)} spellCheck={false} value={v2ray.tlsCertFile || ""} />
                </Field>
                <Field label="TLS 私钥路径">
                  <input autoComplete="off" className="input mono" name="v2ray_tls_key_file" onChange={(event) => updateV2Ray("tlsKeyFile", event.target.value)} spellCheck={false} value={v2ray.tlsKeyFile || ""} />
                </Field>
              </div>
            ) : null}

            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
              <Toggle checked={Boolean(v2ray.startOnPhantomLaunch)} label="随服务启动" name="v2ray_start_on_launch" onChange={(checked) => updateV2Ray("startOnPhantomLaunch", checked)} />
              <Toggle checked={Boolean(v2ray.sniffingEnabled)} label="启用 sniffing" name="v2ray_sniffing_enabled" onChange={(checked) => updateV2Ray("sniffingEnabled", checked)} />
              <Toggle checked={Boolean(v2ray.blockPrivateNetwork)} label="阻断远程设备访问私网" name="v2ray_block_private_network" onChange={(checked) => updateV2Ray("blockPrivateNetwork", checked)} />
            </div>

            {!v2ray.blockPrivateNetwork ? <Notice>关闭私网阻断会提高远程接入风险，建议只在明确需要时使用。</Notice> : null}
            {(v2ray.port || 0) > 0 && (v2ray.port || 0) < 1024 ? (
              <Notice>低于 1024 的端口需要 Phantom Lancer 进程具备系统绑定权限；如果启动失败，请使用 root、cap_net_bind_service、端口转发或反向代理处理。</Notice>
            ) : null}
            {validation ? (
              <Notice>
                {validation.ok ? validation.message || "配置校验通过" : validation.message || "配置校验失败"}
                {validation.configHash ? ` / ${validation.configHash}` : ""}
              </Notice>
            ) : null}

            <div className="flex flex-wrap justify-between gap-2">
              <span className="muted text-xs">
                {v2rayTransportLabel(v2ray.transport)} / {v2raySecurityLabel(v2ray.security)}
              </span>
              <div className="flex flex-wrap justify-end gap-2">
                <Button disabled={busy === "v2ray"} onClick={() => void saveV2Ray()}>
                  保存设置
                </Button>
                <Button disabled={busy === "save-control"} onClick={() => void saveAndControlV2Ray()} tone="primary">
                  {status?.running ? "保存并重启" : "保存并启动"}
                </Button>
              </div>
            </div>
          </div>
        </Panel>

        <Panel title="服务状态">
          <div className="grid gap-3">
            {settingsDirty ? <Notice>当前表单有未保存配置；请先保存，或使用配置面板的“保存并启动/重启”。</Notice> : null}
            <ContextList
              items={[
                ["状态", <Pill tone={status?.running ? "good" : "warn"}>{v2rayStateLabel(status)}</Pill>],
                ["端点", status?.endpoint || "-"],
                ["配置", status?.configPath || "-"],
                ["版本", status?.coreVersion || "-"],
                ["远程设备", `${status?.enabledRemoteClients || 0}/${status?.remoteClientCount || 0}`],
                ["错误", status?.lastError || "-"],
              ]}
            />
            <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <div className="flex items-center justify-between gap-3">
                <strong className="text-xs text-[var(--muted-strong)]">已保存配置操作</strong>
                <span className="muted text-xs">{settingsDirty ? "等待保存" : "可直接执行"}</span>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button className="min-h-8 px-2 text-xs" disabled={settingsDirty || status?.running || busy === "start"} onClick={() => void controlV2Ray("start")}>
                  启动
                </Button>
                <Button className="min-h-8 px-2 text-xs" disabled={settingsDirty || !status?.running || busy === "restart"} onClick={() => void controlV2Ray("restart")}>
                  重启
                </Button>
                <Button className="min-h-8 px-2 text-xs" disabled={!status?.running || busy === "stop"} onClick={() => void controlV2Ray("stop")} tone="danger">
                  停止
                </Button>
              </div>
            </div>
          </div>
        </Panel>
      </div>

      <Panel
        title="远程设备"
        subtitle="为手机或其他远程设备生成 VMess 接入配置；UUID 默认由后端生成。"
        actions={clients.length ? (
          <Button onClick={() => setClientCreateOpen((open) => !open)}>
            {clientFormOpen ? "收起添加" : "添加设备"}
          </Button>
        ) : null}
      >
        <div className="grid gap-4">
          {clientFormOpen ? (
          <div className="grid grid-cols-[1fr_1fr_96px_96px_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
            <Field label="名称">
              <input autoComplete="off" className="input" name="v2ray_client_label" onChange={(event) => setClientDraft((current) => ({ ...current, label: event.target.value }))} value={clientDraft.label} />
            </Field>
            <Field label="Email">
              <input autoComplete="off" className="input" name="v2ray_client_email" onChange={(event) => setClientDraft((current) => ({ ...current, email: event.target.value }))} spellCheck={false} type="email" value={clientDraft.email} />
            </Field>
            <Field label="Level">
              <input className="input mono" inputMode="numeric" name="v2ray_client_level" onChange={(event) => setClientDraft((current) => ({ ...current, level: Number(event.target.value || 0) }))} type="number" value={clientDraft.level} />
            </Field>
            <Field label="Alter ID">
              <input className="input mono" inputMode="numeric" name="v2ray_client_alter_id" onChange={(event) => setClientDraft((current) => ({ ...current, alterId: Number(event.target.value || 0) }))} type="number" value={clientDraft.alterId} />
            </Field>
            <div className="flex items-end">
              <Button className="w-full" disabled={busy === "client"} onClick={() => void createClient()} tone="primary">
                添加
              </Button>
            </div>
          </div>
          ) : null}

          {clients.length ? (
            <div className="grid gap-2">
              {clients.map((client) => (
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-lg:grid-cols-1" key={client.id}>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="truncate text-sm">{client.label || client.email || client.id}</strong>
                      <Pill tone={client.enabled ? "good" : "warn"}>{client.enabled ? "已启用" : "已禁用"}</Pill>
                      {client.revokedAt ? <Pill tone="danger">已撤销</Pill> : null}
                    </div>
                    <div className="muted mono mt-2 grid gap-1 text-xs">
                      <span>id: {client.id}</span>
                      <span>uuid: {maskSecret(client.uuid)}</span>
                      <span>更新: {formatDate(client.updatedAt) || "-"}</span>
                    </div>
                  </div>
                  <div className="flex flex-wrap items-start justify-end gap-2 max-lg:justify-start">
                    <Button disabled={busy === client.id} onClick={() => void updateClient(client, !client.enabled)}>
                      {client.enabled ? "禁用" : "启用"}
                    </Button>
                    <Button disabled={busy === `export-${client.id}`} onClick={() => void exportClient(client)}>
                      导出
                    </Button>
                    <Button disabled={busy === `rotate-${client.id}`} onClick={() => void rotateClient(client)}>
                      轮换
                    </Button>
                    <Button disabled={busy === `revoke-${client.id}`} onClick={() => void revokeClient(client)} tone="danger">
                      撤销
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无远程设备" body="添加设备后，可以导出远程设备接入配置。" />
          )}
        </div>
      </Panel>

      {exportOpen && exported ? (
        <Panel
          actions={<Button onClick={() => actions.setV2RayExportOpen(false)}>关闭</Button>}
          subtitle="分享 URI 包含远程设备凭据，只在受信任环境中使用。"
          title="远程设备接入配置"
        >
          <div className="grid gap-3">
            <ContextList
              items={[
                ["设备", exported.label || exported.clientId || "-"],
                ["端点", exported.endpoint || "-"],
                ["分享 URI", <code className="mono break-all text-xs">{exported.shareUri || "-"}</code>],
              ]}
            />
            <pre className="mono max-h-96 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed whitespace-pre-wrap">
              {JSON.stringify(exported.clientConfig || {}, null, 2)}
            </pre>
          </div>
        </Panel>
      ) : null}
    </div>
    {dangerConfirmDialog}
    </>
  );

  function updateV2Ray<Key extends keyof V2RaySettings>(key: Key, value: V2RaySettings[Key]) {
    setV2Ray((current) => ({ ...current, [key]: value }));
  }
}


function normalizedV2Ray(settings: V2RaySettings): V2RaySettings {
  return {
    ...defaultV2RaySettings(),
    ...settings,
    id: "default",
    port: Number(settings.port || 10086),
  };
}

function sameV2RaySettings(a: V2RaySettings, b: V2RaySettings): boolean {
  return JSON.stringify(normalizedV2Ray(a)) === JSON.stringify(normalizedV2Ray(b));
}
