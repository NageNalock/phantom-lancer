import { FormEvent, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, Workspace } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, EmptyState, Field, Metric, Panel, Pill } from "../components/ui";

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
              <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                <Metric detail={selected.allowCodexWrite ? "workspace-write 可用" : "默认只读"} label="Codex 写入" tone={selected.allowCodexWrite ? "warn" : "good"} value={selected.allowCodexWrite ? "允许" : "关闭"} />
                <Metric detail={selected.allowNonGit ? "可添加非 Git 目录" : "添加时要求 .git"} label="Git 边界" tone={selected.allowNonGit ? "warn" : "good"} value={selected.allowNonGit ? "宽松" : "严格"} />
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
        <WorkspaceForm actions={actions} allowedRoots={data.settings.runtime?.allowedRoots || []} />
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

function WorkspaceForm({ actions, allowedRoots }: { actions: AppActions; allowedRoots: string[] }) {
  const [error, setError] = useState("");
  const [rootPath, setRootPath] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const rootHint = pathBoundaryHint(rootPath, allowedRoots);

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
          allowCodexWrite: data.get("allowCodexWrite") === "on",
          allowNonGit: data.get("allowNonGit") === "on",
          createMissing: data.get("createMissing") === "on",
        },
      });
      actions.setSelectedWorkspaceId(workspace.id);
      await actions.reloadData();
      form.reset();
      setRootPath("");
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
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
          <span className="muted block text-xs">允许根目录</span>
          <div className="mt-2 grid gap-1">
            {allowedRoots.length ? (
              allowedRoots.map((root) => (
                <code className="mono block break-all rounded-md bg-[var(--surface-soft)] px-2 py-1 text-xs" key={root}>
                  {root}
                </code>
              ))
            ) : (
              <span className="muted text-xs">尚未配置允许根目录。</span>
            )}
          </div>
        </div>
        <Field label="名称">
          <input className="input" name="name" placeholder="phantom-lancer" />
        </Field>
        <Field help={rootHint} label="工作目录">
          <input
            className="input mono"
            name="rootPath"
            onChange={(event) => setRootPath(event.target.value)}
            placeholder={allowedRoots[0] ? `${allowedRoots[0]}/my-app` : "/srv/apps/my-app"}
            required
            value={rootPath}
          />
        </Field>
        <label className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-sm"><input name="createMissing" type="checkbox" /> 目录不存在时创建</label>
        <label className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-sm"><input name="allowCodexWrite" type="checkbox" /> 允许 Codex workspace-write</label>
        <label className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-sm"><input name="allowNonGit" type="checkbox" /> 允许非 Git 目录</label>
        <Button disabled={submitting} tone="primary" type="submit">{submitting ? "处理中" : "添加项目"}</Button>
      </form>
    </Panel>
  );
}

function pathBoundaryHint(value: string, allowedRoots: string[]): string {
  if (!allowedRoots.length) return "请先在设置页配置允许根目录。";
  const trimmed = value.trim();
  if (!trimmed) return `必须等于允许根目录，或以 ${allowedRoots[0]}/... 开头。`;
  const matched = allowedRoots.find((root) => isInsideRoot(root, trimmed));
  if (matched) return `已匹配允许根目录：${matched}`;
  return `路径必须以允许根目录开头，例如 ${allowedRoots[0]}/my-app。`;
}

function isInsideRoot(root: string, value: string): boolean {
  const normalizedRoot = root.replace(/\/+$/, "");
  const normalizedValue = value.replace(/\/+$/, "");
  return normalizedValue === normalizedRoot || normalizedValue.startsWith(`${normalizedRoot}/`);
}
