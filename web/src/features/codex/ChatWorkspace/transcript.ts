import type { CodexEvent, CodexTurn } from "../../../app/types";
import { codexEventTitle } from "../../../domain/labels";

export interface ChatArtifact {
  id: string;
  type: "image";
  src: string;
  label: string;
  path?: string;
}

export type ChatEntry =
  | {
      kind: "message";
      key: string;
      role: "user" | "assistant";
      text: string;
      threadId?: string;
      turnId?: string;
      createdAt?: string;
      sequence: number;
      streaming?: boolean;
    }
  | {
      kind: "artifact";
      key: string;
      artifacts: ChatArtifact[];
      createdAt?: string;
      sequence: number;
    }
  | {
      kind: "status";
      key: string;
      label: string;
      detail?: string;
      tone: "neutral" | "good" | "warn" | "danger";
      createdAt?: string;
      sequence: number;
      active?: boolean;
    };

type StatusTone = Extract<ChatEntry, { kind: "status" }>["tone"];

const HIDDEN_TYPES = new Set(["thread.status.changed", "usage.updated", "turn.started", "turn.completed"]);
const CONVERSATION_TYPES = new Set(["message.user", "message.agent"]);

export function mergeCodexEvent(current: CodexEvent[], next: CodexEvent): CodexEvent[] {
  const sequence = next.sequence || 0;
  if (!sequence && !next.id) return current;
  const existingIndex = current.findIndex((item) => eventIdentity(item) === eventIdentity(next));
  if (existingIndex >= 0) {
    const copy = current.slice();
    copy[existingIndex] = { ...copy[existingIndex], ...next };
    return sortEvents(copy);
  }
  return sortEvents([...current, next]);
}

export function buildChatTranscript(events: CodexEvent[], turns: CodexTurn[]): ChatEntry[] {
  const sorted = sortEvents(events);
  const entries: ChatEntry[] = [];
  const turnStartedAt = new Map<string, string>();
  const seenArtifactKeys = new Set<string>();
  const activeTurn = turns.find((turn) => turn.status === "running" || turn.status === "waiting_approval");

  for (const event of sorted) {
    const type = event.eventType || "";
    if (type === "turn.started" && event.turnId) {
      turnStartedAt.set(event.turnId, event.createdAt || "");
    }
    if (HIDDEN_TYPES.has(type)) continue;
    const artifacts = artifactsForEvent(event).filter((artifact) => {
      const key = artifact.path || artifact.src;
      if (seenArtifactKeys.has(key)) return false;
      seenArtifactKeys.add(key);
      return true;
    });
    if (artifacts.length) {
      entries.push({
        kind: "artifact",
        key: `artifact-${eventKey(event)}`,
        artifacts,
        createdAt: event.createdAt,
        sequence: event.sequence || 0,
      });
      if (event.itemType === "imageView") continue;
    }
    if (CONVERSATION_TYPES.has(type)) {
      appendMessageEntry(entries, event);
      continue;
    }
    if (type === "message.reasoning") {
      appendReasoningEntry(entries, event);
      continue;
    }
    const status = statusEntryForEvent(event, turnStartedAt);
    if (status) entries.push(status);
  }

  const lastMessage = [...entries].reverse().find((entry) => entry.kind === "message");
  if (lastMessage?.kind === "message" && activeTurn?.id && lastMessage.turnId === activeTurn.id && lastMessage.role === "assistant") {
    lastMessage.streaming = true;
  } else if (activeTurn) {
    entries.push({
      kind: "status",
      key: `thinking-${activeTurn.id}`,
      label: activeTurn.status === "waiting_approval" ? "等待审批" : "正在思考",
      detail: activeTurn.status === "waiting_approval" ? "需要处理审批后继续" : "Codex 正在生成回复",
      tone: activeTurn.status === "waiting_approval" ? "warn" : "neutral",
      sequence: Number.MAX_SAFE_INTEGER,
      active: true,
    });
  }

  return entries;
}

function appendMessageEntry(entries: ChatEntry[], event: CodexEvent) {
  const text = event.textPreview || "";
  if (!text) return;
  const role = event.eventType === "message.user" ? "user" : "assistant";
  const sequence = event.sequence || 0;
  const key = messageKey(event, role);
  const existing = findLastMessage(entries, key) || (!payloadItemId(event) ? findLastMessageByTurnRole(entries, event.turnId, role) : null);
  const sameTurnMessage = findLastMessageByTurnRole(entries, event.turnId, role);
  if (!existing && sameTurnMessage && sameText(sameTurnMessage.text, text)) {
    sameTurnMessage.createdAt = event.createdAt || sameTurnMessage.createdAt;
    sameTurnMessage.sequence = sequence || sameTurnMessage.sequence;
    return;
  }
  if (!existing) {
    entries.push({ kind: "message", key, role, text, threadId: event.threadId, turnId: event.turnId, createdAt: event.createdAt, sequence });
    return;
  }
  existing.createdAt = event.createdAt || existing.createdAt;
  existing.sequence = sequence || existing.sequence;
  if (isCompletedMessage(event)) {
    existing.text = chooseCompletedText(existing.text, text);
    return;
  }
  if (isDeltaMessage(event)) {
    existing.text = appendDelta(existing.text, text);
    return;
  }
  if (!existing.text.includes(text)) {
    entries.push({ kind: "message", key: `${key}-${sequence}`, role, text, threadId: event.threadId, turnId: event.turnId, createdAt: event.createdAt, sequence });
  }
}

function statusEntryForEvent(event: CodexEvent, turnStartedAt: Map<string, string>): ChatEntry | null {
  const type = event.eventType || "";
  if (type === "turn.started") {
    return {
      kind: "status",
      key: eventKey(event),
      label: "开始处理",
      detail: eventText(event),
      tone: "neutral",
      createdAt: event.createdAt,
      sequence: event.sequence || 0,
      active: true,
    };
  }
  if (type === "turn.completed") {
    return {
      kind: "status",
      key: eventKey(event),
      label: `已处理${durationLabel(turnStartedAt.get(event.turnId || ""), event.createdAt)}`,
      detail: eventText(event),
      tone: "good",
      createdAt: event.createdAt,
      sequence: event.sequence || 0,
    };
  }
  if (type === "turn.failed" || type === "diagnostic.error") {
    return statusFromEvent(event, "danger");
  }
  if (type === "turn.cancelled" || type === "diagnostic.warning" || type === "approval.requested") {
    return statusFromEvent(event, "warn");
  }
  if (type.startsWith("command.") || type.startsWith("tool.") || type.startsWith("file_change.") || type === "plan.updated" || type === "diff.updated" || type === "approval.resolved") {
    return statusFromEvent(event, type.endsWith(".completed") || type === "approval.resolved" ? "good" : "neutral");
  }
  if (type.startsWith("thread.")) return null;
  return event.textPreview ? statusFromEvent(event, "neutral") : null;
}

function appendReasoningEntry(entries: ChatEntry[], event: CodexEvent) {
  const text = event.textPreview || "";
  const key = [event.turnId || "thread", "reasoning", payloadItemId(event) || "message"].join(":");
  const existing = findLastStatus(entries, key) || (!payloadItemId(event) ? findLastStatusByPrefix(entries, `${event.turnId || "thread"}:reasoning:`) : null);
  if (!existing) {
    entries.push({
      kind: "status",
      key,
      label: "正在思考",
      detail: text,
      tone: "neutral",
      createdAt: event.createdAt,
      sequence: event.sequence || 0,
      active: isDeltaMessage(event),
    });
    return;
  }
  existing.createdAt = event.createdAt || existing.createdAt;
  existing.sequence = event.sequence || existing.sequence;
  existing.active = isDeltaMessage(event);
  if (isCompletedMessage(event)) {
    existing.detail = chooseCompletedText(existing.detail || "", text);
  } else if (isDeltaMessage(event)) {
    existing.detail = appendDelta(existing.detail || "", text);
  } else if (text && !(existing.detail || "").includes(text)) {
    existing.detail = text;
  }
}

function statusFromEvent(event: CodexEvent, tone: StatusTone): ChatEntry {
  return {
    kind: "status",
    key: eventKey(event),
    label: codexEventTitle(event.eventType),
    detail: eventText(event),
    tone,
    createdAt: event.createdAt,
    sequence: event.sequence || 0,
    active: isDeltaMessage(event),
  };
}

function artifactsForEvent(event: CodexEvent): ChatArtifact[] {
  const payload = event.payload || {};
  const item = recordValue(payload.item);
  const itemType = event.itemType || firstString(item?.type);
  const refs: string[] = [];
  if (itemType === "imageView") {
    addRef(refs, firstString(item?.path, item?.url, item?.src, item?.filePath, item?.file));
  }
  collectImageCollection(payload.images, refs);
  collectImageCollection(payload.artifacts, refs);
  collectImageCollection(payload.attachments, refs);
  if (!refs.length || !event.threadId) return [];
  return refs
    .map((ref, index) => artifactFromRef(event, ref, index))
    .filter((artifact): artifact is ChatArtifact => Boolean(artifact));
}

function artifactFromRef(event: CodexEvent, ref: string, index: number): ChatArtifact | null {
  const value = ref.trim();
  if (!value) return null;
  const isRemote = /^https?:\/\//i.test(value);
  const isLocal = value.startsWith("/") || /^file:\/\//i.test(value);
  if (!isRemote && !isLocal) return null;
  const src = isRemote
    ? value
    : `/api/codex/threads/${encodeURIComponent(event.threadId || "")}/artifacts/content?path=${encodeURIComponent(value)}`;
  return {
    id: `${payloadItemId(event) || event.id || event.sequence || "artifact"}-${index}`,
    type: "image",
    src,
    path: isLocal ? value : undefined,
    label: imageLabel(value),
  };
}

function collectImageCollection(value: unknown, refs: string[]) {
  if (Array.isArray(value)) {
    for (const item of value) collectImageCollection(item, refs);
    return;
  }
  const record = recordValue(value);
  if (!record) {
    if (typeof value === "string" && looksLikeImageRef(value)) addRef(refs, value);
    return;
  }
  const type = firstString(record.type, record.kind, record.mediaType, record.contentType).toLowerCase();
  const likelyImage = type.includes("image") || looksLikeImageRef(firstString(record.path, record.url, record.src, record.filePath, record.file));
  if (!likelyImage) return;
  addRef(refs, firstString(record.path, record.url, record.src, record.filePath, record.file));
}

function addRef(refs: string[], value: string) {
  const trimmed = value.trim();
  if (trimmed && !refs.includes(trimmed)) refs.push(trimmed);
}

function looksLikeImageRef(value: string): boolean {
  return /\.(png|jpe?g|gif|webp)(?:[?#].*)?$/i.test(value.trim());
}

function imageLabel(value: string): string {
  const clean = value.split(/[?#]/)[0]?.replace(/^file:\/\//i, "") || value;
  const name = clean.split("/").filter(Boolean).pop();
  return name || "image";
}

function findLastStatus(entries: ChatEntry[], key: string) {
  for (let index = entries.length - 1; index >= 0; index--) {
    const entry = entries[index];
    if (entry.kind === "status" && entry.key === key) return entry;
  }
  return null;
}

function findLastStatusByPrefix(entries: ChatEntry[], prefix: string) {
  for (let index = entries.length - 1; index >= 0; index--) {
    const entry = entries[index];
    if (entry.kind === "status" && entry.key.startsWith(prefix)) return entry;
  }
  return null;
}

function findLastMessage(entries: ChatEntry[], key: string) {
  for (let index = entries.length - 1; index >= 0; index--) {
    const entry = entries[index];
    if (entry.kind === "message" && entry.key === key) return entry;
  }
  return null;
}

function findLastMessageByTurnRole(entries: ChatEntry[], turnId: string | undefined, role: "user" | "assistant") {
  for (let index = entries.length - 1; index >= 0; index--) {
    const entry = entries[index];
    if (entry.kind === "message" && entry.role === role && entry.turnId === turnId) return entry;
  }
  return null;
}

function messageKey(event: CodexEvent, role: "user" | "assistant") {
  return [event.turnId || "thread", role, payloadItemId(event) || "message"].join(":");
}

function payloadItemId(event: CodexEvent): string {
  const payload = event.payload || {};
  const direct = firstString(payload.itemId, payload.item_id, payload.callId, payload.call_id);
  if (direct) return direct;
  const item = payload.item;
  if (item && typeof item === "object") {
    const record = item as Record<string, unknown>;
    return firstString(record.id, record.itemId, record.item_id, record.callId, record.call_id);
  }
  return "";
}

function recordValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value;
  }
  return "";
}

function sameText(left: string, right: string): boolean {
  return left.trim().replace(/\s+/g, " ") === right.trim().replace(/\s+/g, " ");
}

function chooseCompletedText(current: string, completed: string): string {
  if (!current) return completed;
  if (completed.length >= current.length) return completed;
  if (current.includes(completed)) return current;
  return completed;
}

function appendDelta(current: string, delta: string): string {
  if (!current) return delta;
  if (current.endsWith(delta)) return current;
  return `${current}${delta}`;
}

function isDeltaMessage(event: CodexEvent): boolean {
  return Boolean(event.codexMethod?.toLowerCase().includes("delta"));
}

function isCompletedMessage(event: CodexEvent): boolean {
  return event.codexMethod === "item/completed" || event.codexMethod === "item.completed";
}

function eventText(event: CodexEvent): string {
  return event.textPreview || "";
}

function eventKey(event: CodexEvent): string {
  return event.id || `${event.eventType || "event"}-${event.sequence || 0}`;
}

function eventIdentity(event: CodexEvent): string {
  return event.id || `seq:${event.sequence || 0}`;
}

function sortEvents(events: CodexEvent[]): CodexEvent[] {
  return events.slice().sort((left, right) => (left.sequence || 0) - (right.sequence || 0));
}

function durationLabel(start?: string, end?: string): string {
  if (!start || !end) return "";
  const startTime = new Date(start).getTime();
  const endTime = new Date(end).getTime();
  if (Number.isNaN(startTime) || Number.isNaN(endTime) || endTime <= startTime) return "";
  const seconds = Math.max(1, Math.round((endTime - startTime) / 1000));
  return ` ${seconds}s`;
}
