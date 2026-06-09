import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexSettings, CodexStatus, CodexThread, CodexWorkspace } from "../../app/types";
import { Button, EmptyState, Notice, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { codexThreadStatusLabel, formatDate } from "../../domain/labels";
import { ThreadWorkspace } from "./ThreadWorkspace";

export function ChatsTab({ actions, status, onStatusChange }: { actions: AppActions; status?: CodexStatus; onStatusChange: () => void }) {
  const [chats, setChats] = useState<CodexThread[]>([]);
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
  const [scratchWorkspaceId, setScratchWorkspaceId] = useState("");
  const [activeId, setActiveId] = useState("");
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      const [chatResp, workspaceResp, settingsResp] = await Promise.all([
        actions.api<{ items?: CodexThread[] }>("/api/codex/chats"),
        actions.api<{ items?: CodexWorkspace[] }>("/api/codex/workspaces"),
        actions.api<{ settings?: CodexSettings }>("/api/codex/settings"),
      ]);
      setChats(chatResp.items || []);
      setWorkspaces(workspaceResp.items || []);
      setScratchWorkspaceId(settingsResp.settings?.scratchWorkspaceId || "");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }, [actions]);

  useEffect(() => {
    void load();
  }, [load]);

  const activeChat = chats.find((chat) => chat.id === activeId) || null;
  const scratchReady = Boolean(scratchWorkspaceId);

  async function createChat() {
    setCreating(true);
    try {
      const response = await actions.api<{ thread: CodexThread }>("/api/codex/chats", { method: "POST", csrf: actions.csrf, body: { title: "" } });
      await load();
      setActiveId(response.thread.id);
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="grid grid-cols-[280px_minmax(0,1fr)] gap-4 max-lg:grid-cols-1">
      <Panel
        actions={<Button disabled={!scratchReady || creating} onClick={() => void createChat()}>{creating ? "创建中" : "新建 Chat"}</Button>}
        subtitle="只读 research/planning 会话，绑定受控 scratch workspace，不写文件、不联网。"
        title="Chats"
      >
        {!scratchReady ? (
          <Notice tone="warn">尚未配置 scratch workspace。请到 Codex Settings 选择一个受控 workspace 后再新建 Chat。</Notice>
        ) : null}
        {chats.length ? (
          <div className="mt-3 grid gap-2">
            {chats.map((chat) => (
              <button
                aria-pressed={chat.id === activeId}
                className={`rounded-lg border px-3 py-2 text-left text-sm transition ${chat.id === activeId ? "border-[var(--accent)] bg-[var(--surface-strong)]" : "border-[var(--line)] bg-[var(--surface-soft)] hover:border-[var(--muted)]"}`}
                key={chat.id}
                onClick={() => setActiveId(chat.id)}
                type="button"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate">{chat.title || chat.id}</span>
                  <Pill tone={chat.status === "failed" ? "danger" : chat.status === "running" || chat.status === "queued" ? "warn" : "neutral"}>{codexThreadStatusLabel(chat.status)}</Pill>
                </div>
                <p className="muted mt-1 mb-0 truncate text-xs">{formatDate(chat.updatedAt) || ""}</p>
              </button>
            ))}
          </div>
        ) : scratchReady ? (
          <EmptyState title="暂无 Chat" body="新建 Chat 后可在受控 scratch workspace 内做只读问答、计划与命令草案。" />
        ) : null}
      </Panel>

      {activeChat ? (
        <ThreadWorkspace key={activeChat.id} actions={actions} status={status} thread={activeChat} workspaces={workspaces} onStatusChange={onStatusChange} onThreadChange={load} />
      ) : (
        <Panel subtitle="Chats 适合整理计划、解释错误、写命令草案，不用于执行生产变更。" title="选择或新建 Chat">
          <EmptyState title="未选择 Chat" body={scratchReady ? "从左侧选择一个 Chat，或新建一个只读会话。" : "请先在 Codex Settings 配置 scratch workspace。"} />
        </Panel>
      )}
    </div>
  );
}
