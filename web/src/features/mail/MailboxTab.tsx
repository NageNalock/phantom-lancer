import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData } from "../../app/types";
import {
  // ---- Folders / Messages / Search / Index / Sync / Compose RPC ----
  mailFolderList,
  mailFolderUpsert,
  mailFolderDelete,
  mailMessageList,
  mailMessageGet,
  mailMessageDelete,
  mailMessageMove,
  mailMessageUpdateFlags,
  mailMessageSearch,
  mailIndexHealthList,
  mailIndexHealthReset,
  mailImapSyncStart,
  mailImapSyncPause,
  mailImapSyncResume,
  mailImapSyncReset,
  mailComposeSend,
  mailDraftSave,
  mailDraftDelete,
  friendlyError,
  type MailFolder,
  type MailMessage,
  type MailMessagePart,
  type MailSearchQuery,
  type MailSearchResp,
  type MailIndexHealth,
  type ComposeSendReq,
  type DraftSaveReq,
  type AttachmentInfo,
} from "../../api/client";
import { Button, EmptyState, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";

// =============================================================================
// Props contract
// =============================================================================

export interface MailboxTabProps {
  actions: AppActions;
  reload: () => Promise<unknown> | void;
  data: AppData;
}

// =============================================================================
// Small helpers
// =============================================================================

// Default FTS scope dropdown values.
const SCOPE_OPTIONS = [
  { value: "one", label: "当前账户" },
  { value: "all", label: "全部账户" },
  { value: "attachments", label: "附件内容" },
] as const;

type SyncState = "idle" | "syncing" | "paused" | "error" | "reset";

// Return a pill tone + label for a given IMAP sync state.
function describeSyncState(state?: SyncState | string): { tone: "good" | "warn" | "danger" | "neutral"; label: string } {
  switch (state) {
    case "syncing":
      return { tone: "good", label: "同步中" };
    case "paused":
      return { tone: "warn", label: "已暂停" };
    case "error":
      return { tone: "danger", label: "出错" };
    case "reset":
      return { tone: "warn", label: "已重置" };
    default:
      return { tone: "neutral", label: "待机" };
  }
}

// Group flat part rows into headers (one per message_id) so the middle column
// can render one list row per email rather than one per MIME part.
function groupPartsByMessage(parts: MailMessagePart[]): Array<{
  messageId: string;
  folderId: string;
  subject: string;
  from: string;
  date: string;
  preview: string;
  unseen: boolean;
  hasAttachment: boolean;
  sizeBytes: number;
}> {
  const byId = new Map<string, {
    messageId: string;
    folderId: string;
    subject: string;
    from: string;
    date: string;
    preview: string;
    unseen: boolean;
    hasAttachment: boolean;
    sizeBytes: number;
  }>();
  for (const p of parts) {
    const entry = byId.get(p.message_id) ?? {
      messageId: p.message_id,
      folderId: p.folder_id,
      subject: "",
      from: "",
      date: p.created_at ?? "",
      preview: "",
      unseen: false,
      hasAttachment: false,
      sizeBytes: 0,
    };
    entry.sizeBytes += p.size_bytes ?? 0;
    if (p.is_attachment) entry.hasAttachment = true;
    if (p.content_type?.startsWith("text/plain") && !entry.preview && p.decoded_text) {
      entry.preview = p.decoded_text.slice(0, 140).replace(/\s+/g, " ").trim();
    }
    // HEADERS parts are parsed by the backend into DecodedText as a JSON
    // envelope.  Since the FE doesn't parse it today, try to pick up the
    // Subject from any inline part metadata we already have.
    if (p.created_at && !entry.date) entry.date = p.created_at;
    // The FE currently lacks a per-message Seen flag because the backend
    // aggregates parts; the preview logic keeps unseen=false conservatively.
    byId.set(p.message_id, entry);
  }
  return Array.from(byId.values()).sort((a, b) => (a.date < b.date ? 1 : -1));
}

// Split a multi-recipient textarea value into an array of trimmed,
// non-empty addresses.  Accepts both comma and newline as delimiters.
function splitAddrs(raw?: string): string[] {
  if (!raw) return [];
  return raw
    .split(/[\n,，]/g)
    .map((s) => s.trim())
    .filter(Boolean);
}

// =============================================================================
// Compose modal
// =============================================================================

interface ComposeState {
  open: boolean;
  draftId?: string;
  from: string;
  to: string;
  cc: string;
  bcc: string;
  subject: string;
  body: string;
  // Optional reply/forward context for the backend.
  replyToMessageId?: string;
  forwardMessageId?: string;
}

const EMPTY_COMPOSE: ComposeState = {
  open: false,
  from: "",
  to: "",
  cc: "",
  bcc: "",
  subject: "",
  body: "",
};

interface ComposeModalProps {
  state: ComposeState;
  setState: (s: ComposeState) => void;
  fromOptions: string[];
  loading: boolean;
  actions: AppActions;
  onSend: (s: ComposeState) => Promise<void>;
  onDraft: (s: ComposeState) => Promise<void>;
}

function ComposeModal({ state, setState, fromOptions, loading, actions, onSend, onDraft }: ComposeModalProps) {
  if (!state.open) return null;
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-[rgba(16,18,22,0.56)] p-4"
      onClick={() => setState({ ...state, open: false })}
    >
      <section
        className="w-full max-w-3xl overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="撰写邮件"
      >
        <div className="border-b border-[var(--line)] bg-[var(--surface-soft)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">撰写邮件</h2>
        </div>
        <div className="grid gap-3 p-4 text-sm">
          <Field label="发件人 (From)">
            <select
              className="input"
              value={state.from}
              onChange={(e) => setState({ ...state, from: e.target.value })}
            >
              <option value="">— 选择发件地址 —</option>
              {fromOptions.map((addr) => (
                <option key={addr} value={addr}>{addr}</option>
              ))}
            </select>
          </Field>
          <Field label="收件人 (To)" help="每行或逗号分隔一个邮箱地址">
            <textarea
              className="input min-h-[52px] resize-y"
              placeholder="a@example.com, b@example.com"
              value={state.to}
              onChange={(e) => setState({ ...state, to: e.target.value })}
            />
          </Field>
          <Field label="抄送 (Cc)" help="可选，每行或逗号分隔">
            <textarea
              className="input min-h-[40px] resize-y"
              placeholder="copy@example.com"
              value={state.cc}
              onChange={(e) => setState({ ...state, cc: e.target.value })}
            />
          </Field>
          <Field label="密送 (Bcc)" help="可选，每行或逗号分隔">
            <textarea
              className="input min-h-[40px] resize-y"
              placeholder="bcc@example.com"
              value={state.bcc}
              onChange={(e) => setState({ ...state, bcc: e.target.value })}
            />
          </Field>
          <Field label="主题 (Subject)">
            <input
              className="input"
              placeholder="邮件主题"
              value={state.subject}
              onChange={(e) => setState({ ...state, subject: e.target.value })}
            />
          </Field>
          <Field label="正文 (Text)" help="附件与 HTML 富文本将在后续版本中开放">
            <textarea
              className="input min-h-[220px] resize-y"
              placeholder="邮件正文（纯文本）"
              value={state.body}
              onChange={(e) => setState({ ...state, body: e.target.value })}
            />
          </Field>
        </div>
        <div className="flex items-center justify-between gap-2 border-t border-[var(--line)] bg-[var(--surface-soft)] px-4 py-3">
          <small className="muted">
            {state.draftId ? <span>草稿 ID: <code className="mono">{state.draftId}</code></span> : <span>尚未保存草稿</span>}
          </small>
          <div className="flex items-center gap-2">
            <Button disabled={loading} onClick={() => setState({ ...state, open: false })}>取消</Button>
            <Button disabled={loading || !state.from} onClick={() => onDraft(state)}>保存草稿</Button>
            <Button
              tone="primary"
              disabled={loading || !state.from || splitAddrs(state.to).length === 0}
              onClick={() => onSend(state)}
            >
              {loading ? "发送中…" : "发送"}
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}

// =============================================================================
// Folder rename / create overlay (small inline modal)
// =============================================================================

interface FolderFormState {
  open: boolean;
  mode: "create" | "rename";
  id?: string;
  name: string;
}

function FolderFormModal({
  state,
  setState,
  accountId,
  loading,
  actions,
  onSubmit,
}: {
  state: FolderFormState;
  setState: (s: FolderFormState) => void;
  accountId: string;
  loading: boolean;
  actions: AppActions;
  onSubmit: (patch: Partial<MailFolder>) => Promise<void>;
  _unused?: AppActions;
}) {
  if (!state.open) return null;
  const _ = actions;
  void _;
  void accountId;
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-[rgba(16,18,22,0.56)] p-4"
      onClick={() => setState({ ...state, open: false })}
    >
      <section
        className="w-full max-w-md overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={state.mode === "create" ? "新建文件夹" : "重命名文件夹"}
      >
        <div className="border-b border-[var(--line)] bg-[var(--surface-soft)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold">{state.mode === "create" ? "新建文件夹" : "重命名文件夹"}</h2>
        </div>
        <div className="grid gap-3 p-4 text-sm">
          <Field label="文件夹名称">
            <input
              className="input"
              placeholder="例如：归档 / 客户工单"
              value={state.name}
              onChange={(e) => setState({ ...state, name: e.target.value })}
            />
          </Field>
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-4 py-3">
          <Button disabled={loading} onClick={() => setState({ ...state, open: false })}>取消</Button>
          <Button tone="primary" disabled={loading || !state.name.trim()} onClick={() => onSubmit({ id: state.id, name: state.name.trim() })}>
            确认
          </Button>
        </div>
      </section>
    </div>
  );
}

// =============================================================================
// Main tab
// =============================================================================

export function MailboxTab({ actions, reload, data }: MailboxTabProps) {
  // ---- Danger confirm hook (used by delete message, reset sync, reset index)
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  // ---- Account selection (drives folders + search scope)
  const accounts = data.mail?.accounts ?? [];
  const fromAddresses = useMemo(() => {
    const addrs = new Set<string>();
    for (const a of accounts) {
      if (a.address) addrs.add(a.address);
      if ((a as { email?: string }).email) addrs.add((a as { email: string }).email);
    }
    return Array.from(addrs).sort();
  }, [accounts]);

  const [accountId, setAccountId] = useState<string>(accounts[0]?.id ?? "");
  useEffect(() => {
    if (!accountId && accounts[0]) setAccountId(accounts[0].id);
  }, [accounts, accountId]);

  // ---- Folders
  const [folders, setFolders] = useState<MailFolder[]>([]);
  const [folderLoading, setFolderLoading] = useState(false);
  const [activeFolderId, setActiveFolderId] = useState<string>("");

  // ---- Index health summary (shown under folder list)
  const [indexHealth, setIndexHealth] = useState<MailIndexHealth[]>([]);
  const [indexLoading, setIndexLoading] = useState(false);

  // ---- Sync state (drives the top sync pill)
  const [syncState, setSyncState] = useState<SyncState>("idle");

  // ---- Middle column: message rows + selection + pagination
  const [messages, setMessages] = useState<MailMessagePart[]>([]);
  const [messageLoading, setMessageLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState<string>("");
  const [totalCount, setTotalCount] = useState<number>(0);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [unseenOnly, setUnseenOnly] = useState<0 | 1>(0);
  const [hasAttachmentOnly, setHasAttachmentOnly] = useState(false);

  // ---- Filters (toolbar pills).  "Has attachment" is client-side because the
  // backend list row aggregates attachment per message_id; the aggregation is
  // done in groupPartsByMessage below.  "Unseen only" toggled via query param
  // (0|1) against the backend so server can post-filter when the Seen flag is
  // tracked by the backend later.
  const middleRows = useMemo(() => {
    const grouped = groupPartsByMessage(messages);
    if (hasAttachmentOnly) return grouped.filter((r) => r.hasAttachment);
    return grouped;
  }, [messages, hasAttachmentOnly]);

  // ---- Right column: selected message detail
  const [detail, setDetail] = useState<MailMessage | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [activeMessageId, setActiveMessageId] = useState<string>("");

  // ---- Search
  const [searchQuery, setSearchQuery] = useState("");
  const [searchScope, setSearchScope] = useState<"one" | "all" | "attachments">("one");
  const [searchResults, setSearchResults] = useState<MailSearchResp | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);

  // ---- Compose modal
  const [compose, setCompose] = useState<ComposeState>(EMPTY_COMPOSE);
  const [composeLoading, setComposeLoading] = useState(false);

  // ---- Folder form modal
  const [folderForm, setFolderForm] = useState<FolderFormState>({ open: false, mode: "create", name: "" });
  const [folderFormLoading, setFolderFormLoading] = useState(false);

  // ---- Context-menu target (folder rename / delete menu)
  const [menuFolderId, setMenuFolderId] = useState<string | null>(null);

  // ===========================================================================
  // Data loaders
  // ===========================================================================

  async function loadFolders() {
    if (!accountId) return;
    setFolderLoading(true);
    try {
      const r = await mailFolderList(accountId);
      const list = r.items ?? [];
      setFolders(list);
      if (!activeFolderId || !list.find((f) => f.id === activeFolderId)) {
        const inbox = list.find((f) => f.role === "inbox") ?? list[0];
        if (inbox) setActiveFolderId(inbox.id);
      }
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setFolderLoading(false);
    }
  }

  async function loadIndexHealth() {
    setIndexLoading(true);
    try {
      const r = await mailIndexHealthList();
      setIndexHealth(r.items ?? []);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setIndexLoading(false);
    }
  }

  async function loadMessages() {
    if (!activeFolderId) {
      setMessages([]);
      setTotalCount(0);
      setNextCursor("");
      return;
    }
    setMessageLoading(true);
    try {
      const r = await mailMessageList(activeFolderId, { limit: 50, cursor: nextCursor || undefined, unseen_only: unseenOnly });
      const incoming = r.items ?? [];
      setMessages((prev) => nextCursor ? [...prev, ...incoming] : incoming);
      setTotalCount(r.total ?? 0);
      setNextCursor(r.next_cursor ?? "");
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setMessageLoading(false);
    }
  }

  async function loadDetail(messageId: string) {
    if (!messageId) return;
    setDetailLoading(true);
    setActiveMessageId(messageId);
    try {
      const r = await mailMessageGet(messageId);
      setDetail(r);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
      setDetail(null);
    } finally {
      setDetailLoading(false);
    }
  }

  // ---- Initial / account-changed hydration
  useEffect(() => {
    void loadFolders();
    void loadIndexHealth();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId]);

  // ---- Folder change: reset cursor + selection + detail + messages
  useEffect(() => {
    setMessages([]);
    setNextCursor("");
    setSelectedIds(new Set());
    setDetail(null);
    setActiveMessageId("");
    void loadMessages();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeFolderId, unseenOnly]);

  // ---- Keep folder list refreshed when user performs folder mutations
  // (handled inline via explicit calls to loadFolders after each mutation).

  // ===========================================================================
  // Folder actions
  // ===========================================================================

  function startCreateFolder() {
    if (!accountId) {
      actions.setToast("请先选择一个账户后再新建文件夹", "warn");
      return;
    }
    setFolderForm({ open: true, mode: "create", name: "" });
  }

  function startRenameFolder(f: MailFolder) {
    setMenuFolderId(null);
    setFolderForm({ open: true, mode: "rename", id: f.id, name: f.name });
  }

  async function submitFolderForm(patch: Partial<MailFolder>) {
    if (!accountId) return;
    setFolderFormLoading(true);
    try {
      await mailFolderUpsert(accountId, patch);
      actions.setToast(folderForm.mode === "create" ? "文件夹已创建" : "文件夹已重命名", "good");
      setFolderForm({ ...folderForm, open: false, name: "" });
      await loadFolders();
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setFolderFormLoading(false);
    }
  }

  async function deleteFolder(f: MailFolder) {
    setMenuFolderId(null);
    if (f.role) {
      actions.setToast(`系统文件夹 ${f.name} 受保护，无法删除`, "warn");
      return;
    }
    const ok = await confirmDanger({
      title: "删除文件夹",
      objectName: f.name,
      body: <>将删除文件夹及其索引条目。文件夹中的已同步邮件不会从上游 IMAP 服务器移除。</>,
      impact: [
        `文件夹 ID ${f.id}`,
        "下游邮件视图中该文件夹将不再显示",
        "全文索引中归属于该文件夹的条目将被清理",
      ],
      recovery: "可在 30 天内通过备份恢复（尚未实现）。",
      confirmationText: "DELETE",
      confirmationLabel: "请输入 DELETE 以确认删除",
    });
    if (!ok) return;
    try {
      await mailFolderDelete(f.id);
      actions.setToast("文件夹已删除", "good");
      if (activeFolderId === f.id) setActiveFolderId("");
      await loadFolders();
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  // ===========================================================================
  // Selection + bulk actions (middle column)
  // ===========================================================================

  function toggleSelect(messageId: string) {
    setSelectedIds((prev) => {
      const n = new Set(prev);
      if (n.has(messageId)) n.delete(messageId); else n.add(messageId);
      return n;
    });
  }

  function selectAllVisible() {
    if (selectedIds.size === middleRows.length && middleRows.length > 0) {
      setSelectedIds(new Set());
      return;
    }
    setSelectedIds(new Set(middleRows.map((r) => r.messageId)));
  }

  async function bulkMark(asRead: boolean) {
    if (selectedIds.size === 0) {
      actions.setToast("请先选择至少一封邮件", "warn");
      return;
    }
    const add = asRead ? ["\\Seen"] : [];
    const remove = asRead ? [] : ["\\Seen"];
    let ok = true;
    for (const id of selectedIds) {
      try {
        await mailMessageUpdateFlags(id, { add, remove });
      } catch (e) {
        ok = false;
        actions.setToast(friendlyError(e), "warn");
      }
    }
    if (ok) actions.setToast(asRead ? "已标记为已读" : "已标记为未读", "good");
    setSelectedIds(new Set());
  }

  async function bulkMove(destFolderId: string) {
    if (selectedIds.size === 0) {
      actions.setToast("请先选择至少一封邮件", "warn");
      return;
    }
    if (!destFolderId) return;
    let ok = true;
    for (const id of selectedIds) {
      try {
        await mailMessageMove(id, destFolderId);
      } catch (e) {
        ok = false;
        actions.setToast(friendlyError(e), "warn");
      }
    }
    if (ok) actions.setToast("已移动", "good");
    setSelectedIds(new Set());
    setNextCursor("");
    await loadMessages();
  }

  async function bulkDelete() {
    if (selectedIds.size === 0) {
      actions.setToast("请先选择至少一封邮件", "warn");
      return;
    }
    const ok = await confirmDanger({
      title: "删除所选邮件",
      body: <>将永久删除 {selectedIds.size} 封邮件。该操作不可撤销，且会同步更新全文索引。</>,
      impact: [
        `删除 ${selectedIds.size} 封邮件及其附件索引`,
        "全文搜索中将不再出现被删除邮件",
        "IMAP 上游服务器中的原始邮件不会被触达（仅本地同步副本）",
      ],
      recovery: "备份保留策略允许在保留期内还原单封邮件（尚未实现）。",
      confirmationText: "DELETE",
      confirmationLabel: "请输入 DELETE 以确认永久删除",
    });
    if (!ok) return;
    let done = 0;
    for (const id of selectedIds) {
      try {
        await mailMessageDelete(id);
        done++;
      } catch (e) {
        actions.setToast(friendlyError(e), "warn");
      }
    }
    actions.setToast(`已删除 ${done} / ${selectedIds.size} 封邮件`, done === selectedIds.size ? "good" : "warn");
    setSelectedIds(new Set());
    setDetail(null);
    setNextCursor("");
    await loadMessages();
  }

  // ===========================================================================
  // Search
  // ===========================================================================

  async function runSearch() {
    if (!searchQuery.trim()) {
      setSearchResults(null);
      return;
    }
    const q: MailSearchQuery = {
      query: searchQuery.trim(),
      account_ids: searchScope === "one" && accountId ? [accountId] : undefined,
      scope: searchScope,
      limit: 50,
      offset: 0,
    };
    setSearchLoading(true);
    try {
      const r = await mailMessageSearch(accountId || "all", q);
      setSearchResults(r);
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
      setSearchResults(null);
    } finally {
      setSearchLoading(false);
    }
  }

  function clearSearch() {
    setSearchQuery("");
    setSearchResults(null);
  }

  // ===========================================================================
  // Sync control
  // ===========================================================================

  async function doSyncStart() {
    if (!accountId) return;
    try {
      await mailImapSyncStart(accountId);
      setSyncState("syncing");
      actions.setToast("已请求 IMAP 同步", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }
  async function doSyncPause() {
    if (!accountId) return;
    try {
      await mailImapSyncPause(accountId);
      setSyncState("paused");
      actions.setToast("已暂停同步", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }
  async function doSyncResume() {
    if (!accountId) return;
    try {
      await mailImapSyncResume(accountId);
      setSyncState("syncing");
      actions.setToast("已恢复同步", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }
  async function doSyncReset() {
    if (!accountId) return;
    const ok = await confirmDanger({
      title: "重置 IMAP 同步状态",
      objectName: `账户 ${accountId}`,
      body: <>清空本地 UID / MODSEQ 水线并强制下一次启动进行全量重新同步。此操作不会删除已下载的邮件内容。</>,
      impact: [
        "下一次启动 IMAP 同步将按全量重新拉取",
        "预计会消耗更多服务器带宽与本地磁盘写入",
        "期间可能出现重复展示相同邮件的短暂窗口",
      ],
      recovery: "如需回滚，请联系管理员恢复上次备份的 mail_accounts 行。",
      confirmationText: "RESET",
    });
    if (!ok) return;
    try {
      await mailImapSyncReset(accountId);
      setSyncState("reset");
      actions.setToast("已重置 IMAP 同步状态", "good");
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function doIndexReset(healthAccountId: string) {
    const ok = await confirmDanger({
      title: "重建全文搜索索引",
      objectName: `账户 ${healthAccountId}`,
      body: <>将清空所有已索引邮件并重新构建 FTS5 索引。索引重建期间搜索结果可能为空或不完整。</>,
      impact: [
        "立即删除该账户已索引的 FTS5 行",
        "索引状态切为 rebuilding，同步完成前搜索会返回不全结果",
        "可能占用大量 CPU / IO",
      ],
      recovery: "若索引损坏，重建后可自动恢复；无需其他操作。",
      confirmationText: "REINDEX",
    });
    if (!ok) return;
    try {
      await mailIndexHealthReset(healthAccountId);
      actions.setToast("已请求重建索引", "good");
      await loadIndexHealth();
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    }
  }

  // ===========================================================================
  // Compose / Draft handlers
  // ===========================================================================

  function startCompose(prefill?: Partial<ComposeState>) {
    const from = fromAddresses[0] ?? "";
    setCompose({ ...EMPTY_COMPOSE, open: true, from, ...(prefill ?? {}) });
  }

  function startReply(replyAll: boolean) {
    if (!detail) {
      actions.setToast("请先选择一封邮件", "warn");
      return;
    }
    const subject = detail.subject?.startsWith("Re:") ? detail.subject : `Re: ${detail.subject ?? ""}`;
    const to = replyAll
      ? [detail.from ?? "", ...(detail.to ?? [])].filter(Boolean).join(", ")
      : detail.from ?? "";
    const cc = replyAll ? (detail.cc ?? []).join(", ") : "";
    const lines = (detail.message_body_text ?? "")
      .split("\n")
      .map((l) => `> ${l}`)
      .join("\n");
    const body = `\n\n---- 原始邮件 ----\nFrom: ${detail.from ?? ""}\nDate: ${detail.date ?? ""}\nSubject: ${detail.subject ?? ""}\n\n${lines}`;
    startCompose({
      from: fromAddresses.includes(compose.from) ? compose.from : fromAddresses[0] ?? "",
      to, cc, subject, body,
      replyToMessageId: detail.message_id ?? detail.id,
    });
  }

  function startForward() {
    if (!detail) {
      actions.setToast("请先选择一封邮件", "warn");
      return;
    }
    const subject = detail.subject?.startsWith("Fwd:") ? detail.subject : `Fwd: ${detail.subject ?? ""}`;
    const lines = (detail.message_body_text ?? "")
      .split("\n")
      .map((l) => `> ${l}`)
      .join("\n");
    const body = `\n\n---- 转发邮件 ----\nFrom: ${detail.from ?? ""}\nDate: ${detail.date ?? ""}\nSubject: ${detail.subject ?? ""}\n\n${lines}`;
    startCompose({
      from: fromAddresses.includes(compose.from) ? compose.from : fromAddresses[0] ?? "",
      to: "", cc: "", subject, body,
      forwardMessageId: detail.message_id ?? detail.id,
    });
  }

  async function handleComposeSend(s: ComposeState) {
    if (!accountId) return;
    setComposeLoading(true);
    try {
      const req: ComposeSendReq = {
        account_id: accountId,
        from: s.from,
        to: splitAddrs(s.to),
        cc: splitAddrs(s.cc),
        bcc: splitAddrs(s.bcc),
        subject: s.subject,
        body_text: s.body,
        reply_to_message_id: s.replyToMessageId,
        forward_message_id: s.forwardMessageId,
      };
      const resp = await mailComposeSend(req);
      actions.setToast(`已入队发送，任务 ID：${resp.job_id}`, "good");
      setCompose({ ...s, open: false });
      if (s.draftId) {
        try { await mailDraftDelete(s.draftId); } catch { /* best-effort */ }
      }
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setComposeLoading(false);
    }
  }

  async function handleComposeDraft(s: ComposeState) {
    if (!accountId) return;
    setComposeLoading(true);
    try {
      const req: DraftSaveReq = {
        draft_id: s.draftId,
        account_id: accountId,
        from: s.from,
        to: splitAddrs(s.to),
        cc: splitAddrs(s.cc),
        bcc: splitAddrs(s.bcc),
        subject: s.subject,
        body_text: s.body,
        reply_to_message_id: s.replyToMessageId,
        forward_message_id: s.forwardMessageId,
      };
      const resp = await mailDraftSave(req);
      actions.setToast(`草稿已保存 (${resp.draft_id})`, "good");
      setCompose({ ...s, draftId: resp.draft_id });
    } catch (e) {
      actions.setToast(friendlyError(e), "warn");
    } finally {
      setComposeLoading(false);
    }
  }

  // ===========================================================================
  // Folder list helpers
  // ===========================================================================

  const inboxFolder = folders.find((f) => f.role === "inbox");
  const systemFolders = folders.filter((f) => !!f.role);
  const customFolders = folders.filter((f) => !f.role);

  function folderBadge(f: MailFolder): { count?: string; tone?: "neutral" | "good" | "warn" } {
    if (f.role === "inbox" && (f.unseen_messages ?? 0) > 0) {
      return { count: String(f.unseen_messages), tone: "good" };
    }
    if ((f.unseen_messages ?? 0) > 0) {
      return { count: String(f.unseen_messages), tone: "neutral" };
    }
    if (f.message_count ?? 0 > 0) {
      return { count: String(f.message_count) };
    }
    return {};
  }

  // ===========================================================================
  // Render
  // ===========================================================================

  const syncDesc = describeSyncState(syncState);
  const myIndex = accountId ? indexHealth.find((h) => h.account_id === accountId) : undefined;

  return (
    <div className="grid gap-3">
      {/* ======= Top toolbar (account selector, search, compose, sync) ====== */}
      <Panel>
        <div className="flex flex-wrap items-center gap-3">
          <Field label="账户">
            <select
              className="input"
              value={accountId}
              onChange={(e) => { setAccountId(e.target.value); setDetail(null); setActiveMessageId(""); }}
            >
              {accounts.length === 0 ? <option value="">（尚未配置账户）</option> : null}
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.address ?? (a as { email?: string }).email ?? a.id}
                </option>
              ))}
            </select>
          </Field>

          <div className="flex min-w-0 flex-1 items-center gap-2">
            <input
              className="input flex-1"
              placeholder="全文搜索（主题 / 发件人 / 正文 / 附件文本）"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") void runSearch(); }}
            />
            <select
              className="input w-[140px]"
              value={searchScope}
              onChange={(e) => setSearchScope(e.target.value as "one" | "all" | "attachments")}
            >
              {SCOPE_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
            <Button tone="primary" onClick={() => void runSearch()} disabled={searchLoading || !accountId}>
              {searchLoading ? "搜索中…" : "搜索"}
            </Button>
            {searchResults ? <Button onClick={clearSearch}>清空</Button> : null}
          </div>

          <Pill tone={syncDesc.tone}>同步：{syncDesc.label}</Pill>
          <Button disabled={!accountId || syncState === "syncing"} onClick={() => void doSyncStart()}>启动同步</Button>
          <Button disabled={!accountId || syncState !== "syncing"} onClick={() => void doSyncPause()}>暂停</Button>
          <Button disabled={!accountId || syncState !== "paused"} onClick={() => void doSyncResume()}>恢复</Button>
          <Button tone="danger" disabled={!accountId} onClick={() => void doSyncReset()}>重置同步</Button>

          <Button tone="primary" onClick={() => startCompose()} disabled={fromAddresses.length === 0}>
            撰写
          </Button>
        </div>
      </Panel>

      {/* ==================== Search results overlay ======================= */}
      {searchResults ? (
        <Panel title="搜索结果" subtitle={`${searchResults.total} 条命中，耗时 ${searchResults.duration_ms}ms`}
          actions={<small className="muted">查询：<code className="mono">{searchResults.query}</code></small>}>
          {searchResults.items.length === 0 ? (
            <EmptyState title="未找到匹配的邮件" body="请尝试更换关键词、放宽作用域，或在索引重建完成后重试。" />
          ) : (
            <ul className="m-0 list-none divide-y divide-[var(--line)] border border-[var(--line)] rounded-md">
              {searchResults.items.map((r) => (
                <li
                  key={r.id}
                  className={`flex items-start gap-3 px-3 py-2 cursor-pointer hover:bg-[var(--surface-soft)] ${activeMessageId === r.message_id ? "bg-[var(--surface-soft)]" : ""}`}
                  onClick={() => void loadDetail(r.message_id)}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center justify-between gap-2">
                      <strong className="truncate">{r.subject || "（无主题）"}</strong>
                      <small className="muted shrink-0">{r.date}</small>
                    </div>
                    <div className="flex items-center justify-between gap-2 text-xs">
                      <span className="truncate muted">
                        <span className="text-[var(--text)]">{r.from}</span>
                        {" → "}
                        <span>{r.to}</span>
                      </span>
                      <span className="mono muted shrink-0">{r.size_bytes} B</span>
                    </div>
                    <p className="m-0 mt-1 text-xs leading-relaxed" dangerouslySetInnerHTML={{ __html: r.snippet }} />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      ) : null}

      {/* ====================== Three-column mail view ===================== */}
      {!searchResults ? (
        <div className="grid gap-3" style={{ gridTemplateColumns: "minmax(180px, 1fr) minmax(260px, 2fr) minmax(380px, 5fr)" }}>
          {/* ---------------- Column 1: Folders ------------------------------ */}
          <Panel
            title="文件夹"
            subtitle={folderLoading ? "加载中…" : `${folders.length} 个文件夹`}
            actions={
              <div className="flex items-center gap-1">
                <Button onClick={startCreateFolder} disabled={!accountId}>新建</Button>
                <Button onClick={() => void loadFolders()} disabled={folderLoading || !accountId}>刷新</Button>
              </div>
            }
          >
            {accounts.length === 0 ? (
              <EmptyState title="尚未配置邮箱账户" body="请在『邮箱账户』页面先创建至少一个账户，再回到此处浏览邮件。" />
            ) : folders.length === 0 && !folderLoading ? (
              <EmptyState title="暂无文件夹" body="点击右上『新建』创建自定义文件夹，或点击『启动同步』以自动拉取上游 IMAP 文件夹结构。" />
            ) : (
              <ul className="m-0 list-none border border-[var(--line)] rounded-md overflow-hidden">
                {[...systemFolders, ...customFolders].map((f) => {
                  const badge = folderBadge(f);
                  return (
                    <li key={f.id} className="relative">
                      <div
                        onClick={() => setActiveFolderId(f.id)}
                        className={`flex cursor-pointer items-center justify-between gap-2 px-3 py-2 hover:bg-[var(--surface-soft)] ${activeFolderId === f.id ? "bg-[var(--surface-soft)]" : ""}`}
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <span className={`w-2 h-2 rounded-full ${f.role ? "bg-[var(--primary)]" : "bg-[var(--muted-strong)]"}`} />
                          <span className={`truncate text-sm ${f.role === "inbox" ? "font-semibold" : ""}`}>{f.name}</span>
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          {badge.count ? <Pill tone={badge.tone ?? "neutral"}>{badge.count}</Pill> : null}
                          <button
                            type="button"
                            aria-label={`${f.name} 更多`}
                            className="rounded px-1 text-[var(--muted-strong)] hover:bg-[var(--surface)] hover:text-[var(--text)]"
                            onClick={(e) => {
                              e.stopPropagation();
                              setMenuFolderId((prev) => (prev === f.id ? null : f.id));
                            }}
                          >⋮</button>
                        </div>
                      </div>
                      {menuFolderId === f.id ? (
                        <div className="absolute right-1 top-full z-20 mt-1 w-40 overflow-hidden rounded border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] text-xs"
                          onMouseLeave={() => setMenuFolderId(null)}
                        >
                          <button
                            type="button"
                            disabled={!!f.role}
                            onClick={() => startRenameFolder(f)}
                            className="block w-full px-3 py-2 text-left hover:bg-[var(--surface-soft)] disabled:opacity-40 disabled:cursor-not-allowed"
                          >重命名</button>
                          <button
                            type="button"
                            disabled={!!f.role}
                            onClick={() => void deleteFolder(f)}
                            className="block w-full px-3 py-2 text-left text-[var(--danger)] hover:bg-[var(--danger-soft)] disabled:opacity-40 disabled:cursor-not-allowed"
                          >删除</button>
                          {f.role ? (
                            <p className="m-0 border-t border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 muted">系统文件夹受保护</p>
                          ) : null}
                        </div>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}

            {/* ---- Index health summary ---- */}
            <div className="mt-4 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
              <div className="mb-1 flex items-center justify-between">
                <strong>搜索索引健康</strong>
                {inboxFolder ? null : null}
                <small className="muted">{indexLoading ? "加载中" : `${indexHealth.length} 个账户`}</small>
              </div>
              {indexHealth.length === 0 ? (
                <p className="m-0 muted">暂无索引数据。启动首次同步后将自动创建。</p>
              ) : (
                <ul className="m-0 mt-2 list-none divide-y divide-[var(--line)]">
                  {indexHealth.map((h) => {
                    const label = h.status === "ok" ? "健康" :
                      h.status === "rebuilding" ? "重建中" :
                      h.status === "stale" ? "陈旧" :
                      h.status === "error" ? "错误" : "待索引";
                    const tone: "good" | "warn" | "danger" | "neutral" =
                      h.status === "ok" ? "good" :
                      h.status === "rebuilding" ? "neutral" :
                      h.status === "error" ? "danger" : "warn";
                    return (
                      <li key={h.id ?? h.account_id} className="flex items-center justify-between gap-2 py-1">
                        <div className="min-w-0">
                          <Pill tone={tone}>{label}</Pill>
                          <span className="ml-2 truncate text-[var(--muted-strong)]">账户 {h.account_id}</span>
                        </div>
                        <div className="flex shrink-0 items-center gap-2">
                          <span className="mono muted">{h.indexed_messages}/{h.total_messages}</span>
                          <Button onClick={() => void doIndexReset(h.account_id)} tone="danger" disabled={h.status === "rebuilding"}>重置</Button>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              )}
              {myIndex && myIndex.status === "rebuilding" ? (
                <Notice tone="warn">
                  <strong>当前账户正在重建索引。</strong> 在此期间搜索结果可能不完整；完成后将自动切换为健康。
                </Notice>
              ) : null}
            </div>
          </Panel>

          {/* ----------------- Column 2: Messages ----------------------------- */}
          <Panel
            title={activeFolderId ? (folders.find((f) => f.id === activeFolderId)?.name ?? "邮件") : "邮件"}
            subtitle={messageLoading ? "加载中…" : `${middleRows.length} / ${totalCount} 封`}
            actions={
              <div className="flex items-center gap-1">
                <span className="cursor-pointer select-none" onClick={() => setUnseenOnly((v) => (v === 1 ? 0 : 1))}>
                  <Pill tone={unseenOnly ? "good" : "neutral"}>仅未读</Pill>
                </span>
                <span className="cursor-pointer select-none" onClick={() => setHasAttachmentOnly((v) => !v)}>
                  <Pill tone={hasAttachmentOnly ? "good" : "neutral"}>仅含附件</Pill>
                </span>
                <Button onClick={selectAllVisible} disabled={middleRows.length === 0}>
                  {selectedIds.size === middleRows.length && middleRows.length > 0 ? "取消全选" : "全选"}
                </Button>
                <Button onClick={() => void loadMessages()} disabled={messageLoading}>刷新</Button>
              </div>
            }
          >
            {/* ---- Bulk action bar ---- */}
            {selectedIds.size > 0 ? (
              <div className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs">
                <span>已选 {selectedIds.size} 封：</span>
                <Button onClick={() => void bulkMark(true)}>标记已读</Button>
                <Button onClick={() => void bulkMark(false)}>标记未读</Button>
                <select
                  className="input"
                  defaultValue=""
                  onChange={(e) => { const v = e.target.value; e.currentTarget.value = ""; void bulkMove(v); }}
                >
                  <option value="">移动到…</option>
                  {folders.filter((f) => f.id !== activeFolderId).map((f) => (
                    <option key={f.id} value={f.id}>{f.name}</option>
                  ))}
                </select>
                <Button tone="danger" onClick={() => void bulkDelete()}>删除</Button>
              </div>
            ) : null}

            {!activeFolderId ? (
              <EmptyState title="选择一个文件夹" body="点击左侧任一文件夹以查看其中邮件。" />
            ) : middleRows.length === 0 && !messageLoading ? (
              <EmptyState title="暂无邮件" body={unseenOnly || hasAttachmentOnly ? "当前筛选条件下无匹配邮件，请尝试切换筛选。" : "文件夹为空，点击右上『启动同步』拉取上游邮件。"} />
            ) : (
              <ul className="m-0 list-none divide-y divide-[var(--line)] border border-[var(--line)] rounded-md max-h-[520px] overflow-auto">
                {middleRows.map((r) => (
                  <li
                    key={r.messageId}
                    onClick={() => void loadDetail(r.messageId)}
                    className={`flex cursor-pointer items-start gap-2 px-3 py-2 hover:bg-[var(--surface-soft)] ${activeMessageId === r.messageId ? "bg-[var(--surface-soft)]" : ""}`}
                  >
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={selectedIds.has(r.messageId)}
                      onChange={(e) => { e.stopPropagation(); toggleSelect(r.messageId); }}
                      onClick={(e) => e.stopPropagation()}
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center justify-between gap-2">
                        <strong className={`truncate text-sm ${r.unseen ? "text-[var(--text)]" : "text-[var(--muted-strong)] font-normal"}`}>
                          {r.subject || "（无主题）"}
                        </strong>
                        <small className="muted shrink-0">{r.date?.slice(0, 16) ?? ""}</small>
                      </div>
                      <div className="flex items-center justify-between gap-2 text-xs">
                        <span className="truncate muted">{r.from || "—"}</span>
                        <div className="flex shrink-0 items-center gap-1">
                          {r.hasAttachment ? <Pill>附件</Pill> : null}
                          <span className="mono muted">{r.sizeBytes > 0 ? `${(r.sizeBytes / 1024).toFixed(1)}K` : ""}</span>
                        </div>
                      </div>
                      {r.preview ? <p className="m-0 mt-1 truncate text-xs text-[var(--muted-strong)]">{r.preview}</p> : null}
                    </div>
                  </li>
                ))}
              </ul>
            )}

            {/* ---- Pagination ---- */}
            <div className="mt-2 flex items-center justify-between text-xs">
              <small className="muted">
                {messageLoading ? "加载中…" : nextCursor ? "滚动到底加载下一页" : "已到达末尾"}
              </small>
              {nextCursor ? (
                <Button onClick={() => void loadMessages()} disabled={messageLoading}>加载更多</Button>
              ) : null}
            </div>
          </Panel>

          {/* ------------------ Column 3: Detail / Preview -------------------- */}
          <Panel
            title={detail ? (detail.subject || "（无主题）") : "邮件预览"}
            subtitle={detailLoading ? "加载中…" : detail ? (detail.date ? detail.date.slice(0, 19) : "") : "选择左侧一封邮件以查看详情"}
            actions={
              detail ? (
                <div className="flex items-center gap-1">
                  <Button onClick={() => startReply(false)}>回复</Button>
                  <Button onClick={() => startReply(true)}>全部回复</Button>
                  <Button onClick={() => startForward()}>转发</Button>
                  <Button tone="danger" onClick={async () => {
                    const ok = await confirmDanger({
                      title: "删除该邮件",
                      body: <>将从本地同步副本中永久删除该邮件。</>,
                      impact: ["全文索引将同步移除该邮件", "上游 IMAP 服务器中的原始邮件不会被触达"],
                      recovery: "若误删，可通过备份恢复（尚未实现）。",
                      confirmationText: "DELETE",
                    });
                    if (!ok) return;
                    try {
                      await mailMessageDelete(activeMessageId);
                      actions.setToast("邮件已删除", "good");
                      setDetail(null);
                      setActiveMessageId("");
                      setNextCursor("");
                      await loadMessages();
                    } catch (e) {
                      actions.setToast(friendlyError(e), "warn");
                    }
                  }}>删除</Button>
                </div>
              ) : undefined
            }
          >
            {!detail ? (
              <EmptyState title="尚未选择邮件" body="从中间列表选择一封邮件以预览内容、下载附件或进行回复/转发。" />
            ) : (
              <div className="grid gap-3 text-sm">
                <div className="grid gap-1 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div><span className="muted">From：</span><code className="mono">{detail.from ?? "—"}</code></div>
                      <div><span className="muted">To：</span><code className="mono">{(detail.to ?? []).join(", ") || "—"}</code></div>
                      {(detail.cc ?? []).length > 0 ? (
                        <div><span className="muted">Cc：</span><code className="mono">{(detail.cc ?? []).join(", ")}</code></div>
                      ) : null}
                    </div>
                    <div className="text-right shrink-0">
                      <div className="mono text-xs text-[var(--muted-strong)]">{detail.date ?? ""}</div>
                      {(detail.attachments_count ?? detail.attachments?.length ?? 0) > 0 ? (
                        <div className="mt-1"><Pill>{detail.attachments_count ?? detail.attachments?.length ?? 0} 个附件</Pill></div>
                      ) : null}
                    </div>
                  </div>
                </div>

                {/* ---- Body preview ---- */}
                {detail.message_body_text ? (
                  detail.message_body_text.length >= 10_000 ? (
                    <Notice tone="warn">
                      <strong>正文超过 10 KB。</strong> 此处仅显示前 10 KB；若需查看完整原文，请使用邮件客户端或点击下载原始文本。
                    </Notice>
                  ) : null
                ) : null}
                <pre className="m-0 max-h-[420px] overflow-auto whitespace-pre-wrap break-words rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-xs leading-relaxed font-[inherit]">
                  {detail.message_body_text
                    ? detail.message_body_text.length > 10_000
                      ? detail.message_body_text.slice(0, 10_000) + "\n……（已截断）"
                      : detail.message_body_text
                    : "（该邮件无纯文本正文，后续版本将提供 HTML 预览）"}
                </pre>

                {/* ---- Attachments ---- */}
                {(detail.attachments ?? []).length > 0 ? (
                  <div>
                    <div className="mb-2 text-xs muted">附件（下载尚未实现，按钮为占位）：</div>
                    <ul className="m-0 list-none divide-y divide-[var(--line)] border border-[var(--line)] rounded-md">
                      {(detail.attachments as AttachmentInfo[]).map((a) => (
                        <li key={`${a.part_id ?? a.index}-${a.filename}`} className="flex items-center justify-between gap-2 px-3 py-2">
                          <div className="min-w-0">
                            <div className="truncate text-sm">{a.filename || `attachment-${a.index}`}</div>
                            <div className="mono text-xs muted">{a.content_type} · {a.size_bytes} B</div>
                          </div>
                          <div className="flex shrink-0 items-center gap-2">
                            <Pill tone={a.stored ? "good" : "warn"}>{a.stored ? "已缓存" : "未缓存"}</Pill>
                            <Button disabled title="附件下载功能尚未启用">下载</Button>
                          </div>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}

                {/* ---- MIME parts debug (collapsed-ish style) ---- */}
                {(detail.parts ?? []).length > 1 ? (
                  <details className="rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
                    <summary className="cursor-pointer muted">MIME 结构（{detail.parts?.length ?? 0} 个部分）</summary>
                    <ul className="m-0 mt-2 list-none pl-4 grid gap-1">
                      {(detail.parts ?? []).map((p) => (
                        <li key={p.id} className="flex items-center justify-between gap-2">
                          <code className="mono truncate">{p.content_type}{p.filename ? ` · ${p.filename}` : ""}</code>
                          <span className="mono muted shrink-0">{p.size_bytes} B</span>
                        </li>
                      ))}
                    </ul>
                  </details>
                ) : null}
              </div>
            )}
          </Panel>
        </div>
      ) : null}

      {/* ======================= Modal stack ================================== */}
      <FolderFormModal
        state={folderForm}
        setState={setFolderForm}
        accountId={accountId}
        loading={folderFormLoading}
        actions={actions}
        onSubmit={submitFolderForm}
      />
      <ComposeModal
        state={compose}
        setState={setCompose}
        fromOptions={fromAddresses}
        loading={composeLoading}
        actions={actions}
        onSend={handleComposeSend}
        onDraft={handleComposeDraft}
      />
      {dangerConfirmDialog}
    </div>
  );
}
