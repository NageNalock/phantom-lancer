import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexReviewComment, CodexReviewSnapshot, CodexThread } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState, Field, Pill } from "../../../components/ui";
import { useQueryParamState } from "../../../hooks/useQueryParamState";
import { diffLineClass, parseDiff } from "./diff";
import type { DiffFile } from "./diff";

type ReviewScope = "uncommitted" | "last_turn" | "branch";
const REVIEW_SCOPES: ReviewScope[] = ["uncommitted", "last_turn", "branch"];

export function ReviewPane({ actions, thread, onDraft, onRefresh }: { actions: AppActions; thread: CodexThread; onDraft: (threadId: string, prompt: string) => void; onRefresh: () => void }) {
  const [scope, setScope] = useQueryParamState<ReviewScope>("codexReviewScope", REVIEW_SCOPES, "uncommitted", { clearKeys: ["codex", "codexInbox", "codexRuntime"] });
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

  function askCodex(comment: CodexReviewComment) {
    const prompt = reviewFollowUpPrompt(comment);
    if (!prompt) return;
    onDraft(thread.id, prompt);
    actions.setToast("已填入 composer，可编辑后发送", "good");
  }

  function selectDiffLine(file: string, line?: number, hunk?: string) {
    setFilePath(file);
    setNewLine(line ? String(line) : "");
    setHunkHeader(hunk || "");
  }

  return (
    <div className="grid gap-3">
      <ReviewToolbar loading={loading} onLoad={() => void load()} onScopeChange={setScope} scope={scope} truncated={snapshot?.truncated} />
      {snapshot ? (
        <>
          <pre className="mono max-h-32 overflow-auto rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs whitespace-pre-wrap">{snapshot.summary || "无 diff 摘要"}</pre>
          <DiffViewer files={diffFiles} onSelect={selectDiffLine} />
          <ReviewCommentForm body={body} filePath={filePath} hunkHeader={hunkHeader} newLine={newLine} onBodyChange={setBody} onFilePathChange={setFilePath} onHunkHeaderChange={setHunkHeader} onNewLineChange={setNewLine} onSubmit={createComment} />
          <ReviewCommentList comments={snapshot.comments || []} onAskCodex={askCodex} onResolve={(id) => void resolve(id)} />
        </>
      ) : (
        <EmptyState title="Review 不可用" body="当前工作区可能不是 Git 仓库，或 diff 暂时无法读取。" />
      )}
    </div>
  );
}

function ReviewToolbar({ loading, scope, truncated, onLoad, onScopeChange }: { loading: boolean; scope: ReviewScope; truncated?: boolean; onLoad: () => void; onScopeChange: (scope: ReviewScope) => void }) {
  return (
    <div className="flex flex-wrap gap-2">
      <select className="select" name="codex_review_scope" onChange={(event) => onScopeChange(event.target.value as ReviewScope)} value={scope}>
        <option value="uncommitted">Uncommitted</option>
        <option value="last_turn">Last turn</option>
        <option value="branch">Branch</option>
      </select>
      <Button onClick={onLoad}>{loading ? "加载中" : "刷新 diff"}</Button>
      {truncated ? <Pill tone="warn">已截断</Pill> : null}
    </div>
  );
}

function DiffViewer({ files, onSelect }: { files: DiffFile[]; onSelect: (file: string, line?: number, hunk?: string) => void }) {
  if (!files.length) {
    return <EmptyState title="无 diff" body="当前 scope 没有可展示的 Git 差异。" />;
  }
  return (
    <div className="max-h-96 overflow-auto rounded border border-[var(--line)] bg-[var(--surface-soft)]">
      {files.map((file) => (
        <details className="border-b border-[var(--line)] last:border-b-0" key={file.file} open={files.length === 1}>
          <summary className="cursor-pointer px-2 py-1.5 text-xs font-medium">{file.file}</summary>
          <div className="grid gap-2 px-2 pb-2">
            {file.hunks.map((hunk, hunkIndex) => (
              <div className="overflow-hidden rounded border border-[var(--line)] bg-[var(--surface)]" key={`${file.file}:${hunkIndex}`}>
                <button className="mono w-full bg-[var(--surface-strong)] px-2 py-1 text-left text-xs text-[var(--muted-strong)]" onClick={() => onSelect(file.file, undefined, hunk.header)} type="button">
                  {hunk.header || "file header"}
                </button>
                <div className="mono overflow-x-auto text-xs">
                  {hunk.lines.map((line, lineIndex) => (
                    <button className={`grid w-full grid-cols-[3.5rem_minmax(0,1fr)] gap-2 px-2 py-0.5 text-left ${diffLineClass(line.kind)}`} disabled={!line.newLine} key={`${file.file}:${hunkIndex}:${lineIndex}`} onClick={() => onSelect(file.file, line.newLine, hunk.header)} type="button">
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
  );
}

function ReviewCommentForm({ body, filePath, hunkHeader, newLine, onBodyChange, onFilePathChange, onHunkHeaderChange, onNewLineChange, onSubmit }: { body: string; filePath: string; hunkHeader: string; newLine: string; onBodyChange: (value: string) => void; onFilePathChange: (value: string) => void; onHunkHeaderChange: (value: string) => void; onNewLineChange: (value: string) => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  return (
    <form className="grid gap-2" onSubmit={onSubmit}>
      <Field label="文件路径">
        <input className="input" name="codex_review_file_path" onChange={(event) => onFilePathChange(event.target.value)} placeholder="web/src/App.tsx" value={filePath} />
      </Field>
      <div className="grid grid-cols-[8rem_minmax(0,1fr)] gap-2">
        <Field label="新行号">
          <input className="input mono" inputMode="numeric" name="codex_review_new_line" onChange={(event) => onNewLineChange(event.target.value)} placeholder="42" value={newLine} />
        </Field>
        <Field label="Hunk">
          <input className="input mono" name="codex_review_hunk" onChange={(event) => onHunkHeaderChange(event.target.value)} placeholder="@@ hunk header，可选" value={hunkHeader} />
        </Field>
      </div>
      <Field label="Review comment">
        <textarea autoComplete="off" className="input min-h-16" name="codex_review_comment" onChange={(event) => onBodyChange(event.target.value)} placeholder="说明希望 Codex 修改的点" value={body} />
      </Field>
      <Button disabled={!filePath.trim() || !body.trim()} type="submit">添加评论</Button>
    </form>
  );
}

function ReviewCommentList({ comments, onAskCodex, onResolve }: { comments: CodexReviewComment[]; onAskCodex: (comment: CodexReviewComment) => void; onResolve: (id: string) => void }) {
  if (!comments.length) return null;
  return (
    <div className="grid gap-2">
      {comments.map((comment) => (
        <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={comment.id}>
          <div className="flex justify-between gap-2">
            <span className="mono">{comment.filePath}{comment.newLine ? `:${comment.newLine}` : ""}</span>
            <Pill tone={comment.status === "resolved" ? "good" : "neutral"}>{comment.status || "open"}</Pill>
          </div>
          {comment.hunkHeader ? <span className="mono mt-1 block text-[var(--muted)]">{comment.hunkHeader}</span> : null}
          <p className="mt-1 mb-0 whitespace-pre-wrap">{comment.body}</p>
          {comment.status !== "resolved" ? (
            <div className="mt-2 flex flex-wrap gap-2">
              <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onAskCodex(comment)} type="button">让 Codex 修复</button>
              <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onResolve(comment.id)} type="button">标记已处理</button>
            </div>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function reviewFollowUpPrompt(comment: CodexReviewComment): string {
  const file = comment.filePath?.trim();
  const body = comment.body?.trim();
  if (!file || !body) return "";
  const line = comment.newLine ? `:${comment.newLine}` : "";
  const hunk = comment.hunkHeader ? `\nHunk: ${comment.hunkHeader}` : "";
  return `Please address this review comment in ${file}${line}.${hunk}\n\nReview comment:\n${body}\n\nMake the smallest safe code change, then summarize what changed.`;
}
