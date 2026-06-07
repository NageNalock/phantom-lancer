import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { CodexCapabilitiesPayload, CodexModel } from "../app/types";
import { Button, ContextList, EmptyState, Panel, Pill } from "../components/ui";
import { friendlyError } from "../api/client";

export function CodexCapabilitiesView({ actions }: { actions: AppActions }) {
  const [payload, setPayload] = useState<CodexCapabilitiesPayload>({});
  const [busy, setBusy] = useState(false);

  async function load() {
    setBusy(true);
    try {
      setPayload(await actions.api<CodexCapabilitiesPayload>("/api/codex/capabilities"));
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  const sections = payload.sections || {};
  const errors = payload.errors || {};
  const models = useMemo(() => modelItems(sections.models), [sections.models]);
  const mcp = listItems(sections.mcp);
  const skills = listItems(sections.skills);
  const hooks = listItems(sections.hooks);
  const plugins = pluginCount(sections.plugins);

  return (
    <div className="grid min-h-[calc(100dvh-104px)] content-start gap-4 p-5">
      <Panel
        actions={<Button disabled={busy} onClick={() => void load()}>刷新</Button>}
        subtitle="只读取 Codex app-server 暴露的客户端能力；配置写入、OAuth 和安装类操作后续需要独立确认。"
        title="Capabilities"
      >
        <div className="grid grid-cols-4 gap-3 max-xl:grid-cols-2 max-sm:grid-cols-1">
          <CapabilityMetric error={errors.models} label="Models" value={models.length} />
          <CapabilityMetric error={errors.mcp} label="MCP servers" value={mcp.length} />
          <CapabilityMetric error={errors.skills} label="Skills" value={skills.length} />
          <CapabilityMetric error={errors.plugins} label="Plugins" value={plugins} />
        </div>
      </Panel>

      <div className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)] gap-4 max-xl:grid-cols-1">
        <Panel title="Models">
          {models.length ? (
            <div className="grid gap-2">
              {models.slice(0, 18).map((model) => (
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={model.id || model.model || model.displayName}>
                  <div className="min-w-0">
                    <strong className="block truncate text-sm">{model.displayName || model.model || model.id}</strong>
                    <p className="muted mt-1 mb-0 truncate text-xs">{model.description || model.model || model.id}</p>
                  </div>
                  <div className="flex flex-wrap justify-end gap-1">
                    {model.isDefault ? <Pill tone="good">default</Pill> : null}
                    {model.hidden ? <Pill tone="warn">hidden</Pill> : null}
                    {model.defaultReasoningEffort ? <Pill>{model.defaultReasoningEffort}</Pill> : null}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无模型数据" body={errors.models || "Codex app-server 未返回 model/list 数据。"} />
          )}
        </Panel>

        <div className="grid content-start gap-4">
          <Panel title="Account / Rate limits">
            <ContextList
              items={[
                ["Account", errors.account ? "读取失败" : sectionState(sections.account)],
                ["Rate limit", errors.rateLimits ? "读取失败" : sectionState(sections.rateLimits)],
                ["Config", errors.config ? "读取失败" : sectionState(sections.config)],
              ]}
            />
          </Panel>
          <Panel title="MCP / Skills / Hooks">
            <ContextList
              items={[
                ["MCP", errors.mcp || `${mcp.length} servers`],
                ["Skills", errors.skills || `${skills.length} skills`],
                ["Hooks", errors.hooks || `${hooks.length} hooks`],
                ["Plugins", errors.plugins || `${plugins} marketplaces`],
              ]}
            />
          </Panel>
        </div>
      </div>
    </div>
  );
}

function CapabilityMetric({ label, value, error }: { label: string; value: number; error?: string }) {
  return (
    <div className={`rounded-lg border p-3 ${error ? "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]" : "border-[var(--line)] bg-[var(--surface-soft)]"}`}>
      <span className="muted text-xs">{label}</span>
      <strong className="mt-2 block text-xl">{error ? "error" : value}</strong>
      {error ? <small className="mt-1 block break-words text-xs text-[var(--warn)]">{error}</small> : null}
    </div>
  );
}

function modelItems(value: unknown): CodexModel[] {
  if (!value || typeof value !== "object") return [];
  const data = (value as { data?: CodexModel[] }).data;
  return Array.isArray(data) ? data : [];
}

function listItems(value: unknown): unknown[] {
  if (!value || typeof value !== "object") return [];
  const data = (value as { data?: unknown[] }).data;
  return Array.isArray(data) ? data : [];
}

function pluginCount(value: unknown): number {
  if (!value || typeof value !== "object") return 0;
  const marketplaces = (value as { marketplaces?: unknown[] }).marketplaces;
  return Array.isArray(marketplaces) ? marketplaces.length : 0;
}

function sectionState(value: unknown): string {
  if (!value) return "无数据";
  if (typeof value === "object") return "已读取";
  return String(value);
}
