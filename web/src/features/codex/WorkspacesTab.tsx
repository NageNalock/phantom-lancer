import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { CodexWorkspace } from "../../app/types";
import { Button, EmptyState, Field, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { codexSandboxLabel, formatDate } from "../../domain/labels";

const TRUST_OPTIONS = [
  { value: "untrusted", label: "未信任（只读）" },
  { value: "trusted", label: "已信任（可写入）" },
  { value: "restricted", label: "受限（仅可读）" },
];

export function WorkspacesTab({ actions, onChange }: { actions: AppActions; onChange: () => void }) {
  const [items, setItems] = useState<CodexWorkspace[]>([]);
  const [loading, setLoading] = useState(false);
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");
  const [trust, setTrust] = useState("untrusted");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await actions.api<{ items?: CodexWorkspace[] }>("/api/codex/workspaces");
      setItems(response.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions]);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!path.trim()) {
      actions.setToast("请填写工作区路径", "warn");
      return;
    }
    try {
      await actions.api("/api/codex/workspaces", {
        method: "POST",
        csrf: actions.csrf,
        body: { path: path.trim(), label: label.trim(), trustState: trust },
      });
      setPath("");
      setLabel("");
      setTrust("untrusted");
      await load();
      onChange();
      actions.setToast("已登记工作区", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function updateTrust(workspace: CodexWorkspace, trustState: string) {
    try {
      await actions.api(`/api/codex/workspaces/${workspace.id}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: { trustState, label: workspace.label },
      });
      await load();
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function remove(workspace: CodexWorkspace) {
    try {
      await actions.api(`/api/codex/workspaces/${workspace.id}`, { method: "DELETE", csrf: actions.csrf });
      await load();
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-lg:grid-cols-1">
      <Panel actions={<Button onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>} subtitle="Codex 会话只能绑定允许根目录内的工作区。" title="Workspaces">
        {items.length ? (
          <div className="grid gap-2">
            {items.map((workspace) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={workspace.id}>
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <strong className="block truncate text-sm">{workspace.label || workspace.id}</strong>
                    <span className="mono mt-1 block truncate text-xs text-[var(--muted-strong)]">{workspace.pathSummary}</span>
                  </div>
                  <Pill tone={workspace.trustState === "trusted" ? "good" : workspace.trustState === "restricted" ? "warn" : "neutral"}>{trustLabel(workspace.trustState)}</Pill>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
                  <span>沙箱 {codexSandboxLabel(workspace.defaultSandbox)}</span>
                  {workspace.lastOpenedAt ? <span>最近 {formatDate(workspace.lastOpenedAt)}</span> : null}
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <select className="select" onChange={(event) => void updateTrust(workspace, event.target.value)} value={workspace.trustState}>
                    {TRUST_OPTIONS.map((item) => (
                      <option key={item.value} value={item.value}>
                        {item.label}
                      </option>
                    ))}
                  </select>
                  <Button tone="danger" onClick={() => void remove(workspace)}>
                    移除
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <EmptyState body={loading ? "正在加载工作区。" : "尚未登记工作区，使用右侧表单从允许根目录内添加。"} title="暂无工作区" />
        )}
      </Panel>

      <Panel subtitle="路径必须位于全局允许根目录内。" title="登记工作区">
        <form className="grid gap-3" onSubmit={create}>
          <Field label="工作区路径" help="例如 /srv/projects/my-app，需在允许根目录内。">
            <input className="input" onChange={(event) => setPath(event.target.value)} placeholder="/path/to/workspace" value={path} />
          </Field>
          <Field label="标签（可选）">
            <input className="input" onChange={(event) => setLabel(event.target.value)} placeholder="默认取目录名" value={label} />
          </Field>
          <Field label="信任状态">
            <select className="select" onChange={(event) => setTrust(event.target.value)} value={trust}>
              {TRUST_OPTIONS.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </select>
          </Field>
          <Button tone="primary" type="submit">
            登记工作区
          </Button>
        </form>
      </Panel>
    </div>
  );
}

function trustLabel(value?: string): string {
  return TRUST_OPTIONS.find((item) => item.value === value)?.label || value || "未信任";
}
