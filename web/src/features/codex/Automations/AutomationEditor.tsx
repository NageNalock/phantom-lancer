import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { CodexAutomation, CodexThread, CodexWorkspace } from "../../../app/types";
import { Button, Field, Panel } from "../../../components/ui";

type ScheduleMode = "interval" | "cron";

export interface AutomationDraft {
  kind: string;
  threadId: string;
  workspaceId: string;
  title: string;
  prompt: string;
  schedule: Record<string, unknown>;
}

export function AutomationEditor({
  editing,
  threads,
  workspaces,
  onSubmit,
  onReset,
}: {
  editing?: CodexAutomation;
  threads: CodexThread[];
  workspaces: CodexWorkspace[];
  onSubmit: (draft: AutomationDraft) => Promise<void>;
  onReset: () => void;
}) {
  const [kind, setKind] = useState("thread_wakeup");
  const [threadId, setThreadId] = useState("");
  const [workspaceId, setWorkspaceId] = useState("");
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [scheduleMode, setScheduleMode] = useState<ScheduleMode>("interval");
  const [intervalMinutes, setIntervalMinutes] = useState(1440);
  const [cron, setCron] = useState("0 9 * * *");

  useEffect(() => {
    if (!editing) {
      setKind("thread_wakeup");
      setThreadId("");
      setWorkspaceId("");
      setTitle("");
      setPrompt("");
      setScheduleMode("interval");
      setIntervalMinutes(1440);
      setCron("0 9 * * *");
      return;
    }
    setKind(editing.kind || "thread_wakeup");
    setThreadId(editing.threadId || "");
    setWorkspaceId(editing.workspaceId || "");
    setTitle(editing.title || "");
    setPrompt(editing.promptSummary || "");
    const cronValue = typeof editing.schedule?.cron === "string" ? (editing.schedule.cron as string) : "";
    if (cronValue) {
      setScheduleMode("cron");
      setCron(cronValue);
    } else {
      setScheduleMode("interval");
      const interval = Number(editing.schedule?.intervalMinutes);
      setIntervalMinutes(Number.isFinite(interval) && interval > 0 ? interval : 1440);
    }
  }, [editing]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const schedule = scheduleMode === "cron" ? { cron: cron.trim() } : { intervalMinutes };
    await onSubmit({ kind, threadId, workspaceId, title, prompt, schedule });
  }

  return (
    <Panel
      actions={editing ? <Button onClick={onReset}>新建</Button> : undefined}
      subtitle="Thread Wakeup 绑定现有会话；Project Automation 绑定 workspace 并创建后台 thread。"
      title={editing ? "编辑自动化" : "创建自动化"}
    >
      <form className="grid gap-3" onSubmit={submit}>
        <Field label="类型">
          <select className="select" disabled={Boolean(editing)} onChange={(event) => setKind(event.target.value)} value={kind}>
            <option value="thread_wakeup">Thread Wakeup</option>
            <option value="project">Project Automation</option>
          </select>
        </Field>
        {kind === "thread_wakeup" ? (
          <Field label="会话">
            <select className="select" onChange={(event) => setThreadId(event.target.value)} value={threadId}>
              <option value="">选择会话</option>
              {threads.map((thread) => <option key={thread.id} value={thread.id}>{thread.title || thread.id}</option>)}
            </select>
          </Field>
        ) : (
          <Field label="工作区">
            <select className="select" onChange={(event) => setWorkspaceId(event.target.value)} value={workspaceId}>
              <option value="">选择工作区</option>
              {workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.label || workspace.pathSummary || workspace.id}</option>)}
            </select>
          </Field>
        )}
        <Field label="标题">
          <input className="input" onChange={(event) => setTitle(event.target.value)} value={title} />
        </Field>
        <Field label="Prompt 摘要">
          <textarea className="input min-h-20" onChange={(event) => setPrompt(event.target.value)} value={prompt} />
        </Field>
        <Field label="调度方式">
          <select className="select" onChange={(event) => setScheduleMode(event.target.value as ScheduleMode)} value={scheduleMode}>
            <option value="interval">固定间隔</option>
            <option value="cron">Cron 表达式</option>
          </select>
        </Field>
        {scheduleMode === "interval" ? (
          <Field label="间隔分钟">
            <input className="input" min={15} onChange={(event) => setIntervalMinutes(Number(event.target.value))} type="number" value={intervalMinutes} />
          </Field>
        ) : (
          <Field help="5 字段 UTC cron：分 时 日 月 周。例如 0 9 * * 1-5 表示工作日 09:00。" label="Cron 表达式">
            <input className="input mono" onChange={(event) => setCron(event.target.value)} placeholder="0 9 * * *" value={cron} />
          </Field>
        )}
        <Button type="submit" tone="primary">{editing ? "保存" : "创建"}</Button>
      </form>
    </Panel>
  );
}
