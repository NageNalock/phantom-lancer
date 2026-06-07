import type { CodexEvent } from "../../app/types";
import { codexEventTitle, formatDate } from "../../domain/labels";

export function EventRow({ event }: { event: CodexEvent }) {
  const type = event.eventType || "";
  const isUser = type === "message.user";
  const isAgent = type === "message.agent" || type === "message.reasoning";
  const isCommand = type === "command.started" || type === "command.completed";
  const isFileChange = type === "file_change.started" || type === "file_change.completed";
  const tone = type === "diagnostic.error" || type === "turn.failed" ? "danger" : type === "diagnostic.warning" ? "warn" : type === "approval.requested" ? "warn" : "neutral";

  if ((isCommand || isFileChange) && event.textPreview) {
    return (
      <details className={`rounded-md border px-3 py-2 text-sm ${rowClass(tone, false, false)}`}>
        <summary className="flex cursor-pointer items-center justify-between gap-2">
          <span className="flex min-w-0 items-center gap-2">
            <span className="text-xs font-medium text-[var(--muted-strong)]">{codexEventTitle(type)}</span>
            <code className="mono truncate text-xs text-[var(--muted)]">{firstLine(event.textPreview)}</code>
          </span>
          <span className="muted shrink-0 text-xs">{formatDate(event.createdAt)}</span>
        </summary>
        <pre className="mono mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-[var(--surface-soft)] p-2 text-xs leading-relaxed">{event.textPreview}</pre>
      </details>
    );
  }

  return (
    <div className={`rounded-md border px-3 py-2 text-sm ${rowClass(tone, isUser, isAgent)}`}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-[var(--muted-strong)]">{codexEventTitle(type)}</span>
        <span className="muted text-xs">{formatDate(event.createdAt)}</span>
      </div>
      {event.textPreview ? <div className="mt-1 whitespace-pre-wrap break-words leading-relaxed">{event.textPreview}</div> : null}
    </div>
  );
}

function firstLine(text: string): string {
  const line = text.split("\n", 1)[0] || "";
  return line.length > 80 ? `${line.slice(0, 80)}...` : line;
}

function rowClass(tone: string, isUser: boolean, isAgent: boolean): string {
  if (tone === "danger") return "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)]";
  if (tone === "warn") return "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]";
  if (isUser) return "border-[var(--line-strong)] bg-[var(--surface)]";
  if (isAgent) return "border-[var(--line)] bg-[var(--surface)]";
  return "border-[var(--line)] bg-[var(--surface)]";
}
