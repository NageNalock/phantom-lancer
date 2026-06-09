import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexCommand, CodexCommandAssessment, CodexThread } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState, Notice, Pill } from "../../../components/ui";
import { formatDate } from "../../../domain/labels";

export function CommandPane({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
  const [items, setItems] = useState<CodexCommand[]>([]);
  const [command, setCommand] = useState("");
  const [confirmDanger, setConfirmDanger] = useState(false);
  const [assessment, setAssessment] = useState<CodexCommandAssessment | null>(null);
  const [assessing, setAssessing] = useState(false);

  const load = useCallback(async () => {
    const response = await actions.api<{ items?: CodexCommand[] }>(`/api/codex/threads/${thread.id}/commands`);
    setItems(response.items || []);
  }, [actions, thread.id]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setAssessment(null);
  }, [command]);

  async function assess() {
    if (!command.trim()) return;
    setAssessing(true);
    try {
      const response = await actions.api<{ assessment?: CodexCommandAssessment }>(`/api/codex/threads/${thread.id}/commands/assess`, { method: "POST", csrf: actions.csrf, body: { command: command.trim() } });
      setAssessment(response.assessment || null);
    } catch (error) {
      setAssessment(null);
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setAssessing(false);
    }
  }

  async function run(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!command.trim()) return;
    try {
      await actions.api(`/api/codex/threads/${thread.id}/commands`, { method: "POST", csrf: actions.csrf, body: { command: command.trim(), confirmDanger } });
      setCommand("");
      setConfirmDanger(false);
      await load();
      onRefresh();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function interrupt(id: string) {
    try {
      await actions.api(`/api/codex/commands/${id}/interrupt`, { method: "POST", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function attach(id: string) {
    try {
      await actions.api(`/api/codex/commands/${id}/attach`, { method: "POST", csrf: actions.csrf });
      await load();
      onRefresh();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid gap-3">
      <form className="grid gap-2" onSubmit={run}>
        <input className="input mono" onChange={(event) => setCommand(event.target.value)} placeholder="npm test -- --runInBand" value={command} />
        <Notice tone="warn">只允许只读 Git 查询，以及经确认的本地 check/test/build 脚本；项目代码命令必须通过本机 OS sandbox，沙箱不可用时会拒绝执行。</Notice>
        <label className="flex items-center gap-2 text-xs text-[var(--muted-strong)]">
          <input checked={confirmDanger} onChange={(event) => setConfirmDanger(event.target.checked)} type="checkbox" />
          我确认要执行本地项目代码或构建脚本
        </label>
        {assessment ? <CommandAssessmentNotice assessment={assessment} /> : null}
        <div className="flex flex-wrap gap-2">
          <Button disabled={!command.trim() || assessing} onClick={() => void assess()}>
            {assessing ? "评估中" : "评估"}
          </Button>
          <Button disabled={!command.trim()} type="submit">运行命令</Button>
        </div>
      </form>
      <CommandHistory items={items} onAttach={(id) => void attach(id)} onInterrupt={(id) => void interrupt(id)} />
    </div>
  );
}

function CommandAssessmentNotice({ assessment }: { assessment: CodexCommandAssessment }) {
  return (
    <Notice tone="warn">
      {assessment.commandPreview} · {assessment.cwdSummary || "workspace"} · {assessment.riskSummary || assessment.class || "已评估"}
      {assessment.sandboxSummary ? <span className="mt-1 block">{assessment.sandboxSummary}</span> : null}
    </Notice>
  );
}

function CommandHistory({ items, onAttach, onInterrupt }: { items: CodexCommand[]; onAttach: (id: string) => void; onInterrupt: (id: string) => void }) {
  if (!items.length) {
    return <EmptyState title="暂无命令" body="Owner 显式运行的命令会保留输出历史；需要时再附加摘要到 Codex 上下文。" />;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={item.id}>
          <div className="flex items-center justify-between gap-2">
            <span className="mono truncate">{item.commandPreview}</span>
            <Pill tone={item.status === "completed" ? "good" : item.status === "failed" || item.status === "timeout" ? "danger" : "warn"}>{item.status}</Pill>
          </div>
          {item.outputPreview ? <pre className="mono mt-2 max-h-32 overflow-auto whitespace-pre-wrap">{item.outputPreview}</pre> : null}
          {item.errorSummary ? <Notice tone="danger">{item.errorSummary}</Notice> : null}
          <div className="mt-1 flex flex-wrap gap-2">
            {item.status === "running" ? <button className="text-[var(--danger)]" onClick={() => onInterrupt(item.id)} type="button">中断</button> : null}
            {item.outputPreview && item.status !== "running" ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onAttach(item.id)} type="button">附加输出到上下文</button> : null}
          </div>
          <span className="muted mt-1 block">{formatDate(item.createdAt)}</span>
        </div>
      ))}
    </div>
  );
}
