import { FormEvent, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, Workspace } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, EmptyState, Field, Metric, Panel, Pill } from "../components/ui";
import { profileLabel } from "../domain/labels";

export function ProjectsView({ actions, data, selectedWorkspaceId }: { actions: AppActions; data: AppData; selectedWorkspaceId: string }) {
  const selected = useMemo(() => data.workspaces.find((item) => item.id === selectedWorkspaceId) || data.workspaces[0], [data.workspaces, selectedWorkspaceId]);

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[328px_minmax(0,1fr)_332px] max-2xl:grid-cols-[292px_minmax(0,1fr)] max-xl:grid-cols-1">
      <aside className="border-r border-[var(--line)] bg-[var(--surface-soft)] p-3 max-xl:border-r-0 max-xl:border-b">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <strong className="block text-sm">项目</strong>
            <span className="muted text-xs">{data.workspaces.length} 个工作区</span>
          </div>
        </div>
        <div className="grid gap-2">
          {data.workspaces.map((workspace) => (
            <WorkspaceButton active={workspace.id === selected?.id} key={workspace.id} onClick={() => actions.setSelectedWorkspaceId(workspace.id)} workspace={workspace} />
          ))}
          {!data.workspaces.length ? <EmptyState body="添加项目后，Codex、日志和审计都会以项目为上下文。" title="等待项目" /> : null}
        </div>
      </aside>

      <div className="grid content-start gap-4 p-5">
        <Panel subtitle="项目是 Codex、日志、服务和审计的主上下文。" title={selected ? selected.name : "项目"}>
          {selected ? (
            <div className="grid gap-4">
              <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
                <Metric label="默认 Profile" value={profileLabel(selected.defaultProfile)} />
                <Metric detail={selected.allowCodexWrite ? "workspace-write 可用" : "默认只读"} label="Codex 写入" tone={selected.allowCodexWrite ? "warn" : "good"} value={selected.allowCodexWrite ? "允许" : "关闭"} />
                <Metric label="目录类型" value={selected.appType || "app"} />
              </div>
              <ContextList
                items={[
                  ["工作目录", <span className="mono">{selected.rootPath}</span>],
                  ["非 Git", selected.allowNonGit ? "允许" : "关闭"],
                  ["描述", selected.description || "-"],
                ]}
              />
            </div>
          ) : (
            <EmptyState body="项目是后续服务器能力的资源边界。" title="等待项目" />
          )}
        </Panel>
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-2xl:col-span-2 max-2xl:border-l-0 max-2xl:border-t max-xl:col-span-1">
        <WorkspaceForm actions={actions} />
      </aside>
    </div>
  );
}

function WorkspaceButton({ active, workspace, onClick }: { active: boolean; workspace: Workspace; onClick: () => void }) {
  return (
    <button className={`rounded-lg border p-3 text-left ${active ? "border-[var(--line)] bg-[var(--surface)] shadow-[inset_2px_0_0_var(--accent)]" : "border-transparent hover:bg-[var(--surface-strong)]"}`} onClick={onClick} type="button">
      <strong className="block text-sm">{workspace.name}</strong>
      <span className="muted mono mt-1 block break-all text-xs">{workspace.rootPath}</span>
      <span className="mt-2 flex gap-2">
        <Pill tone={workspace.allowCodexWrite ? "warn" : "good"}>{workspace.allowCodexWrite ? "允许写入" : "默认只读"}</Pill>
      </span>
    </button>
  );
}

function WorkspaceForm({ actions }: { actions: AppActions }) {
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setSubmitting(true);
    setError("");
    try {
      const workspace = await actions.api<Workspace>("/api/workspaces", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          name: data.get("name"),
          rootPath: data.get("rootPath"),
          appType: data.get("appType"),
          defaultProfile: data.get("defaultProfile") || "Observe",
          allowCodexWrite: data.get("allowCodexWrite") === "on",
          allowNonGit: data.get("allowNonGit") === "on",
        },
      });
      actions.setSelectedWorkspaceId(workspace.id);
      await actions.reloadData();
      form.reset();
      actions.setToast("项目已添加", "good");
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Panel subtitle="路径必须落在允许根目录内。" title="添加项目">
      <form className="grid gap-3" onSubmit={submit}>
        {error ? <div className="rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-3 text-sm text-[var(--danger)]">{error}</div> : null}
        <Field label="名称">
          <input className="input" name="name" placeholder="phantom-lancer" />
        </Field>
        <Field label="工作目录">
          <input className="input mono" name="rootPath" placeholder="/srv/apps/my-app" required />
        </Field>
        <Field label="类型">
          <input className="input" name="appType" placeholder="web" />
        </Field>
        <Field label="默认 Profile">
          <select className="select" name="defaultProfile" defaultValue="Observe">
            {["Observe", "Maintain", "Deploy", "Admin"].map((item) => (
              <option key={item} value={item}>{profileLabel(item)}</option>
            ))}
          </select>
        </Field>
        <label className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-sm"><input name="allowCodexWrite" type="checkbox" /> 允许 Codex workspace-write</label>
        <label className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-sm"><input name="allowNonGit" type="checkbox" /> 允许非 Git 目录</label>
        <Button disabled={submitting} tone="primary" type="submit">{submitting ? "处理中" : "添加项目"}</Button>
      </form>
    </Panel>
  );
}
