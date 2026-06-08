import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { CodexBrowserSession, CodexCommand, CodexCommandAssessment, CodexReviewSnapshot, CodexThread } from "../../app/types";
import { Button, EmptyState, Notice, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { formatDate } from "../../domain/labels";

export function ThreadP1Panels({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
  const [panel, setPanel] = useState<"review" | "commands" | "browser">("review");
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
      <div className="flex flex-wrap gap-1 border-b border-[var(--line)] p-2">
        {[
          ["review", "Review"],
          ["commands", "Commands"],
          ["browser", "Preview"],
        ].map(([id, label]) => (
          <button className={`rounded-md px-2 py-1 text-xs ${panel === id ? "bg-[var(--surface-strong)] text-[var(--text)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`} key={id} onClick={() => setPanel(id as typeof panel)} type="button">
            {label}
          </button>
        ))}
      </div>
      <div className="p-3">
        {panel === "review" ? <ReviewPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
        {panel === "commands" ? <CommandPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
        {panel === "browser" ? <BrowserPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
      </div>
    </div>
  );
}

function ReviewPane({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
  const [scope, setScope] = useState("uncommitted");
  const [snapshot, setSnapshot] = useState<CodexReviewSnapshot | null>(null);
  const [filePath, setFilePath] = useState("");
  const [newLine, setNewLine] = useState("");
  const [hunkHeader, setHunkHeader] = useState("");
  const [body, setBody] = useState("");
  const [loading, setLoading] = useState(false);
  const diffFiles = useMemo(() => parseDiff(snapshot?.diff || ""), [snapshot?.diff]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await actions.api<CodexReviewSnapshot>(`/api/codex/threads/${thread.id}/review?scope=${scope}`);
      setSnapshot(response);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions, scope, thread.id]);

  useEffect(() => {
    void load();
  }, [load]);

  async function createComment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!filePath.trim() || !body.trim()) return;
    try {
      await actions.api(`/api/codex/threads/${thread.id}/review/comments`, { method: "POST", csrf: actions.csrf, body: { filePath: filePath.trim(), newLine: Number(newLine) || 0, hunkHeader: hunkHeader.trim(), body: body.trim() } });
      setBody("");
      setNewLine("");
      setHunkHeader("");
      await load();
      onRefresh();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function resolve(id: string) {
    try {
      await actions.api(`/api/codex/review/comments/${id}`, { method: "DELETE", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  function selectDiffLine(file: string, line?: number, hunk?: string) {
    setFilePath(file);
    setNewLine(line ? String(line) : "");
    setHunkHeader(hunk || "");
  }

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap gap-2">
        <select className="select" onChange={(event) => setScope(event.target.value)} value={scope}>
          <option value="uncommitted">Uncommitted</option>
          <option value="last_turn">Last turn</option>
          <option value="branch">Branch</option>
        </select>
        <Button onClick={() => void load()}>{loading ? "加载中" : "刷新 diff"}</Button>
        {snapshot?.truncated ? <Pill tone="warn">已截断</Pill> : null}
      </div>
      {snapshot ? (
        <>
          <pre className="mono max-h-32 overflow-auto rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs whitespace-pre-wrap">{snapshot.summary || "无 diff 摘要"}</pre>
          {diffFiles.length ? (
            <div className="max-h-96 overflow-auto rounded border border-[var(--line)] bg-[var(--surface-soft)]">
              {diffFiles.map((file) => (
                <details className="border-b border-[var(--line)] last:border-b-0" key={file.file} open={diffFiles.length === 1}>
                  <summary className="cursor-pointer px-2 py-1.5 text-xs font-medium">{file.file}</summary>
                  <div className="grid gap-2 px-2 pb-2">
                    {file.hunks.map((hunk, hunkIndex) => (
                      <div className="overflow-hidden rounded border border-[var(--line)] bg-[var(--surface)]" key={`${file.file}:${hunkIndex}`}>
                        <button className="mono w-full bg-[var(--surface-strong)] px-2 py-1 text-left text-xs text-[var(--muted-strong)]" onClick={() => selectDiffLine(file.file, undefined, hunk.header)} type="button">
                          {hunk.header || "file header"}
                        </button>
                        <div className="mono overflow-x-auto text-xs">
                          {hunk.lines.map((line, lineIndex) => (
                            <button
                              className={`grid w-full grid-cols-[3.5rem_minmax(0,1fr)] gap-2 px-2 py-0.5 text-left ${diffLineClass(line.kind)}`}
                              disabled={!line.newLine}
                              key={`${file.file}:${hunkIndex}:${lineIndex}`}
                              onClick={() => selectDiffLine(file.file, line.newLine, hunk.header)}
                              type="button"
                            >
                              <span className="select-none text-right text-[var(--muted)]">{line.newLine || line.oldLine || ""}</span>
                              <span className="whitespace-pre">{line.text || " "}</span>
                            </button>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                </details>
              ))}
            </div>
          ) : (
            <EmptyState title="无 diff" body="当前 scope 没有可展示的 Git 差异。" />
          )}
          <form className="grid gap-2" onSubmit={createComment}>
            <input className="input" onChange={(event) => setFilePath(event.target.value)} placeholder="相对文件路径，例如 web/src/App.tsx" value={filePath} />
            <div className="grid grid-cols-[8rem_minmax(0,1fr)] gap-2">
              <input className="input mono" inputMode="numeric" onChange={(event) => setNewLine(event.target.value)} placeholder="new line" value={newLine} />
              <input className="input mono" onChange={(event) => setHunkHeader(event.target.value)} placeholder="@@ hunk header，可选" value={hunkHeader} />
            </div>
            <textarea className="input min-h-16" onChange={(event) => setBody(event.target.value)} placeholder="给 Codex 的 review comment" value={body} />
            <Button disabled={!filePath.trim() || !body.trim()} type="submit">添加评论</Button>
          </form>
          {snapshot.comments?.length ? (
            <div className="grid gap-2">
              {snapshot.comments.map((comment) => (
                <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={comment.id}>
                  <div className="flex justify-between gap-2">
                    <span className="mono">{comment.filePath}{comment.newLine ? `:${comment.newLine}` : ""}</span>
                    <Pill tone={comment.status === "resolved" ? "good" : "neutral"}>{comment.status || "open"}</Pill>
                  </div>
                  {comment.hunkHeader ? <span className="mono mt-1 block text-[var(--muted)]">{comment.hunkHeader}</span> : null}
                  <p className="mt-1 mb-0 whitespace-pre-wrap">{comment.body}</p>
                  {comment.status !== "resolved" ? <button className="mt-1 text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => void resolve(comment.id)} type="button">标记已处理</button> : null}
                </div>
              ))}
            </div>
          ) : null}
        </>
      ) : (
        <EmptyState title="Review 不可用" body="当前工作区可能不是 Git 仓库，或 diff 暂时无法读取。" />
      )}
    </div>
  );
}

type DiffFile = {
  file: string;
  hunks: DiffHunk[];
};

type DiffHunk = {
  header: string;
  lines: DiffLine[];
};

type DiffLine = {
  kind: "add" | "remove" | "context" | "meta";
  text: string;
  oldLine?: number;
  newLine?: number;
};

function parseDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let currentFile: DiffFile | null = null;
  let currentHunk: DiffHunk | null = null;
  let oldLine = 0;
  let newLine = 0;

  for (const rawLine of diff.split("\n")) {
    if (rawLine.startsWith("diff --git ")) {
      const file = parseDiffFile(rawLine);
      currentFile = { file, hunks: [] };
      files.push(currentFile);
      currentHunk = null;
      continue;
    }
    if (!currentFile) continue;
    if (rawLine.startsWith("@@")) {
      const parsed = parseHunkHeader(rawLine);
      oldLine = parsed.oldStart;
      newLine = parsed.newStart;
      currentHunk = { header: rawLine, lines: [] };
      currentFile.hunks.push(currentHunk);
      continue;
    }
    if (!currentHunk) {
      currentHunk = { header: "file header", lines: [] };
      currentFile.hunks.push(currentHunk);
    }
    if (rawLine.startsWith("+") && !rawLine.startsWith("+++")) {
      currentHunk.lines.push({ kind: "add", text: rawLine, newLine });
      newLine += 1;
    } else if (rawLine.startsWith("-") && !rawLine.startsWith("---")) {
      currentHunk.lines.push({ kind: "remove", text: rawLine, oldLine });
      oldLine += 1;
    } else if (rawLine.startsWith(" ")) {
      currentHunk.lines.push({ kind: "context", text: rawLine, oldLine, newLine });
      oldLine += 1;
      newLine += 1;
    } else {
      currentHunk.lines.push({ kind: "meta", text: rawLine });
    }
  }

  return files.filter((file) => file.hunks.some((hunk) => hunk.lines.length));
}

function parseDiffFile(line: string): string {
  const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
  if (!match) return line.replace(/^diff --git\s+/, "");
  return match[2] || match[1];
}

function parseHunkHeader(line: string): { oldStart: number; newStart: number } {
  const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  return { oldStart: match ? Number(match[1]) : 0, newStart: match ? Number(match[2]) : 0 };
}

function diffLineClass(kind: DiffLine["kind"]): string {
  if (kind === "add") return "bg-[var(--good-soft)] text-[var(--text)] hover:bg-[rgba(18,132,79,0.14)]";
  if (kind === "remove") return "bg-[var(--danger-soft)] text-[var(--danger)]";
  if (kind === "meta") return "text-[var(--muted)]";
  return "hover:bg-[var(--surface-soft)]";
}

function CommandPane({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
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
        <Notice tone="warn">只允许只读 Git 查询，以及经确认的本地 check/test/build 脚本；输出默认只展示给 owner。</Notice>
        <label className="flex items-center gap-2 text-xs text-[var(--muted-strong)]">
          <input checked={confirmDanger} onChange={(event) => setConfirmDanger(event.target.checked)} type="checkbox" />
          我确认要执行本地项目代码或构建脚本
        </label>
        {assessment ? (
          <Notice tone="warn">
            {assessment.commandPreview} · {assessment.cwdSummary || "workspace"} · {assessment.riskSummary || assessment.class || "已评估"}
          </Notice>
        ) : null}
        <div className="flex flex-wrap gap-2">
          <Button disabled={!command.trim() || assessing} onClick={() => void assess()}>
            {assessing ? "评估中" : "评估"}
          </Button>
          <Button disabled={!command.trim()} type="submit">运行命令</Button>
        </div>
      </form>
      {items.length ? (
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
	                {item.status === "running" ? <button className="text-[var(--danger)]" onClick={() => void interrupt(item.id)} type="button">中断</button> : null}
	                {item.outputPreview && item.status !== "running" ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => void attach(item.id)} type="button">附加输出到上下文</button> : null}
	              </div>
              <span className="muted mt-1 block">{formatDate(item.createdAt)}</span>
            </div>
          ))}
        </div>
      ) : (
	        <EmptyState title="暂无命令" body="Owner 显式运行的命令会保留输出历史；需要时再附加摘要到 Codex 上下文。" />
      )}
    </div>
  );
}

function BrowserPane({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
  const [items, setItems] = useState<CodexBrowserSession[]>([]);
  const [url, setUrl] = useState("http://127.0.0.1:5173");
  const [active, setActive] = useState<CodexBrowserSession | null>(null);
  const [comment, setComment] = useState("");
  const [allowPublic, setAllowPublic] = useState(false);
  const [marking, setMarking] = useState(false);
  const [point, setPoint] = useState<{ x: number; y: number } | null>(null);

  const load = useCallback(async () => {
    const response = await actions.api<{ items?: CodexBrowserSession[] }>(`/api/codex/threads/${thread.id}/browser/sessions`);
    const next = response.items || [];
    setItems(next);
    if (!active && next.length) setActive(next[0]);
  }, [actions, active, thread.id]);

  useEffect(() => {
    void load();
  }, [load]);

  async function open(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      const response = await actions.api<{ session: CodexBrowserSession }>(`/api/codex/threads/${thread.id}/browser/sessions`, { method: "POST", csrf: actions.csrf, body: { url, allowPublic } });
      setActive(response.session);
      setAllowPublic(false);
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function addComment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!active || !comment.trim()) return;
    try {
      await actions.api(`/api/codex/browser/sessions/${active.id}/comments`, { method: "POST", csrf: actions.csrf, body: { body: comment.trim(), x: point?.x || 0, y: point?.y || 0 } });
      setComment("");
      setPoint(null);
      setMarking(false);
      onRefresh();
      actions.setToast("已写入预览评论", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function deleteSession(id: string) {
    try {
      await actions.api(`/api/codex/browser/sessions/${id}`, { method: "DELETE", csrf: actions.csrf });
      const next = items.filter((item) => item.id !== id);
      setItems(next);
      setActive((current) => (current?.id === id ? next[0] || null : current));
      onRefresh();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid gap-3">
      <form className="grid grid-cols-[minmax(0,1fr)_auto] gap-2" onSubmit={open}>
        <input className="input mono" onChange={(event) => setUrl(event.target.value)} placeholder="http://127.0.0.1:5173" value={url} />
        <Button type="submit">打开预览</Button>
        <label className="col-span-full flex items-center gap-2 text-xs text-[var(--muted-strong)]">
          <input checked={allowPublic} onChange={(event) => setAllowPublic(event.target.checked)} type="checkbox" />
          允许打开公共 URL；localhost 和 workspace file URL 不需要勾选
        </label>
      </form>
      {items.length ? (
        <div className="flex flex-wrap gap-2">
          {items.map((item) => (
            <button className={`max-w-full truncate rounded border px-2 py-1 text-xs ${active?.id === item.id ? "border-[var(--line-strong)] bg-[var(--surface-strong)]" : "border-[var(--line)]"}`} key={item.id} onClick={() => setActive(item)} type="button">
              {item.url}
            </button>
          ))}
        </div>
      ) : null}
      {active ? (
        <>
          <div className="relative h-80 overflow-hidden rounded border border-[var(--line)] bg-white">
            <iframe className="h-full w-full" sandbox={previewAllowsScripts(active.url) ? "allow-scripts allow-forms" : ""} src={`/api/codex/browser/sessions/${active.id}/proxy`} title="Codex preview" />
            {marking ? (
              <button
                aria-label="选择预览评论位置"
                className="absolute inset-0 cursor-crosshair bg-transparent"
                onClick={(event) => {
                  const rect = event.currentTarget.getBoundingClientRect();
                  setPoint({ x: Math.round(event.clientX - rect.left), y: Math.round(event.clientY - rect.top) });
                  setMarking(false);
                }}
                type="button"
              />
            ) : null}
            {point ? <span className="absolute h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white bg-[var(--accent)] shadow-sm" style={{ left: point.x, top: point.y }} /> : null}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--muted-strong)]">
            <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => setMarking((value) => !value)} type="button">
              {marking ? "取消标注" : point ? `位置 ${point.x}, ${point.y}` : "标注区域"}
            </button>
            {point ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => setPoint(null)} type="button">清除位置</button> : null}
            <button className="text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => void deleteSession(active.id)} type="button">关闭预览</button>
          </div>
          <form className="grid grid-cols-[minmax(0,1fr)_auto] gap-2" onSubmit={addComment}>
            <input className="input" onChange={(event) => setComment(event.target.value)} placeholder="给当前预览添加视觉反馈或验证结论" value={comment} />
            <Button disabled={!comment.trim()} type="submit">写入评论</Button>
          </form>
        </>
      ) : (
	        <EmptyState title="暂无预览" body="支持 localhost/127.0.0.1、workspace file URL；公共 URL 需要显式允许，私网探测默认拒绝。" />
      )}
    </div>
  );
}

function previewAllowsScripts(raw?: string): boolean {
  if (!raw) return false;
  try {
    const parsed = new URL(raw);
    return parsed.protocol === "file:" || parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1" || parsed.hostname === "::1";
  } catch {
    return false;
  }
}
