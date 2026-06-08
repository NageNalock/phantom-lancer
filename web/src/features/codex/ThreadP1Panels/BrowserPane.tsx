import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexBrowserSession, CodexThread } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState } from "../../../components/ui";

export function BrowserPane({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
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
    setActive((current) => current || next[0] || null);
  }, [actions, thread.id]);

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
      <PreviewOpenForm allowPublic={allowPublic} onAllowPublicChange={setAllowPublic} onSubmit={open} onURLChange={setUrl} url={url} />
      <PreviewSessionList activeId={active?.id} items={items} onSelect={setActive} />
      {active ? (
        <>
          <PreviewFrame active={active} marking={marking} onPoint={setPoint} onStopMarking={() => setMarking(false)} point={point} />
          <PreviewActions active={active} marking={marking} onDelete={(id) => void deleteSession(id)} onPointClear={() => setPoint(null)} onToggleMarking={() => setMarking((value) => !value)} point={point} />
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

function PreviewOpenForm({ allowPublic, url, onAllowPublicChange, onSubmit, onURLChange }: { allowPublic: boolean; url: string; onAllowPublicChange: (value: boolean) => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void; onURLChange: (value: string) => void }) {
  return (
    <form className="grid grid-cols-[minmax(0,1fr)_auto] gap-2" onSubmit={onSubmit}>
      <input className="input mono" onChange={(event) => onURLChange(event.target.value)} placeholder="http://127.0.0.1:5173" value={url} />
      <Button type="submit">打开预览</Button>
      <label className="col-span-full flex items-center gap-2 text-xs text-[var(--muted-strong)]">
        <input checked={allowPublic} onChange={(event) => onAllowPublicChange(event.target.checked)} type="checkbox" />
        允许打开公共 URL；localhost 和 workspace file URL 不需要勾选
      </label>
    </form>
  );
}

function PreviewSessionList({ activeId, items, onSelect }: { activeId?: string; items: CodexBrowserSession[]; onSelect: (session: CodexBrowserSession) => void }) {
  if (!items.length) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {items.map((item) => (
        <button className={`max-w-full truncate rounded border px-2 py-1 text-xs ${activeId === item.id ? "border-[var(--line-strong)] bg-[var(--surface-strong)]" : "border-[var(--line)]"}`} key={item.id} onClick={() => onSelect(item)} type="button">
          {item.url}
        </button>
      ))}
    </div>
  );
}

function PreviewFrame({ active, marking, point, onPoint, onStopMarking }: { active: CodexBrowserSession; marking: boolean; point: { x: number; y: number } | null; onPoint: (point: { x: number; y: number }) => void; onStopMarking: () => void }) {
  return (
    <div className="relative h-80 overflow-hidden rounded border border-[var(--line)] bg-white">
      <iframe className="h-full w-full" sandbox={previewAllowsScripts(active.url) ? "allow-scripts allow-forms" : ""} src={`/api/codex/browser/sessions/${active.id}/proxy`} title="Codex preview" />
      {marking ? (
        <button
          aria-label="选择预览评论位置"
          className="absolute inset-0 cursor-crosshair bg-transparent"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            onPoint({ x: Math.round(event.clientX - rect.left), y: Math.round(event.clientY - rect.top) });
            onStopMarking();
          }}
          type="button"
        />
      ) : null}
      {point ? <span className="absolute h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white bg-[var(--accent)] shadow-sm" style={{ left: point.x, top: point.y }} /> : null}
    </div>
  );
}

function PreviewActions({ active, marking, point, onDelete, onPointClear, onToggleMarking }: { active: CodexBrowserSession; marking: boolean; point: { x: number; y: number } | null; onDelete: (id: string) => void; onPointClear: () => void; onToggleMarking: () => void }) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--muted-strong)]">
      <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={onToggleMarking} type="button">
        {marking ? "取消标注" : point ? `位置 ${point.x}, ${point.y}` : "标注区域"}
      </button>
      {point ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={onPointClear} type="button">清除位置</button> : null}
      <button className="text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => onDelete(active.id)} type="button">关闭预览</button>
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
