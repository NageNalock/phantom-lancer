import { useEffect, useState } from "react";
import type { CodexModel, CodexThread, CodexWorkspace } from "../../app/types";
import { Button, CheckLabel, EmptyState, Field, Notice, Pill } from "../../components/ui";
import { codexThreadStatusLabel } from "../../domain/labels";

export type CreateConversationMode = "code" | "chat";
export interface CreateThreadOptions {
  initialPrompt?: string;
  model?: string;
  sandbox?: string;
  approvalPolicy?: string;
  executionMode?: string;
}

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
  models,
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
  models: CodexModel[];
  onQuery: (value: string) => void;
  onWorkspaceFilter: (value: string) => void;
  onStatusFilter: (value: string) => void;
  onIncludeArchived: (value: boolean) => void;
  onSearch: () => void;
  onSelect: (id: string) => void;
  onCreate: (workspaceId: string, mode: CreateConversationMode, options?: CreateThreadOptions) => void;
  onTogglePin: (thread: CodexThread) => void;
  onArchive: (thread: CodexThread) => void;
  onResume: (thread: CodexThread) => void;
  onFork: (thread: CodexThread) => void;
}) {
  const filtersActive = workspaceFilter !== "all" || statusFilter !== "all" || includeArchived;
  const [newWorkspace, setNewWorkspace] = useState("");
  const [newMode, setNewMode] = useState<CreateConversationMode>("code");
  const [newPrompt, setNewPrompt] = useState("");
  const [newModel, setNewModel] = useState("");
  const [newSandbox, setNewSandbox] = useState("read-only");
  const [newApproval, setNewApproval] = useState("on-request");
  const [newExecution, setNewExecution] = useState("workspace");
  const [createOpen, setCreateOpen] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(filtersActive);
  useEffect(() => {
    if (!newWorkspace && workspaces.length) setNewWorkspace(workspaces[0].id);
  }, [workspaces, newWorkspace]);
  useEffect(() => {
    if (!loading && !threads.length) setCreateOpen(true);
  }, [loading, threads.length]);
  useEffect(() => {
    if (filtersActive) setFiltersOpen(true);
  }, [filtersActive]);

  const pinned = threads.filter((thread) => thread.pinned);
  const restGroups = groupThreadsByWorkspace(threads.filter((thread) => !thread.pinned), workspaces);
  const canCreate = newMode === "chat" ? Boolean(scratchReady) : Boolean(newWorkspace);
  const selectedWorkspace = workspaces.find((workspace) => workspace.id === newWorkspace);
  const workspaceWriteAllowed = selectedWorkspace?.trustState === "trusted";
  function create() {
    if (!canCreate) return;
    onCreate(newWorkspace, newMode, {
      initialPrompt: newPrompt.trim(),
      model: newModel.trim(),
      sandbox: newMode === "chat" ? "read-only" : newSandbox,
      approvalPolicy: newApproval,
      executionMode: newMode === "chat" ? "workspace" : newExecution,
    });
    setNewPrompt("");
  }

  return (
    <section className="panel min-w-0 overflow-hidden">
      <div className="panel-header">
        <div className="min-w-0">
          <h2 className="m-0 text-sm font-semibold">项目 / 会话</h2>
          <p className="muted mt-1 mb-0 text-xs">选择项目，创建或继续代码任务。</p>
        </div>
      </div>
      <div className="panel-body grid min-w-0 gap-3">
        <div className="codex-sidebar-create">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <div className="min-w-0">
              <strong className="block truncate text-sm">{newMode === "chat" ? "只读问答" : "代码任务"}</strong>
              <span className="muted block truncate text-xs">{selectedWorkspace?.label || selectedWorkspace?.pathSummary || "选择工作区后开始"}</span>
            </div>
            <Button className="min-h-8 px-2 text-xs" tone={createOpen ? "neutral" : "primary"} onClick={() => setCreateOpen((value) => !value)}>
              {createOpen ? "收起" : "新建"}
            </Button>
          </div>
          {createOpen ? (
            <div className="mt-3 grid gap-2">
              <div className="grid grid-cols-2 gap-2">
                <Field label="类型">
                  <select className="select" name="codex_thread_create_mode" onChange={(event) => setNewMode(event.target.value as CreateConversationMode)} value={newMode}>
                    <option value="code">代码任务</option>
                    <option disabled={!scratchReady} value="chat">只读问答</option>
                  </select>
                </Field>
                {newMode === "code" ? (
                  <Field label="执行">
                    <select className="select" name="codex_thread_execution" onChange={(event) => setNewExecution(event.target.value)} value={newExecution}>
                      <option value="workspace">工作区</option>
                      <option disabled={!workspaceWriteAllowed} value="worktree">Worktree</option>
                    </select>
                  </Field>
                ) : (
                  <div className="grid gap-2">
                    <span className="text-xs font-semibold text-[var(--muted-strong)]">沙箱</span>
                    <div className="flex h-9 items-center rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-xs text-[var(--muted-strong)]">read-only</div>
                  </div>
                )}
              </div>
              {newMode === "code" ? (
                <Field label="工作区">
                  <select className="select" disabled={!workspaces.length} name="codex_thread_workspace" onChange={(event) => setNewWorkspace(event.target.value)} value={newWorkspace}>
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
                </Field>
              ) : null}
              <Field label="初始 prompt">
                <textarea
                  autoComplete="off"
                  className="input min-h-20 resize-y py-2"
                  name="codex_thread_initial_prompt"
                  onChange={(event) => setNewPrompt(event.target.value)}
                  placeholder={newMode === "chat" ? "可选：创建后立即发送只读问题" : "可选：创建后立即发送任务 prompt"}
                  value={newPrompt}
                />
              </Field>
              <details className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1.5">
                <summary className="cursor-pointer text-xs text-[var(--muted-strong)]">模型和权限</summary>
                <div className="mt-2 grid gap-2">
                  {models.length ? (
                    <Field label="模型">
                      <select className="select" name="codex_thread_model" onChange={(event) => setNewModel(event.target.value)} value={newModel}>
                        <option value="">模型（默认）</option>
                        {models.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.displayName || item.id}
                            {item.isDefault ? "（默认）" : ""}
                          </option>
                        ))}
                      </select>
                    </Field>
                  ) : (
                    <Field label="模型">
                      <input autoComplete="off" className="input mono" name="codex_thread_model" onChange={(event) => setNewModel(event.target.value)} placeholder="模型（默认）" spellCheck={false} value={newModel} />
                    </Field>
                  )}
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="沙箱">
                      <select className="select" disabled={newMode === "chat"} name="codex_thread_sandbox" onChange={(event) => setNewSandbox(event.target.value)} value={newMode === "chat" ? "read-only" : newSandbox}>
                        <option value="read-only">只读</option>
                        <option disabled={!workspaceWriteAllowed} value="workspace-write">写入</option>
                      </select>
                    </Field>
                    <Field label="审批">
                      <select className="select" name="codex_thread_approval" onChange={(event) => setNewApproval(event.target.value)} value={newApproval}>
                        <option value="on-request">on-request</option>
                      </select>
                    </Field>
                  </div>
                </div>
              </details>
              {newMode === "code" && newExecution === "worktree" ? <p className="muted m-0 text-xs">会创建 Git worktree；完成后可在右侧状态中应用或丢弃。</p> : null}
              {newMode === "code" && (newSandbox === "workspace-write" || newExecution === "worktree") && !workspaceWriteAllowed ? <p className="muted m-0 text-xs">仅 trusted workspace 可使用写入或 Worktree。</p> : null}
              {newMode === "chat" && !scratchReady ? <p className="muted m-0 text-xs">请先在运行时设置里选择 scratch workspace。</p> : null}
              {newMode === "code" && !workspaces.length ? <Notice tone="warn">请先通过右上角“项目”登记一个 allowed roots 内的工作区。</Notice> : null}
              <Button className="justify-self-end px-4" disabled={!canCreate} tone="primary" onClick={create}>
                创建
              </Button>
            </div>
          ) : null}
        </div>
        <form
          className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            onSearch();
          }}
        >
          <Field label="搜索会话">
            <input autoComplete="off" className="input" name="codex_thread_search" onChange={(event) => onQuery(event.target.value)} placeholder="标题、路径或输出摘要" value={query} />
          </Field>
          <Button type="submit">{loading ? "搜索中" : "搜索"}</Button>
        </form>
        <details className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1.5" onToggle={(event) => setFiltersOpen(event.currentTarget.open)} open={filtersOpen}>
          <summary className="cursor-pointer text-xs text-[var(--muted-strong)]">筛选</summary>
          <div className="mt-2 grid gap-2">
            <div className="grid grid-cols-2 gap-2">
              <Field label="工作区">
                <select className="select" name="codex_thread_workspace_filter" onChange={(event) => onWorkspaceFilter(event.target.value)} value={workspaceFilter}>
                  <option value="all">全部</option>
                  {workspaces.map((workspace) => (
                    <option key={workspace.id} value={workspace.id}>
                      {workspace.label || workspace.pathSummary || workspace.id}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="状态">
                <select className="select" name="codex_thread_status_filter" onChange={(event) => onStatusFilter(event.target.value)} value={statusFilter}>
                  <option value="all">全部</option>
                  <option value="idle">Idle</option>
                  <option value="running">Running</option>
                  <option value="needs_approval">Approval</option>
                  <option value="queued">Queued</option>
                  <option value="failed">Failed</option>
                  <option value="archived">Archived</option>
                </select>
              </Field>
            </div>
            <CheckLabel
              checked={includeArchived}
              onChange={(checked) => onIncludeArchived(checked)}
              size="xs"
            >
              显示已归档会话
            </CheckLabel>
          </div>
        </details>
        {loading ? (
          <ThreadListSkeleton />
        ) : threads.length ? (
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

function ThreadListSkeleton() {
  return (
    <div aria-label="正在加载会话列表" className="grid gap-2">
      {Array.from({ length: 4 }).map((_, index) => (
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2" key={index}>
          <div className="h-3 w-3/4 animate-pulse rounded bg-[var(--line)]" />
          <div className="mt-2 h-2 w-1/2 animate-pulse rounded bg-[var(--line)]" />
          <div className="mt-2 flex gap-2">
            <span className="h-5 w-12 animate-pulse rounded bg-[var(--line)]" />
            <span className="h-5 w-16 animate-pulse rounded bg-[var(--line)]" />
          </div>
        </div>
      ))}
    </div>
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
              <select aria-label="新会话工作区" className="select" name="codex_empty_workspace" onChange={(event) => setWorkspaceId(event.target.value)} value={workspaceId}>
                {workspaces.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>
                    {workspace.label || workspace.id}
                  </option>
                ))}
              </select>
              <Button tone="primary" onClick={() => workspaceId && onCreate(workspaceId)}>
                新建
              </Button>
            </div>
          ) : (
            <p className="muted mt-4 text-sm">请先通过右上角“项目”登记一个工作区。</p>
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
