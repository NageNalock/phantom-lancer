import { useEffect, useState } from "react";
import type { CodexThread, CodexWorkspace } from "../../app/types";
import { Button, CheckLabel, EmptyState, Pill } from "../../components/ui";
import { codexThreadStatusLabel } from "../../domain/labels";

export type CreateConversationMode = "code" | "chat";

export function ThreadList({
  loading,
  threads,
  workspaces,
  activeId,
  query,
  workspaceFilter,
  statusFilter,
  includeArchived,
  scratchReady,
  onQuery,
  onWorkspaceFilter,
  onStatusFilter,
  onIncludeArchived,
  onSearch,
  onSelect,
  onCreate,
  onTogglePin,
  onArchive,
  onResume,
  onFork,
}: {
  loading: boolean;
  threads: CodexThread[];
  workspaces: CodexWorkspace[];
  activeId: string;
  query: string;
  workspaceFilter: string;
  statusFilter: string;
  includeArchived: boolean;
  scratchReady?: boolean;
  onQuery: (value: string) => void;
  onWorkspaceFilter: (value: string) => void;
  onStatusFilter: (value: string) => void;
  onIncludeArchived: (value: boolean) => void;
  onSearch: () => void;
  onSelect: (id: string) => void;
  onCreate: (workspaceId: string, mode: CreateConversationMode) => void;
  onTogglePin: (thread: CodexThread) => void;
  onArchive: (thread: CodexThread) => void;
  onResume: (thread: CodexThread) => void;
  onFork: (thread: CodexThread) => void;
}) {
  const [newWorkspace, setNewWorkspace] = useState("");
  const [newMode, setNewMode] = useState<CreateConversationMode>("code");
  useEffect(() => {
    if (!newWorkspace && workspaces.length) setNewWorkspace(workspaces[0].id);
  }, [workspaces, newWorkspace]);

  const pinned = threads.filter((thread) => thread.pinned);
  const restGroups = groupThreadsByWorkspace(threads.filter((thread) => !thread.pinned), workspaces);
  const canCreate = newMode === "chat" ? Boolean(scratchReady) : Boolean(newWorkspace);

  return (
    <section className="panel min-w-0 overflow-hidden">
      <div className="panel-header">
        <h2 className="m-0 text-sm font-semibold">会话</h2>
      </div>
      <div className="panel-body grid min-w-0 gap-3">
        <div className="grid gap-2">
          <select className="select" onChange={(event) => setNewMode(event.target.value as CreateConversationMode)} value={newMode}>
            <option value="code">代码任务</option>
            <option disabled={!scratchReady} value="chat">只读问答</option>
          </select>
          <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
            {newMode === "code" ? (
              <select className="select" disabled={!workspaces.length} onChange={(event) => setNewWorkspace(event.target.value)} value={newWorkspace}>
                {workspaces.length ? (
                  workspaces.map((workspace) => (
                    <option key={workspace.id} value={workspace.id}>
                      {workspace.label || workspace.id}
                    </option>
                  ))
                ) : (
                  <option value="">先登记工作区</option>
                )}
              </select>
            ) : (
              <div className="flex min-h-9 items-center rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-xs text-[var(--muted-strong)]">
                使用 scratch workspace
              </div>
            )}
            <Button disabled={!canCreate} tone="primary" onClick={() => onCreate(newWorkspace, newMode)}>
              新对话
            </Button>
          </div>
          {newMode === "chat" && !scratchReady ? <p className="muted m-0 text-xs">请先在 Settings 里选择 scratch workspace。</p> : null}
        </div>
        <form
          className="grid grid-cols-[minmax(0,1fr)_auto] gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            onSearch();
          }}
        >
          <input className="input" onChange={(event) => onQuery(event.target.value)} placeholder="搜索会话" value={query} />
          <Button type="submit">{loading ? "搜索中" : "搜索"}</Button>
        </form>
        <div className="grid grid-cols-2 gap-2">
          <select className="select" onChange={(event) => onWorkspaceFilter(event.target.value)} value={workspaceFilter}>
            <option value="all">全部工作区</option>
            {workspaces.map((workspace) => (
              <option key={workspace.id} value={workspace.id}>
                {workspace.label || workspace.pathSummary || workspace.id}
              </option>
            ))}
          </select>
          <select className="select" onChange={(event) => onStatusFilter(event.target.value)} value={statusFilter}>
            <option value="all">全部状态</option>
            <option value="idle">Idle</option>
            <option value="running">Running</option>
            <option value="needs_approval">Approval</option>
            <option value="queued">Queued</option>
            <option value="failed">Failed</option>
            <option value="archived">Archived</option>
          </select>
        </div>
        <CheckLabel
          checked={includeArchived}
          onChange={(checked) => onIncludeArchived(checked)}
          size="xs"
        >
          显示已归档会话
        </CheckLabel>
        {threads.length ? (
          <div className="grid min-w-0 gap-1">
            {pinned.length ? <span className="muted px-1 text-xs">置顶</span> : null}
            {pinned.map((thread) => (
              <ThreadRow key={thread.id} active={thread.id === activeId} thread={thread} workspaces={workspaces} onSelect={onSelect} onTogglePin={onTogglePin} onArchive={onArchive} onResume={onResume} onFork={onFork} />
            ))}
            {restGroups.map((group) => (
              <div className="grid min-w-0 gap-1" key={group.id}>
                <span className="muted mt-1 px-1 text-xs">{group.label}</span>
                {group.threads.map((thread) => (
                  <ThreadRow key={thread.id} active={thread.id === activeId} thread={thread} workspaces={workspaces} onSelect={onSelect} onTogglePin={onTogglePin} onArchive={onArchive} onResume={onResume} onFork={onFork} />
                ))}
              </div>
            ))}
          </div>
        ) : (
          <EmptyState body={loading ? "正在加载会话。" : "暂无会话，可创建代码任务或只读问答。"} title="暂无会话" />
        )}
      </div>
    </section>
  );
}

export function ComposerEmptyState({ workspaces, onCreate }: { workspaces: CodexWorkspace[]; onCreate: (workspaceId: string) => void }) {
  const [workspaceId, setWorkspaceId] = useState("");
  useEffect(() => {
    if (!workspaceId && workspaces.length) setWorkspaceId(workspaces[0].id);
  }, [workspaces, workspaceId]);
  return (
    <section className="panel">
      <div className="panel-body grid place-items-center py-16">
        <div className="w-full max-w-md text-center">
          <strong className="block text-base">开始一个 Codex 会话</strong>
          <p className="muted mt-1 text-sm">选择工作区后创建会话，使用本机 codex CLI 在受控边界内执行。</p>
          {workspaces.length ? (
            <div className="mt-4 grid grid-cols-[minmax(0,1fr)_auto] gap-2">
              <select className="select" onChange={(event) => setWorkspaceId(event.target.value)} value={workspaceId}>
                {workspaces.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>
                    {workspace.label || workspace.id}
                  </option>
                ))}
              </select>
              <Button tone="primary" onClick={() => workspaceId && onCreate(workspaceId)}>
                新对话
              </Button>
            </div>
          ) : (
            <p className="muted mt-4 text-sm">请先在 Workspaces 标签登记一个工作区。</p>
          )}
        </div>
      </div>
    </section>
  );
}

function ThreadRow({
  active,
  thread,
  workspaces,
  onSelect,
  onTogglePin,
  onArchive,
  onResume,
  onFork,
}: {
  active: boolean;
  thread: CodexThread;
  workspaces: CodexWorkspace[];
  onSelect: (id: string) => void;
  onTogglePin: (thread: CodexThread) => void;
  onArchive: (thread: CodexThread) => void;
  onResume: (thread: CodexThread) => void;
  onFork: (thread: CodexThread) => void;
}) {
  const workspace = workspaces.find((item) => item.id === thread.workspaceId);
  const archived = thread.status === "archived" || Boolean(thread.archivedAt);
  const kindLabel = thread.kind === "chat" ? "只读问答" : "代码任务";
  return (
    <div className={`min-w-0 overflow-hidden rounded-lg border px-2 py-2 text-left transition ${active ? "border-[var(--line-strong)] bg-[var(--surface-strong)]" : "border-transparent hover:bg-[var(--surface-soft)]"}`}>
      <button className="block w-full min-w-0 text-left" onClick={() => onSelect(thread.id)} type="button">
        <div className="flex min-w-0 items-start justify-between gap-2">
          <strong className="min-w-0 flex-1 truncate text-sm">{thread.title || "新对话"}</strong>
          <span className="flex max-w-[52%] shrink-0 flex-wrap items-center justify-end gap-1 overflow-hidden">
            {thread.kind === "chat" ? <Pill tone="neutral">只读</Pill> : null}
            {!active && thread.background ? <span className="rounded border border-[var(--line)] px-1.5 py-0.5 text-[10px] text-[var(--muted-strong)]">后台</span> : null}
            <Pill tone={threadTone(thread.status)}>{codexThreadStatusLabel(thread.status)}</Pill>
          </span>
        </div>
        <span className="muted mt-1 block truncate text-xs">{workspace?.label || workspace?.pathSummary || "工作区"} / {kindLabel}</span>
      </button>
      <div className="mt-1 flex min-w-0 flex-wrap items-center gap-2 overflow-hidden text-xs">
        {archived ? (
          <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onResume(thread)} type="button">
            恢复
          </button>
        ) : (
          <>
            <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onTogglePin(thread)} type="button">
              {thread.pinned ? "取消置顶" : "置顶"}
            </button>
            <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onFork(thread)} type="button">
              复制
            </button>
            <button className="text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => onArchive(thread)} type="button">
              归档
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function groupThreadsByWorkspace(threads: CodexThread[], workspaces: CodexWorkspace[]) {
  const labels = new Map(workspaces.map((workspace) => [workspace.id, workspace.label || workspace.pathSummary || workspace.id]));
  const groups = new Map<string, { id: string; label: string; threads: CodexThread[] }>();
  for (const thread of threads) {
    const id = thread.workspaceId || "unknown";
    const label = labels.get(id) || "未关联工作区";
    const group = groups.get(id) || { id, label, threads: [] };
    group.threads.push(thread);
    groups.set(id, group);
  }
  return Array.from(groups.values());
}

function threadTone(status?: string) {
  if (status === "running") return "good" as const;
  if (status === "needs_approval" || status === "queued") return "warn" as const;
  if (status === "failed") return "danger" as const;
  return "neutral" as const;
}
