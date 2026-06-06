import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexSession, EventRecord, Workspace } from "../app/types";
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
}: {
  actions: AppActions;
  activeSession: CodexSession | null;
  activeSessionId: string;
  activeSessionWorkspace: Workspace | null;
  data: AppData;
  sessionEvents: EventRecord[];
}) {
  const [workspaceId, setWorkspaceId] = useState(activeSessionWorkspace?.id || "");
  const [title, setTitle] = useState("");
  const [sandbox, setSandbox] = useState<SandboxMode>("read-only");
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState("");

  const selectedWorkspace = data.workspaces.find((item) => item.id === workspaceId) || null;
  const events = useMemo(() => [...sessionEvents].sort((a, b) => (a.sequence || 0) - (b.sequence || 0)), [sessionEvents]);
  const blocks = useMemo(() => buildConversationBlocks(events), [events]);
  const lastSequence = events.at(-1)?.sequence || 0;
  const canWrite = selectedWorkspace?.allowCodexWrite;
  const sessionBusy = activeSession?.status === "active" || activeSession?.status === "starting";
  const contextWorkspace = activeSession ? activeSessionWorkspace : selectedWorkspace;

  useEffect(() => {
    if (activeSessionWorkspace?.id) setWorkspaceId(activeSessionWorkspace.id);
    else if (activeSession && !activeSession.workspaceId) setWorkspaceId("");
  }, [activeSession, activeSessionWorkspace?.id]);

  useEffect(() => {
    if (!canWrite && sandbox === "workspace-write") setSandbox("read-only");
  }, [canWrite, sandbox]);

  useEffect(() => {
    if (!activeSessionId) return;
    const source = new EventSource(`/api/events/stream?scope=codex_session&id=${encodeURIComponent(activeSessionId)}&after=0`);

    const handleMessage = (message: Event) => {
      if (!("data" in message) || typeof message.data !== "string") return;
      try {
        const next = JSON.parse(message.data) as EventRecord;
        actions.setSessionEvents((current) => mergeEvent(current, next));
        patchSessionFromEvent(actions, next);
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
        body: { workspaceId, title, sandbox },
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

  return (
    <div className="grid min-h-[calc(100dvh-96px)] grid-cols-[280px_minmax(0,1fr)_300px] max-xl:grid-cols-[250px_minmax(0,1fr)] max-lg:grid-cols-1">
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
            {!canWrite && sandbox === "workspace-write" ? <Notice>当前项目未允许 Codex 写入。</Notice> : null}
            <Button disabled={busy === "create"} onClick={() => void createSession()} tone="primary">
              新建会话
            </Button>
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
}

function messageClass(kind: string): string {
  const base = "rounded-lg border p-3";
  if (kind === "user") return `${base} ml-auto w-[min(760px,92%)] border-[rgba(207,77,16,0.18)] bg-[var(--accent-soft)]`;
  if (kind === "assistant") return `${base} mr-auto w-[min(820px,96%)] border-[var(--line)] bg-[var(--surface)]`;
  if (kind === "error") return `${base} border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] text-[var(--danger)]`;
  if (kind === "warn") return `${base} border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] text-[var(--warn)]`;
  return `${base} border-[var(--line)] bg-[var(--surface-soft)]`;
}
