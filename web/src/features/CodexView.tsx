import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexApproval, CodexGitActionResult, CodexGitPayload, CodexItem, CodexModel, CodexModelsPayload, CodexSession, EventRecord, Workspace } from "../app/types";
import { buildConversationBlocks, SESSION_EVENT_NAMES, statusLabelValue } from "../codex/conversation";
import { RichText } from "../codex/richText";
import { Button, ContextList, EmptyState, Field, Notice, Panel, Pill } from "../components/ui";
import { eventLabel, formatDate, sandboxLabel, sessionStatusLabel, workspaceName } from "../domain/labels";
import { friendlyError } from "../api/client";

type SandboxMode = "read-only" | "workspace-write";

export function CodexView({
  actions,
  activeSession,
  activeSessionId,
  activeSessionWorkspace,
  data,
  sessionEvents,
  sessionItems,
}: {
  actions: AppActions;
  activeSession: CodexSession | null;
  activeSessionId: string;
  activeSessionWorkspace: Workspace | null;
  data: AppData;
  sessionEvents: EventRecord[];
  sessionItems: CodexItem[];
}) {
  const [workspaceId, setWorkspaceId] = useState(activeSessionWorkspace?.id || "");
  const [title, setTitle] = useState("");
  const [sandbox, setSandbox] = useState<SandboxMode>("read-only");
  const [model, setModel] = useState("");
  const [serviceTier, setServiceTier] = useState("");
  const [approvalPolicy, setApprovalPolicy] = useState("on-request");
  const [approvalsReviewer, setApprovalsReviewer] = useState("user");
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState("");
  const [models, setModels] = useState<CodexModel[]>([]);
  const [git, setGit] = useState<CodexGitPayload | null>(null);
  const [selectedGitPaths, setSelectedGitPaths] = useState<string[]>([]);
  const [commitMessage, setCommitMessage] = useState("");
  const [lastGitResult, setLastGitResult] = useState<CodexGitActionResult | null>(null);

  const selectedWorkspace = data.workspaces.find((item) => item.id === workspaceId) || null;
  const events = useMemo(() => [...sessionEvents].sort((a, b) => (a.sequence || 0) - (b.sequence || 0)), [sessionEvents]);
  const blocks = useMemo(() => buildConversationBlocks(events), [events]);
  const lastSequence = events.at(-1)?.sequence || 0;
  const canWrite = selectedWorkspace?.allowCodexWrite;
  const sessionBusy = activeSession?.status === "active" || activeSession?.status === "starting";
  const contextWorkspace = activeSession ? activeSessionWorkspace : selectedWorkspace;
  const pendingApprovals = data.pendingApprovals.filter((item) => item.sessionId === activeSession?.id);
  const usage = activeSession?.tokenUsage;
  const totalTokens = usage?.total?.totalTokens || 0;
  const contextWindow = usage?.modelContextWindow || 0;
  const contextPercent = contextWindow ? Math.min(100, Math.round((totalTokens / contextWindow) * 100)) : 0;
  const gitPaths = useMemo(() => parseGitPaths(git?.status?.output || ""), [git?.status?.output]);
  const displayedItems = useMemo(() => mergeCodexItems(sessionItems, events), [sessionItems, events]);
  const itemSummary = useMemo(() => displayedItems.slice(-8).reverse(), [displayedItems]);

  useEffect(() => {
    if (activeSessionWorkspace?.id) setWorkspaceId(activeSessionWorkspace.id);
    else if (activeSession && !activeSession.workspaceId) setWorkspaceId("");
  }, [activeSession, activeSessionWorkspace?.id]);

  useEffect(() => {
    if (!activeSession) return;
    setModel(activeSession.model || "");
    setServiceTier(activeSession.serviceTier || "");
    setApprovalPolicy(activeSession.approvalPolicy || "on-request");
    setApprovalsReviewer(activeSession.approvalsReviewer || "user");
    setSandbox((activeSession.sandbox as SandboxMode) || "read-only");
  }, [activeSession]);

  useEffect(() => {
    if (!data.codexStatus.appServerAvailable) return;
    let alive = true;
    actions
      .api<CodexModelsPayload>("/api/codex/models")
      .then((payload) => {
        if (alive) setModels(payload.data || []);
      })
      .catch(() => {
        if (alive) setModels([]);
      });
    return () => {
      alive = false;
    };
  }, [actions, data.codexStatus.appServerAvailable]);

  useEffect(() => {
    if (!canWrite && sandbox === "workspace-write") setSandbox("read-only");
  }, [canWrite, sandbox]);

  useEffect(() => {
    if (!activeSessionId) return;
    setSelectedGitPaths([]);
    setLastGitResult(null);
  }, [activeSessionId]);

  useEffect(() => {
    if (!activeSession || !activeSessionWorkspace?.id) {
      setGit(null);
      return;
    }
    let alive = true;
    actions
      .api<CodexGitPayload>(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/git`)
      .then((payload) => {
        if (alive) setGit(payload);
      })
      .catch(() => {
        if (alive) setGit(null);
      });
    return () => {
      alive = false;
    };
  }, [actions, activeSession?.id, activeSessionWorkspace?.id]);

  useEffect(() => {
    if (!activeSessionId) return;
    const source = new EventSource(`/api/events/stream?scope=codex_session&id=${encodeURIComponent(activeSessionId)}&after=0`);

    const handleMessage = (message: Event) => {
      if (!("data" in message) || typeof message.data !== "string") return;
      try {
        const next = JSON.parse(message.data) as EventRecord;
        actions.setSessionEvents((current) => mergeEvent(current, next));
        patchSessionFromEvent(actions, next);
        if (next.type === "git.action.completed") void refreshGit(true);
      } catch {
        actions.setToast("事件流解析失败", "warn");
      }
    };

    for (const name of SESSION_EVENT_NAMES) source.addEventListener(name, handleMessage);
    source.onerror = () => {
      actions.setToast("Codex 事件流暂时断开，浏览器会自动重连", "warn");
    };

    return () => {
      for (const name of SESSION_EVENT_NAMES) source.removeEventListener(name, handleMessage);
      source.close();
    };
  }, [actions, activeSessionId]);

  async function createSession() {
    setBusy("create");
    try {
      const session = await actions.api<CodexSession>("/api/codex/sessions", {
        method: "POST",
        csrf: actions.csrf,
        body: { workspaceId, title, sandbox, model, serviceTier, approvalPolicy, approvalsReviewer },
      });
      setTitle("");
      await actions.refreshSessions();
      await actions.setActiveSessionId(session.id);
      actions.setMainTab("codex");
      actions.setCodexTab("sessions");
      actions.setToast("已创建 Codex 会话", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function sendTurn() {
    const text = prompt.trim();
    if (!activeSession || !text) return;
    setBusy("turn");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/turns`, {
        method: "POST",
        csrf: actions.csrf,
        body: { prompt: text, mode: "auto" },
      });
      setPrompt("");
      actions.patchActiveSession({ status: "active", lastPrompt: text });
      await actions.refreshSessions();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function interruptTurn() {
    if (!activeSession) return;
    setBusy("interrupt");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/interrupt`, { method: "POST", csrf: actions.csrf });
      actions.setToast("已请求中断当前回合", "warn");
      await actions.refreshSessions();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function archiveSession() {
    if (!activeSession) return;
    setBusy("archive");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/archive`, { method: "POST", csrf: actions.csrf });
      actions.patchActiveSession({ archived: true, status: "archived" });
      await actions.refreshSessions();
      actions.setToast("已归档会话", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function updateSettings() {
    if (!activeSession) return;
    setBusy("settings");
    try {
      const updated = await actions.api<CodexSession>(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/settings`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: { model, serviceTier, approvalPolicy, approvalsReviewer, sandbox },
      });
      actions.patchActiveSession(updated);
      await actions.refreshSessions();
      actions.setToast("已更新会话设置", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function forkSession() {
    if (!activeSession) return;
    setBusy("fork");
    try {
      const forked = await actions.api<CodexSession>(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/fork`, { method: "POST", csrf: actions.csrf });
      await actions.refreshSessions();
      await actions.setActiveSessionId(forked.id);
      actions.setToast("已 fork 会话", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function compactSession() {
    if (!activeSession) return;
    setBusy("compact");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/compact`, { method: "POST", csrf: actions.csrf });
      actions.setToast("已请求压缩上下文", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function rollbackSession() {
    if (!activeSession) return;
    setBusy("rollback");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/rollback`, { method: "POST", csrf: actions.csrf, body: { numTurns: 1 } });
      actions.setToast("已请求回滚最近一个回合", "warn");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function startReview() {
    if (!activeSession) return;
    setBusy("review");
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/review`, { method: "POST", csrf: actions.csrf, body: { target: "uncommittedChanges" } });
      actions.setToast("已启动 review", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function resolveApproval(approval: CodexApproval, action: "allow" | "allow_session" | "deny") {
    setBusy(`approval-${approval.id}`);
    try {
      await actions.api(`/api/approvals/${encodeURIComponent(approval.id)}/resolve`, { method: "POST", csrf: actions.csrf, body: { action } });
      await actions.reloadData();
      actions.setToast(action === "deny" ? "已拒绝审批" : "已通过审批", action === "deny" ? "warn" : "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function refreshGit(silent = false) {
    if (!activeSession || !activeSessionWorkspace?.id) return;
    if (!silent) setBusy("git-refresh");
    try {
      const payload = await actions.api<CodexGitPayload>(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/git`);
      setGit(payload);
      setSelectedGitPaths((current) => current.filter((item) => parseGitPaths(payload.status?.output || "").includes(item)));
    } catch (error) {
      if (!silent) actions.setToast(friendlyError(error), "danger");
    } finally {
      if (!silent) setBusy("");
    }
  }

  async function runGitAction(action: "stage" | "unstage" | "commit") {
    if (!activeSession) return;
    if (action === "commit" && !commitMessage.trim()) {
      actions.setToast("Commit message 不能为空", "warn");
      return;
    }
    setBusy(`git-${action}`);
    try {
      const result = await actions.api<CodexGitActionResult>(`/api/codex/sessions/${encodeURIComponent(activeSession.id)}/git/actions`, {
        method: "POST",
        csrf: actions.csrf,
        body: { action, paths: selectedGitPaths, message: commitMessage },
      });
      setLastGitResult(result);
      await refreshGit(true);
      if (result.error) actions.setToast(result.error, "danger");
      else {
        if (action === "commit") setCommitMessage("");
        actions.setToast(action === "commit" ? "已创建 commit" : "Git 状态已更新", "good");
      }
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  function toggleGitPath(path: string) {
    setSelectedGitPaths((current) => (current.includes(path) ? current.filter((item) => item !== path) : [...current, path]));
  }

  return (
    <div className="grid min-h-[calc(100dvh-96px)] grid-cols-[280px_minmax(0,1fr)_340px] max-xl:grid-cols-[250px_minmax(0,1fr)] max-lg:grid-cols-1">
      <aside className="border-r border-[var(--line)] bg-[var(--surface-soft)] max-lg:border-r-0 max-lg:border-b">
        <div className="border-b border-[var(--line)] p-4">
          <div className="flex items-center justify-between gap-2">
            <strong className="text-sm">会话</strong>
            <Button onClick={() => void actions.refreshSessions()}>刷新</Button>
          </div>
        </div>
        <div className="max-h-[420px] overflow-auto p-2">
          {data.codexSessions.length ? (
            <div className="grid gap-1">
              {data.codexSessions.map((session) => {
                const active = session.id === activeSessionId;
                const workspace = data.workspaces.find((item) => item.id === session.workspaceId);
                return (
                  <button
                    className={`rounded-lg border px-3 py-2 text-left transition ${active ? "border-[rgba(207,77,16,0.34)] bg-[var(--accent-soft)]" : "border-transparent hover:border-[var(--line)] hover:bg-[var(--surface)]"}`}
                    key={session.id}
                    onClick={() => void actions.setActiveSessionId(session.id)}
                    type="button"
                  >
                    <span className="block truncate text-sm font-semibold">{session.title || "Untitled session"}</span>
                    <span className="muted mt-1 block truncate text-xs">{workspaceName(workspace)}</span>
                    <span className="mt-2 flex flex-wrap gap-1">
                      <Pill tone={session.archived ? "warn" : session.status === "active" ? "good" : "neutral"}>{sessionStatusLabel(session.status)}</Pill>
                      <Pill>{sandboxLabel(session.sandbox)}</Pill>
                      {session.model ? <Pill>{session.model}</Pill> : null}
                    </span>
                  </button>
                );
              })}
            </div>
          ) : (
            <EmptyState title="还没有会话" body="选择项目后创建一个长期 Codex 会话。" />
          )}
        </div>
        <div className="border-t border-[var(--line)] p-4">
          <div className="grid gap-3">
            <Field help="选择无项目时，Codex 不绑定工作目录，只能使用只读沙箱。" label="项目">
              <select
                className="select"
                onChange={(event) => {
                  setWorkspaceId(event.target.value);
                  actions.setSelectedWorkspaceId(event.target.value);
                }}
                value={workspaceId}
              >
                <option value="">无项目</option>
                {data.workspaces.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>
                    {workspace.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="标题">
              <input className="input" onChange={(event) => setTitle(event.target.value)} placeholder="可留空" value={title} />
            </Field>
            <Field label="沙箱">
              <select className="select" onChange={(event) => setSandbox(event.target.value as SandboxMode)} value={sandbox}>
                <option value="read-only">只读</option>
                <option disabled={!canWrite} value="workspace-write">
                  工作区可写
                </option>
              </select>
            </Field>
            <Field help="留空时使用 Codex 默认模型。" label="模型">
              <input className="input mono" list="codex-models" onChange={(event) => setModel(event.target.value)} placeholder="default" value={model} />
            </Field>
            <Field label="审批策略">
              <select className="select" onChange={(event) => setApprovalPolicy(event.target.value)} value={approvalPolicy}>
                <option value="on-request">按请求审批</option>
                <option value="untrusted">不信任模式</option>
              </select>
            </Field>
            <Field label="Reviewer">
              <select className="select" onChange={(event) => setApprovalsReviewer(event.target.value)} value={approvalsReviewer}>
                <option value="user">用户</option>
                <option value="auto_review">自动审查</option>
              </select>
            </Field>
            {!canWrite && sandbox === "workspace-write" ? <Notice>当前项目未允许 Codex 写入。</Notice> : null}
            <Button disabled={busy === "create"} onClick={() => void createSession()} tone="primary">
              新建会话
            </Button>
            <datalist id="codex-models">
              {models.map((item) => (
                <option key={item.id || item.model || item.displayName} value={item.model || item.id || ""}>
                  {item.displayName || item.id}
                </option>
              ))}
            </datalist>
          </div>
        </div>
      </aside>

      <section className="grid min-w-0 grid-rows-[auto_minmax(0,1fr)_auto]">
        <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] p-4 max-md:grid">
          <div>
            <h2 className="m-0 text-base font-semibold">{activeSession?.title || "Codex 会话"}</h2>
            <p className="muted mt-1 mb-0 text-sm">{activeSession ? activeSessionWorkspace?.rootPath || "无项目会话" : "选择或创建一个会话"}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            {activeSession ? <Pill tone={sessionBusy ? "good" : activeSession.archived ? "warn" : "neutral"}>{sessionStatusLabel(activeSession.status)}</Pill> : null}
            {activeSession ? <Pill>{sandboxLabel(activeSession.sandbox)}</Pill> : null}
            <Button disabled={!activeSession || !sessionBusy || busy === "interrupt"} onClick={() => void interruptTurn()}>
              中断
            </Button>
            <Button disabled={!activeSession || activeSession.archived || busy === "review"} onClick={() => void startReview()}>
              Review
            </Button>
            <Button disabled={!activeSession || activeSession.archived || busy === "fork"} onClick={() => void forkSession()}>
              Fork
            </Button>
            <Button disabled={!activeSession || activeSession.archived || busy === "compact"} onClick={() => void compactSession()}>
              Compact
            </Button>
            <Button disabled={!activeSession || activeSession.archived || busy === "rollback"} onClick={() => void rollbackSession()}>
              Rollback
            </Button>
            <Button disabled={!activeSession || activeSession.archived || busy === "archive"} onClick={() => void archiveSession()} tone="danger">
              归档
            </Button>
          </div>
        </div>

        <div className="min-h-[520px] overflow-auto bg-[var(--surface)] p-4">
          {activeSession ? (
            blocks.length ? (
              <div className="mx-auto grid max-w-4xl gap-3">
                {blocks.map((block, index) => (
                  <article className={messageClass(block.kind)} key={`${block.kind}-${block.id || index}`}>
                    {block.kind === "user" || block.kind === "assistant" ? (
                      <>
                        <div className="mb-2 flex items-center gap-2 text-xs">
                          <strong>{block.kind === "user" ? "You" : "Codex"}</strong>
                          <span className="muted">{formatDate(block.at)}</span>
                        </div>
                        <RichText text={block.text} />
                      </>
                    ) : (
                      <>
                        <div className="flex items-center justify-between gap-2">
                          <strong className="text-sm">{block.title}</strong>
                          <span className="muted text-xs">{formatDate(block.at)}</span>
                        </div>
                        {block.meta ? <code className="mono mt-2 block break-words rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1 text-xs">{block.meta}</code> : null}
                        {block.text ? (
                          <pre className="mono mt-2 max-h-72 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed whitespace-pre-wrap">
                            {block.text}
                          </pre>
                        ) : null}
                      </>
                    )}
                  </article>
                ))}
              </div>
            ) : (
              <EmptyState title="等待第一条消息" body="这个会话已经就绪，可以直接发送任务或追问。" />
            )
          ) : (
            <EmptyState title="没有选中的会话" body="从左侧选择会话，或创建一个无项目 / 项目会话。" />
          )}
        </div>

        <form
          className="border-t border-[var(--line)] bg-[var(--surface-soft)] p-4"
          onSubmit={(event) => {
            event.preventDefault();
            void sendTurn();
          }}
        >
          <div className="mx-auto grid max-w-4xl gap-3">
            <textarea
              className="textarea min-h-28"
              disabled={!activeSession || activeSession.archived || busy === "turn"}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="输入给 Codex 的任务或追问。"
              value={prompt}
            />
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="muted text-xs">{activeSession?.archived ? "会话已归档" : "所有请求通过后端 API 和 CSRF 校验提交。"}</span>
              <Button disabled={!activeSession || activeSession.archived || !prompt.trim() || busy === "turn"} tone="primary" type="submit">
                发送
              </Button>
            </div>
          </div>
        </form>
      </section>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-4 max-xl:col-span-2 max-xl:border-l-0 max-xl:border-t max-lg:col-span-1">
        <div className="grid gap-4">
          <Panel title="上下文">
            <ContextList
              items={[
                ["项目", workspaceName(contextWorkspace)],
                ["根目录", contextWorkspace?.rootPath || "未绑定工作目录"],
                ["Thread", activeSession?.codexThreadId || "-"],
                ["最近回合", activeSession?.lastTurnId || "-"],
                ["事件序号", lastSequence || "-"],
              ]}
            />
          </Panel>
          <Panel
            actions={
              <Button disabled={!activeSession || busy === "git-refresh"} onClick={() => void refreshGit()}>
                刷新
              </Button>
            }
            title="Git"
          >
            {!activeSessionWorkspace ? (
              <EmptyState title="无项目" body="当前会话没有绑定工作目录。" />
            ) : !git ? (
              <Notice>Git 状态暂未加载。</Notice>
            ) : git?.status?.isGit === false ? (
              <Notice>当前项目不是 Git 仓库。</Notice>
            ) : (
              <div className="grid gap-3">
                <pre className="mono max-h-32 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-2 text-xs whitespace-pre-wrap">{git?.status?.output || "Working tree clean"}</pre>
                {git?.status?.error ? <Notice tone="danger">{git.status.error}</Notice> : null}
                {gitPaths.length ? (
                  <div className="grid max-h-40 gap-1 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-2">
                    {gitPaths.map((path) => (
                      <label className="flex min-w-0 items-center gap-2 text-xs" key={path}>
                        <input checked={selectedGitPaths.includes(path)} onChange={() => toggleGitPath(path)} type="checkbox" />
                        <span className="mono truncate">{path}</span>
                      </label>
                    ))}
                  </div>
                ) : null}
                <div className="flex flex-wrap gap-2">
                  <Button disabled={!gitPaths.length} onClick={() => setSelectedGitPaths(gitPaths)}>
                    全选
                  </Button>
                  <Button disabled={!selectedGitPaths.length} onClick={() => setSelectedGitPaths([])}>
                    清空
                  </Button>
                  <Button disabled={!activeSessionWorkspace.allowCodexWrite || busy === "git-stage"} onClick={() => void runGitAction("stage")}>
                    Stage
                  </Button>
                  <Button disabled={!activeSessionWorkspace.allowCodexWrite || busy === "git-unstage"} onClick={() => void runGitAction("unstage")}>
                    Unstage
                  </Button>
                </div>
                <Field label="Commit message">
                  <input className="input" disabled={!activeSessionWorkspace.allowCodexWrite} onChange={(event) => setCommitMessage(event.target.value)} value={commitMessage} />
                </Field>
                <Button disabled={!activeSessionWorkspace.allowCodexWrite || !commitMessage.trim() || busy === "git-commit"} onClick={() => void runGitAction("commit")} tone="primary">
                  Commit staged
                </Button>
                {lastGitResult?.output ? <pre className="mono max-h-32 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-2 text-xs whitespace-pre-wrap">{lastGitResult.output}</pre> : null}
                <DiffPreview diff={git?.stagedDiff?.output} label="Staged diff" truncated={git?.stagedDiff?.truncated} />
                <DiffPreview diff={git?.diff?.output} label="Unstaged diff" truncated={git?.diff?.truncated} />
              </div>
            )}
          </Panel>
          <Panel title={`Items ${displayedItems.length}`}>
            {itemSummary.length ? (
              <div className="grid gap-2">
                {itemSummary.map((item) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2" key={item.id || item.codexItemId}>
                    <div className="flex items-start justify-between gap-2">
                      <strong className="truncate text-xs">{item.title || item.itemType || "Item"}</strong>
                      <Pill tone={item.status === "completed" ? "good" : item.status === "failed" ? "danger" : "neutral"}>{item.status || "running"}</Pill>
                    </div>
                    {item.summary ? <p className="muted mt-1 mb-0 line-clamp-2 text-xs">{item.summary}</p> : null}
                  </div>
                ))}
              </div>
            ) : (
              <EmptyState title="暂无 items" body="Codex item 会在会话运行后写入。" />
            )}
          </Panel>
          <Panel
            actions={
              <Button disabled={!activeSession || activeSession.archived || busy === "settings"} onClick={() => void updateSettings()}>
                保存
              </Button>
            }
            title="会话设置"
          >
            <div className="grid gap-3">
              <Field label="模型">
                <input className="input mono" list="codex-models" onChange={(event) => setModel(event.target.value)} placeholder="default" value={model} />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                <Field label="Service tier">
                  <input className="input mono" onChange={(event) => setServiceTier(event.target.value)} placeholder="default" value={serviceTier} />
                </Field>
                <Field label="沙箱">
                  <select className="select" onChange={(event) => setSandbox(event.target.value as SandboxMode)} value={sandbox}>
                    <option value="read-only">只读</option>
                    <option disabled={!activeSessionWorkspace?.allowCodexWrite} value="workspace-write">
                      工作区可写
                    </option>
                  </select>
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <Field label="审批">
                  <select className="select" onChange={(event) => setApprovalPolicy(event.target.value)} value={approvalPolicy}>
                    <option value="on-request">on-request</option>
                    <option value="untrusted">untrusted</option>
                  </select>
                </Field>
                <Field label="Reviewer">
                  <select className="select" onChange={(event) => setApprovalsReviewer(event.target.value)} value={approvalsReviewer}>
                    <option value="user">user</option>
                    <option value="auto_review">auto_review</option>
                  </select>
                </Field>
              </div>
            </div>
          </Panel>
          <Panel title="Context usage">
            <div className="grid gap-3">
              <div className="h-2 overflow-hidden rounded-full bg-[var(--surface-strong)]">
                <div className="h-full bg-[var(--accent)] transition-[width]" style={{ width: `${contextPercent}%` }} />
              </div>
              <ContextList
                items={[
                  ["Total", totalTokens ? totalTokens.toLocaleString() : "-"],
                  ["Window", contextWindow ? contextWindow.toLocaleString() : "-"],
                  ["Last in", usage?.last?.inputTokens?.toLocaleString() || "-"],
                  ["Last out", usage?.last?.outputTokens?.toLocaleString() || "-"],
                ]}
              />
            </div>
          </Panel>
          <Panel title={`审批 ${pendingApprovals.length}`}>
            {pendingApprovals.length ? (
              <div className="grid gap-2">
                {pendingApprovals.map((approval) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3" key={approval.id}>
                    <div className="flex items-start justify-between gap-2">
                      <strong className="text-sm">{approval.summary || approval.requestType}</strong>
                      <Pill tone={approval.riskLevel === "high" ? "danger" : "warn"}>{approval.riskLevel || "risk"}</Pill>
                    </div>
                    <p className="muted mt-1 mb-2 text-xs">{formatDate(approval.createdAt)}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button disabled={busy === `approval-${approval.id}`} onClick={() => void resolveApproval(approval, "allow")} tone="primary">
                        允许一次
                      </Button>
                      {approvalSupportsSession(approval) ? (
                        <Button disabled={busy === `approval-${approval.id}`} onClick={() => void resolveApproval(approval, "allow_session")}>
                          本会话允许
                        </Button>
                      ) : null}
                      <Button disabled={busy === `approval-${approval.id}`} onClick={() => void resolveApproval(approval, "deny")} tone="danger">
                        拒绝
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <EmptyState title="暂无审批" body="需要用户决策的命令、文件或 MCP 请求会出现在这里。" />
            )}
          </Panel>
          <Panel title="运行能力">
            <ContextList
              items={[
                ["CLI", data.codexStatus.available ? "可用" : "不可用"],
                ["App server", data.codexStatus.appServerAvailable ? "可用" : "不可用"],
                ["版本", data.codexStatus.version || "-"],
                ["路径", data.codexStatus.binaryPath || "-"],
                ["CODEX_HOME", data.codexStatus.codexHome || "-"],
              ]}
            />
          </Panel>
          <Panel title="最近事件">
            {events.length ? (
              <div className="grid gap-2">
                {events.slice(-8).reverse().map((event) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2" key={event.id || event.sequence}>
                    <strong className="block text-xs">{eventLabel(event.type)}</strong>
                    <span className="muted mt-1 block text-xs">{formatDate(event.createdAt)}</span>
                  </div>
                ))}
              </div>
            ) : (
              <EmptyState title="暂无事件" body="事件会在会话开始运行后写入。" />
            )}
          </Panel>
        </div>
      </aside>
    </div>
  );
}

function mergeEvent(current: EventRecord[], next: EventRecord): EventRecord[] {
  const exists = current.some((item) => (item.id && item.id === next.id) || (item.sequence && item.sequence === next.sequence));
  if (exists) return current;
  return [...current, next].sort((a, b) => (a.sequence || 0) - (b.sequence || 0));
}

function patchSessionFromEvent(actions: AppActions, event: EventRecord) {
  if (event.type === "thread/started" || event.type === "turn/started") actions.patchActiveSession({ status: "active" });
  if (event.type === "turn/completed") actions.patchActiveSession({ status: "idle" });
  if (event.type === "thread/archived" || event.type === "thread.archived.local") actions.patchActiveSession({ archived: true, status: "archived" });
  if (event.type === "session.failed" || event.type === "turn.start.failed" || event.type === "turn.steer.failed") actions.patchActiveSession({ status: "failed" });
  if (event.type === "thread/status/changed") actions.patchActiveSession({ status: statusLabelValue(event.payload?.status) });
  if (event.type === "thread/tokenUsage/updated" && event.payload?.tokenUsage) actions.patchActiveSession({ tokenUsage: event.payload.tokenUsage as CodexSession["tokenUsage"] });
  if (event.type === "thread.settings.updated.local") {
    actions.patchActiveSession({
      model: typeof event.payload?.model === "string" ? event.payload.model : undefined,
      serviceTier: typeof event.payload?.serviceTier === "string" ? event.payload.serviceTier : undefined,
      approvalPolicy: typeof event.payload?.approvalPolicy === "string" ? event.payload.approvalPolicy : undefined,
      approvalsReviewer: typeof event.payload?.approvalsReviewer === "string" ? event.payload.approvalsReviewer : undefined,
      sandbox: typeof event.payload?.sandbox === "string" ? event.payload.sandbox : undefined,
    });
  }
  if (event.type === "thread/settings/updated" && event.payload?.threadSettings && typeof event.payload.threadSettings === "object") {
    const settings = event.payload.threadSettings as Record<string, unknown>;
    actions.patchActiveSession({
      model: typeof settings.model === "string" ? settings.model : undefined,
      modelProvider: typeof settings.modelProvider === "string" ? settings.modelProvider : undefined,
      serviceTier: typeof settings.serviceTier === "string" ? settings.serviceTier : undefined,
      approvalPolicy: typeof settings.approvalPolicy === "string" ? settings.approvalPolicy : undefined,
      approvalsReviewer: typeof settings.approvalsReviewer === "string" ? settings.approvalsReviewer : undefined,
      cwd: typeof settings.cwd === "string" ? settings.cwd : undefined,
    });
  }
}

function messageClass(kind: string): string {
  const base = "rounded-lg border p-3";
  if (kind === "user") return `${base} ml-auto w-[min(760px,92%)] border-[rgba(207,77,16,0.18)] bg-[var(--accent-soft)]`;
  if (kind === "assistant") return `${base} mr-auto w-[min(820px,96%)] border-[var(--line)] bg-[var(--surface)]`;
  if (kind === "error") return `${base} border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] text-[var(--danger)]`;
  if (kind === "warn") return `${base} border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] text-[var(--warn)]`;
  return `${base} border-[var(--line)] bg-[var(--surface-soft)]`;
}

function parseGitPaths(output: string): string[] {
  const paths: string[] = [];
  const seen = new Set<string>();
  for (const line of output.split("\n")) {
    if (!line.trim() || line.startsWith("##") || line.length < 4) continue;
    let path = line.slice(3).trim();
    if (path.includes(" -> ")) path = path.split(" -> ").at(-1)?.trim() || path;
    path = path.replace(/^"|"$/g, "");
    if (path && !seen.has(path)) {
      seen.add(path);
      paths.push(path);
    }
  }
  return paths;
}

function mergeCodexItems(items: CodexItem[], events: EventRecord[]): CodexItem[] {
  const byKey = new Map<string, CodexItem>();
  for (const item of items) {
    const key = item.codexItemId || item.id;
    if (key) byKey.set(key, item);
  }
  for (const event of events) {
    if (!["item/started", "item/completed", "rawResponseItem/completed"].includes(event.type)) continue;
    const payloadItem = objectValue(event.payload?.item) || objectValue(event.payload?.rawResponseItem);
    if (!payloadItem) continue;
    const codexItemId = stringValue(payloadItem.id) || stringValue(event.payload?.itemId) || event.id || String(event.sequence || "");
    const existing = byKey.get(codexItemId);
    byKey.set(codexItemId, {
      ...existing,
      id: existing?.id || codexItemId,
      codexItemId,
      itemType: stringValue(payloadItem.type) || existing?.itemType || event.type,
      status: stringValue(payloadItem.status) || (event.type.includes("completed") ? "completed" : existing?.status || "running"),
      title: itemTitleFromPayload(payloadItem, event.type),
      summary: itemSummaryFromPayload(payloadItem) || existing?.summary,
      createdAt: existing?.createdAt || event.createdAt,
      updatedAt: event.createdAt || existing?.updatedAt,
    });
  }
  return [...byKey.values()].sort((a, b) => (a.createdAt || "").localeCompare(b.createdAt || ""));
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : null;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function itemTitleFromPayload(item: Record<string, unknown>, fallback: string): string {
  const type = stringValue(item.type);
  if (type === "commandExecution") return stringValue(item.command) ? `Command: ${stringValue(item.command)}` : "Command execution";
  if (type === "agentMessage") return "Assistant message";
  if (type === "userMessage") return "User message";
  if (type === "fileChange") return stringValue(item.path) ? `File change: ${stringValue(item.path)}` : "File change";
  return type || fallback;
}

function itemSummaryFromPayload(item: Record<string, unknown>): string {
  for (const key of ["text", "summary", "aggregatedOutput", "command", "path"]) {
    const value = stringValue(item[key]);
    if (value) return value;
  }
  return "";
}

function approvalSupportsSession(approval: CodexApproval): boolean {
  return ["item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval"].includes(approval.requestType || "");
}

function DiffPreview({ diff, label, truncated }: { diff?: string; label: string; truncated?: boolean }) {
  if (!diff) return null;
  return (
    <details className="rounded-md border border-[var(--line)] bg-[var(--surface)]">
      <summary className="cursor-pointer px-2 py-1 text-xs font-semibold">
        {label}
        {truncated ? " · truncated" : ""}
      </summary>
      <pre className="mono max-h-64 overflow-auto border-t border-[var(--line)] p-2 text-xs whitespace-pre-wrap">{diff}</pre>
    </details>
  );
}
