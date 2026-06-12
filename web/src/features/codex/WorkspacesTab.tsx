import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { ApiError, CodexWorkspace } from "../../app/types";
import { Button, EmptyState, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
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
  const [defaultModel, setDefaultModel] = useState("");
  const [defaultSandbox, setDefaultSandbox] = useState("read-only");
  const [defaultApproval, setDefaultApproval] = useState("on-request");
  const [networkEnabled, setNetworkEnabled] = useState(false);
  const [missingPath, setMissingPath] = useState("");
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

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
    await submitCreate(false);
  }

  async function submitCreate(createIfMissing: boolean) {
    if (!path.trim()) {
      actions.setToast("请填写工作区路径", "warn");
      return;
    }
    const workspacePath = path.trim();
    try {
      await actions.api("/api/codex/workspaces", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          path: workspacePath,
          label: label.trim(),
          trustState: trust,
          defaultModel: defaultModel.trim(),
          defaultSandbox,
          defaultApprovalPolicy: defaultApproval,
          networkPolicy: { enabled: networkEnabled },
          createIfMissing,
        },
      });
      setPath("");
      setLabel("");
      setTrust("untrusted");
      setDefaultModel("");
      setDefaultSandbox("read-only");
      setDefaultApproval("on-request");
      setNetworkEnabled(false);
      setMissingPath("");
      await load();
      onChange();
      actions.setToast(createIfMissing ? "已创建目录并登记工作区" : "已登记工作区", "good");
    } catch (error) {
      if ((error as ApiError).code === "workspace_path_missing" && !createIfMissing) {
        setMissingPath(workspacePath);
        actions.setToast("工作区路径不存在，请确认后创建目录。", "warn");
        return;
      }
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function updateWorkspace(workspace: CodexWorkspace, patch: Partial<CodexWorkspace>) {
    const next = { ...workspace, ...patch };
    try {
      await actions.api(`/api/codex/workspaces/${workspace.id}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: {
          label: next.label,
          trustState: next.trustState,
          defaultModel: next.defaultModel,
          defaultSandbox: next.defaultSandbox,
          defaultApprovalPolicy: next.defaultApprovalPolicy,
          networkPolicy: next.networkPolicy,
          pinned: next.pinned,
        },
      });
      await load();
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function remove(workspace: CodexWorkspace) {
    const confirmed = await confirmDanger({
      title: "移除 Codex 工作区",
      objectName: workspace.label || workspace.pathSummary || workspace.id,
      body: "该操作会从 Codex 模块移除工作区登记，不会删除磁盘目录。",
      confirmLabel: "移除工作区",
      impact: ["已存在会话可能失去清晰的工作区上下文。", "磁盘文件、Git 仓库和审计记录不会被删除。"],
      recovery: "如需恢复，可重新登记同一路径。",
    });
    if (!confirmed) return;
    try {
      await actions.api(`/api/codex/workspaces/${workspace.id}`, { method: "DELETE", csrf: actions.csrf });
      await load();
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <>
    <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-lg:grid-cols-1">
      <Panel actions={<Button onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>} subtitle="Codex 会话只能绑定允许根目录内的工作区。" title="工作区">
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
                  <span>审批 {workspace.defaultApprovalPolicy || "on-request"}</span>
                  {workspace.gitBranch ? <span className="mono">Git {workspace.gitBranch}</span> : null}
                  {workspace.defaultModel ? <span className="mono">模型 {workspace.defaultModel}</span> : null}
                  {workspace.networkPolicy?.enabled ? <span>网络 enabled</span> : null}
                  {workspace.pinned ? <span>已置顶</span> : null}
                  {workspace.lastOpenedAt ? <span>最近 {formatDate(workspace.lastOpenedAt)}</span> : null}
                </div>
                <div className="mt-2 grid grid-cols-2 gap-2 max-md:grid-cols-1">
                  <select className="select" onChange={(event) => void updateWorkspace(workspace, { trustState: event.target.value })} value={workspace.trustState}>
                    {TRUST_OPTIONS.map((item) => (
                      <option key={item.value} value={item.value}>
                        {item.label}
                      </option>
                    ))}
                  </select>
                  <input className="input" onBlur={(event) => void updateWorkspace(workspace, { defaultModel: event.target.value.trim() })} placeholder="默认模型" defaultValue={workspace.defaultModel || ""} />
                  <select className="select" onChange={(event) => void updateWorkspace(workspace, { defaultSandbox: event.target.value })} value={workspace.defaultSandbox || "read-only"}>
                    <option value="read-only">read-only</option>
                    <option value="workspace-write">workspace-write</option>
                  </select>
                  <select className="select" onChange={(event) => void updateWorkspace(workspace, { defaultApprovalPolicy: event.target.value })} value={workspace.defaultApprovalPolicy || "on-request"}>
                    <option value="on-request">on-request</option>
                  </select>
                  <select className="select" onChange={(event) => void updateWorkspace(workspace, { networkPolicy: { ...(workspace.networkPolicy || {}), enabled: event.target.value === "true" } })} value={String(Boolean(workspace.networkPolicy?.enabled))}>
                    <option value="false">网络关闭</option>
                    <option value="true">网络启用</option>
                  </select>
                  <Button onClick={() => void updateWorkspace(workspace, { pinned: !workspace.pinned })}>{workspace.pinned ? "取消置顶" : "置顶"}</Button>
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
            <input
              className="input"
              onChange={(event) => {
                setPath(event.target.value);
                setMissingPath("");
              }}
              placeholder="/path/to/workspace"
              value={path}
            />
          </Field>
          {missingPath ? (
            <Notice tone="warn">
              <div className="grid gap-2">
                <div>
                  <strong className="block text-sm">工作区路径不存在</strong>
                  <span className="mono mt-1 block break-all text-xs">{missingPath}</span>
                </div>
                <p className="m-0 text-xs">确认后会在允许根目录内创建该目录，然后登记为 Codex 工作区。</p>
                <div className="flex flex-wrap gap-2">
                  <Button tone="primary" onClick={() => void submitCreate(true)} type="button">
                    创建目录并登记
                  </Button>
                  <Button onClick={() => setMissingPath("")} type="button">取消</Button>
                </div>
              </div>
            </Notice>
          ) : null}
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
          <Field label="默认模型">
            <input className="input" onChange={(event) => setDefaultModel(event.target.value)} placeholder="运行时默认" value={defaultModel} />
          </Field>
          <Field label="默认沙箱">
            <select className="select" onChange={(event) => setDefaultSandbox(event.target.value)} value={defaultSandbox}>
              <option value="read-only">read-only</option>
              <option value="workspace-write">workspace-write</option>
            </select>
          </Field>
          <Field label="默认审批策略">
            <select className="select" onChange={(event) => setDefaultApproval(event.target.value)} value={defaultApproval}>
              <option value="on-request">on-request</option>
            </select>
          </Field>
          <Field label="网络策略">
            <select className="select" onChange={(event) => setNetworkEnabled(event.target.value === "true")} value={String(networkEnabled)}>
              <option value="false">默认关闭</option>
              <option value="true">显式启用</option>
            </select>
          </Field>
          <Button tone="primary" type="submit">
            登记工作区
          </Button>
        </form>
      </Panel>
    </div>
    {dangerConfirmDialog}
    </>
  );
}

function trustLabel(value?: string): string {
  return TRUST_OPTIONS.find((item) => item.value === value)?.label || value || "未信任";
}
