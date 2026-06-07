import type { EventRecord } from "../app/types";
import { eventLabel, sessionStatusLabel } from "../domain/labels";

type UserBlock = { kind: "user"; id?: string; text: string; at?: string };
type AssistantBlock = { kind: "assistant"; id?: string; text: string; at?: string };
type ToolBlock = { kind: "tool"; id?: string; title: string; text?: string; meta?: string; at?: string };
type StatusBlock = { kind: "status" | "warn" | "error"; id?: string; title: string; text?: string; meta?: string; at?: string };

export type ConversationBlock = UserBlock | AssistantBlock | ToolBlock | StatusBlock;

export const SESSION_EVENT_NAMES = [
  "session.created",
  "session.failed",
  "thread.attached",
  "thread.resumed",
  "thread/started",
  "thread/status/changed",
  "thread/archived",
  "thread.archived.local",
  "thread.settings.updated.local",
  "thread/settings/updated",
  "thread/tokenUsage/updated",
  "thread.forked.local",
  "thread.rollback.requested",
  "thread.compact.requested",
  "thread/compacted",
  "review.started.local",
  "codex.approval.requested",
  "codex.approval.resolved",
  "codex.approval.expired",
  "codex.approval.interrupted",
  "codex.approval.unsupported",
  "git.action.completed",
  "composer.status",
  "turn.submitted",
  "turn.steered",
  "turn.start.failed",
  "turn.steer.failed",
  "turn.interrupt.requested",
  "turn/started",
  "turn/completed",
  "turn/diff/updated",
  "turn/plan/updated",
  "item/started",
  "item/completed",
  "rawResponseItem/completed",
  "item/agentMessage/delta",
  "item/commandExecution/outputDelta",
  "item/fileChange/outputDelta",
  "item/fileChange/patchUpdated",
  "item/reasoning/summaryTextDelta",
  "error",
  "warning",
] as const;

interface CodexItem {
  id?: string;
  type?: string;
  content?: Array<{ text?: string; path?: string; url?: string }>;
  text?: string;
  command?: string;
  status?: string;
  aggregatedOutput?: string;
}

export function buildConversationBlocks(events: EventRecord[]): ConversationBlock[] {
  const blocks: ConversationBlock[] = [];
  const userItems = new Set<string>();
  const assistantById = new Map<string, AssistantBlock>();
  const toolById = new Map<string, ToolBlock>();

  for (const event of events) {
    const payload = event.payload || {};
    const item = toItem(payload.item);

    if ((event.type === "item/started" || event.type === "item/completed") && item.type === "userMessage" && item.id && !userItems.has(item.id)) {
      userItems.add(item.id);
      blocks.push({ kind: "user", id: item.id, text: userInputText(item.content), at: event.createdAt });
      continue;
    }

    if (event.type === "item/agentMessage/delta") {
      const id = textValue(payload.itemId) || `assistant-${blocks.length}`;
      let block = assistantById.get(id);
      if (!block) {
        block = { kind: "assistant", id, text: "", at: event.createdAt };
        assistantById.set(id, block);
        blocks.push(block);
      }
      block.text += textValue(payload.delta) || "";
      continue;
    }

    if ((event.type === "item/started" || event.type === "item/completed") && item.type === "agentMessage" && item.id) {
      let block = assistantById.get(item.id);
      if (!block) {
        block = { kind: "assistant", id: item.id, text: "", at: event.createdAt };
        assistantById.set(item.id, block);
        blocks.push(block);
      }
      if (item.text) block.text = item.text;
      continue;
    }

    if ((event.type === "item/started" || event.type === "item/completed") && item.type === "commandExecution" && item.id) {
      let block = toolById.get(item.id);
      if (!block) {
        block = { kind: "tool", id: item.id, title: "命令执行", text: "", meta: item.command || "", at: event.createdAt };
        toolById.set(item.id, block);
        blocks.push(block);
      }
      block.text = item.aggregatedOutput || block.text;
      continue;
    }

    if (event.type === "item/commandExecution/outputDelta") {
      const id = textValue(payload.itemId) || `cmd-${blocks.length}`;
      let block = toolById.get(id);
      if (!block) {
        block = { kind: "tool", id, title: "命令输出", text: "", meta: "", at: event.createdAt };
        toolById.set(id, block);
        blocks.push(block);
      }
      block.text += textValue(payload.delta) || textValue(payload.chunk) || "";
      continue;
    }

    if (["error", "warning", "session.failed", "turn.start.failed", "turn.steer.failed"].includes(event.type)) {
      blocks.push({
        kind: event.type === "warning" ? "warn" : "error",
        title: eventLabel(event.type),
        text: errorText(payload),
        at: event.createdAt,
      });
      continue;
    }

    if (
      [
        "turn/started",
        "turn/completed",
        "thread/status/changed",
        "turn.submitted",
        "turn.steered",
        "thread.resumed",
        "thread.attached",
        "thread.settings.updated.local",
        "thread/settings/updated",
        "thread/tokenUsage/updated",
        "thread.forked.local",
        "thread.rollback.requested",
        "thread.compact.requested",
        "thread/compacted",
        "review.started.local",
        "codex.approval.requested",
        "codex.approval.resolved",
        "codex.approval.expired",
        "codex.approval.interrupted",
        "codex.approval.unsupported",
        "git.action.completed",
        "composer.status",
      ].includes(event.type)
    ) {
      blocks.push({ kind: "status", title: eventLabel(event.type), text: statusText(event.type, payload), at: event.createdAt });
      continue;
    }

    if (["item/fileChange/patchUpdated", "turn/diff/updated", "turn/plan/updated"].includes(event.type)) {
      blocks.push({ kind: "tool", title: eventLabel(event.type), text: JSON.stringify(payload, null, 2), at: event.createdAt });
    }
  }

  return blocks;
}

function toItem(value: unknown): CodexItem {
  return value && typeof value === "object" ? (value as CodexItem) : {};
}

function textValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function userInputText(content: CodexItem["content"]): string {
  if (!Array.isArray(content)) return "";
  return content.map((item) => item.text || item.path || item.url || "").filter(Boolean).join("\n");
}

function errorText(payload: Record<string, unknown>): string {
  const error = payload.error as { message?: string } | undefined;
  if (error?.message) return error.message;
  if (typeof payload.message === "string") return payload.message;
  if (typeof payload.additionalDetails === "string") return payload.additionalDetails;
  return JSON.stringify(payload, null, 2);
}

function statusText(type: string, payload: Record<string, unknown>): string {
  if (typeof payload.promptPreview === "string") return payload.promptPreview;
  if (typeof payload.summary === "string") return payload.summary;
  if (typeof payload.model === "string") return `模型：${payload.model || "-"} / 审批：${payload.approvalPolicy || "-"}`;
  if (payload.tokenUsage && typeof payload.tokenUsage === "object") {
    const usage = payload.tokenUsage as { total?: { totalTokens?: number }; modelContextWindow?: number | null };
    const total = usage.total?.totalTokens || 0;
    const windowSize = usage.modelContextWindow || 0;
    return windowSize ? `Context：${total.toLocaleString()} / ${windowSize.toLocaleString()}` : `Tokens：${total.toLocaleString()}`;
  }
  if (typeof payload.turnId === "string") return `回合：${payload.turnId}`;
  if (typeof payload.threadId === "string") return `Thread：${payload.threadId}`;
  if (payload.status) return sessionStatusLabel(statusLabelValue(payload.status));
  return eventLabel(type);
}

export function statusLabelValue(value: unknown): string {
  if (!value) return "idle";
  if (typeof value === "string") return value;
  if (typeof value === "object" && "type" in value && typeof value.type === "string") return value.type;
  return "idle";
}
