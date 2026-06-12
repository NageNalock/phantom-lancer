import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { CodexSettings, CodexWorkspace } from "../../app/types";
import { Button, Field, Panel } from "../../components/ui";
import { friendlyError } from "../../api/client";

export function CodexSettingsTab({ actions, onChange }: { actions: AppActions; onChange: () => void }) {
  const [settings, setSettings] = useState<CodexSettings>({});
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [settingsResp, workspaceResp] = await Promise.all([
        actions.api<{ settings?: CodexSettings }>("/api/codex/settings"),
        actions.api<{ items?: CodexWorkspace[] }>("/api/codex/workspaces"),
      ]);
      setSettings(settingsResp.settings || {});
      setWorkspaces(workspaceResp.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions]);

  useEffect(() => {
    void load();
  }, [load]);

  function update<K extends keyof CodexSettings>(key: K, value: CodexSettings[K]) {
    setSettings((current) => ({ ...current, [key]: value }));
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    try {
      const response = await actions.api<{ settings?: CodexSettings }>("/api/codex/settings", { method: "PUT", csrf: actions.csrf, body: settings });
      setSettings(response.settings || {});
      onChange();
      actions.setToast("已保存 Codex 模块设置", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel actions={<Button onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>} subtitle="仅 Codex 模块自身配置；全局允许根目录等仍在通用设置。" title="Codex Settings">
      <form className="grid grid-cols-2 gap-3 max-lg:grid-cols-1" onSubmit={save}>
        <Field label="启用 Codex 模块">
          <select className="select" onChange={(event) => update("enabled", event.target.value === "true")} value={String(settings.enabled ?? true)}>
            <option value="true">启用</option>
            <option value="false">停用</option>
          </select>
        </Field>
        <Field label="codex 二进制路径" help="留空则从 PATH 自动探测。">
          <input className="input" onChange={(event) => update("binaryPath", event.target.value)} placeholder="自动探测" value={settings.binaryPath || ""} />
        </Field>
        <Field label="CODEX_HOME" help="留空则使用运行用户默认目录。">
          <input className="input" onChange={(event) => update("codexHome", event.target.value)} placeholder="默认" value={settings.codexHome || ""} />
        </Field>
        <Field label="默认模型">
          <input className="input" onChange={(event) => update("defaultModel", event.target.value)} placeholder="以运行时探测为准" value={settings.defaultModel || ""} />
        </Field>
        <Field label="默认沙箱">
          <select className="select" onChange={(event) => update("defaultSandbox", event.target.value)} value={settings.defaultSandbox || "read-only"}>
            <option value="read-only">read-only</option>
            <option value="workspace-write">workspace-write</option>
          </select>
        </Field>
        <Field label="默认审批策略">
          <select className="select" onChange={(event) => update("defaultApprovalPolicy", event.target.value)} value={settings.defaultApprovalPolicy || "on-request"}>
            <option value="on-request">on-request</option>
          </select>
        </Field>
        <Field label="启用 app-server">
          <select className="select" onChange={(event) => update("appServerEnabled", event.target.value === "true")} value={String(settings.appServerEnabled ?? true)}>
            <option value="true">启用</option>
            <option value="false">停用</option>
          </select>
        </Field>
        <Field label="app-server 探测间隔（秒）">
          <input className="input" type="number" min={5} max={600} onChange={(event) => update("appServerProbeIntervalSeconds", Number(event.target.value))} value={settings.appServerProbeIntervalSeconds ?? 20} />
        </Field>
        <Field label="随服务启动自动启动 app-server">
          <select className="select" onChange={(event) => update("appServerStartOnLaunch", event.target.value === "true")} value={String(settings.appServerStartOnLaunch ?? false)}>
            <option value="false">否（建议，由 owner 手动启动）</option>
            <option value="true">是</option>
          </select>
        </Field>
        <Field label="启用 exec 兜底">
          <select className="select" onChange={(event) => update("execFallbackEnabled", event.target.value === "true")} value={String(settings.execFallbackEnabled ?? true)}>
            <option value="true">启用</option>
            <option value="false">停用</option>
          </select>
        </Field>
        <Field label="事件保留天数">
          <input className="input" type="number" min={0} onChange={(event) => update("eventRetentionDays", Number(event.target.value))} value={settings.eventRetentionDays ?? 14} />
        </Field>
        <Field label="单会话最大事件数">
          <input className="input" type="number" min={1} onChange={(event) => update("maxEventsPerThread", Number(event.target.value))} value={settings.maxEventsPerThread ?? 2000} />
        </Field>
        <Field label="单事件最大载荷字节" help="超过后事件载荷会被裁剪，避免长输出占满存储。">
          <input className="input" type="number" min={1024} step={1024} onChange={(event) => update("maxEventPayloadBytes", Number(event.target.value))} value={settings.maxEventPayloadBytes ?? 65536} />
        </Field>
        <Field label="最大并发 turn" help="默认 1；同一 workspace 仍会串行，避免并发写入同一目录。">
          <input className="input" type="number" min={1} max={4} onChange={(event) => update("maxConcurrentTurns", Number(event.target.value))} value={settings.maxConcurrentTurns ?? 1} />
        </Field>
        <Field label="只读问答 scratch workspace" help="统一会话里的只读问答会绑定该受控 workspace；留空则不能使用只读问答创建模式。">
          <select className="select" onChange={(event) => update("scratchWorkspaceId", event.target.value)} value={settings.scratchWorkspaceId || ""}>
            <option value="">未选择</option>
            {workspaces.map((workspace) => (
              <option key={workspace.id} value={workspace.id}>
                {workspace.label || workspace.pathSummary || workspace.id}
              </option>
            ))}
          </select>
        </Field>
        <div className="col-span-2 max-lg:col-span-1">
          <Button disabled={saving} tone="primary" type="submit">
            {saving ? "保存中" : "保存设置"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
