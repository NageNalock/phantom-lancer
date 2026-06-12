import type { CodexEvent } from "../../app/types";

export const CODEX_STREAM_EVENTS = [
  "thread.started",
  "thread.resumed",
  "thread.archived",
  "thread.status.changed",
  "turn.queued",
  "turn.started",
  "turn.completed",
  "turn.failed",
  "turn.cancelled",
  "message.user",
  "message.agent",
  "message.reasoning",
  "command.started",
  "command.completed",
  "command.owner.queued",
  "command.owner.started",
  "command.owner.output",
  "command.owner.output.attached",
  "command.owner.completed",
  "file_change.started",
  "file_change.completed",
  "approval.requested",
  "approval.resolved",
  "tool.started",
  "tool.completed",
  "plan.updated",
  "diff.updated",
  "review.comment.created",
  "browser.preview.opened",
  "browser.preview.comment",
  "usage.updated",
  "diagnostic.warning",
  "diagnostic.error",
];

export type CodexStreamState = "connecting" | "live" | "reconnecting";

export function parseCodexStreamEvent(data: string): CodexEvent | null {
  try {
    const parsed = JSON.parse(data) as CodexEvent & { type?: string; scopeId?: string };
    if (parsed.eventType) return parsed;
    const payload = parsed.payload || {};
    if (parsed.type) {
      return {
        id: stringValue(payload.codexEventId) || parsed.id,
        threadId: parsed.scopeId,
        turnId: stringValue(payload.turnId),
        sequence: numberValue(payload.sequence) || parsed.sequence,
        eventType: parsed.type,
        codexMethod: stringValue(payload.codexMethod),
        itemType: stringValue(payload.itemType),
        textPreview: stringValue(payload.textPreview),
        payload,
        createdAt: parsed.createdAt,
      };
    }
  } catch {
    return null;
  }
  return null;
}

export function shouldRefreshThread(event: CodexEvent): boolean {
  const type = event.eventType || "";
  return type.startsWith("turn.") || type.startsWith("approval.") || type === "thread.status.changed" || type === "diagnostic.error";
}

export function streamStateLabel(value: CodexStreamState): string {
  if (value === "live") return "live";
  if (value === "reconnecting") return "reconnecting";
  return "connecting";
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown): number {
  if (typeof value === "number") return value;
  if (typeof value === "string") return Number(value) || 0;
  return 0;
}
