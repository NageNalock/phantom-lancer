import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexStatus } from "../app/types";
import { Button, Pill, SubTabs } from "../components/ui";
import { codexAppServerStateLabel } from "../domain/labels";
import { shouldHandleQueryLinkClick, useQueryParamState } from "../hooks/useQueryParamState";
import { ThreadsTab } from "./codex/ThreadsTab";
import { WorkspacesTab } from "./codex/WorkspacesTab";
import { ApprovalsTab } from "./codex/ApprovalsTab";
import { DiagnosticsTab } from "./codex/DiagnosticsTab";
import { CodexSettingsTab } from "./codex/CodexSettingsTab";
import { AutomationsTab } from "./codex/AutomationsTab";
import { CapabilitiesTab } from "./codex/CapabilitiesTab";
import { NotificationsTab } from "./codex/NotificationsTab";

type CodexTab = "threads" | "workspaces" | "inbox" | "runtime";
type CodexInboxTab = "approvals" | "notifications" | "automations";
type CodexRuntimeTab = "diagnostics" | "capabilities" | "settings";

const TABS: Array<{ id: CodexTab; label: string }> = [
  { id: "threads", label: "会话" },
  { id: "workspaces", label: "工作区" },
  { id: "inbox", label: "收件箱" },
  { id: "runtime", label: "运行时" },
];

const INBOX_TABS: Array<{ id: CodexInboxTab; label: string }> = [
  { id: "approvals", label: "审批" },
  { id: "notifications", label: "通知" },
  { id: "automations", label: "自动化" },
];

const RUNTIME_TABS: Array<{ id: CodexRuntimeTab; label: string }> = [
  { id: "diagnostics", label: "运行诊断" },
  { id: "capabilities", label: "能力" },
  { id: "settings", label: "设置" },
];
const CODEX_TAB_IDS: CodexTab[] = TABS.map((item) => item.id);
const CODEX_INBOX_TAB_IDS: CodexInboxTab[] = INBOX_TABS.map((item) => item.id);
const CODEX_RUNTIME_TAB_IDS: CodexRuntimeTab[] = RUNTIME_TABS.map((item) => item.id);
const CODEX_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexInbox", "codexRuntime"];
const CODEX_INBOX_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexRuntime"];
const CODEX_RUNTIME_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexInbox"];

export function CodexView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab, tabHref] = useQueryParamState<CodexTab>("codex", CODEX_TAB_IDS, "threads", { clearKeys: CODEX_CLEAR_KEYS });
  const [inboxTab, setInboxTab, inboxTabHref] = useQueryParamState<CodexInboxTab>("codexInbox", CODEX_INBOX_TAB_IDS, "approvals", { clearKeys: CODEX_INBOX_CLEAR_KEYS });
  const [runtimeTab, setRuntimeTab, runtimeTabHref] = useQueryParamState<CodexRuntimeTab>("codexRuntime", CODEX_RUNTIME_TAB_IDS, "diagnostics", { clearKeys: CODEX_RUNTIME_CLEAR_KEYS });
  const [status, setStatus] = useState<CodexStatus | undefined>(data.dashboard.codex);
  const [focusThreadId, setFocusThreadId] = useState("");
  const [drawer, setDrawer] = useState<Exclude<CodexTab, "threads"> | null>(tab === "threads" ? null : tab);

  const refreshStatus = useCallback(async () => {
    try {
      const next = await actions.api<CodexStatus>("/api/codex/status");
      setStatus(next);
    } catch {
      // status errors are surfaced inside each tab; keep the shell quiet.
    }
  }, [actions]);

  useEffect(() => {
    void refreshStatus();
  }, [refreshStatus]);
  useEffect(() => {
    setDrawer(tab === "threads" ? null : tab);
  }, [tab]);

  const appServer = status?.appServer;
  const appServerTone = appServer?.state === "running" ? "good" : appServer?.state === "failed" ? "danger" : appServer?.state === "starting" ? "warn" : "neutral";
  const pending = status?.pendingApprovals || 0;
  const runtime = status?.runtime;

  return (
    <section className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-3 p-4">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] pb-3">
        <div className="min-w-0">
          <h1 className="m-0 text-sm font-semibold">Codex 工作台</h1>
          <p className="muted mt-1 mb-0 text-xs">选择项目和 thread，在同一个界面里对话、审批、review diff 和预览。</p>
        </div>
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
          {runtime?.running ? <Pill tone="good">运行 {runtime.running}</Pill> : null}
          {runtime?.waitingApproval ? <Pill tone="warn">待审批 {runtime.waitingApproval}</Pill> : null}
          {runtime?.queued ? <Pill tone="warn">队列 {runtime.queued}</Pill> : null}
          {runtime?.failed ? <Pill tone="danger">失败 {runtime.failed}</Pill> : null}
          <Pill tone={appServerTone}>app-server {codexAppServerStateLabel(appServer?.state)}</Pill>
          <WorkbenchButton href={tabHref("workspaces")} label="项目" onClick={() => setTab("workspaces")} />
          <WorkbenchButton
            badge={pending}
            href={tabHref("inbox")}
            label="收件箱"
            onClick={() => setTab("inbox")}
          />
          <WorkbenchButton href={tabHref("runtime")} label="运行时" onClick={() => setTab("runtime")} />
        </div>
      </div>

      <ThreadsTab actions={actions} focusThreadId={focusThreadId} status={status} onStatusChange={refreshStatus} />
      {drawer ? (
        <CodexDrawer
          title={drawerTitle(drawer)}
          onClose={() => {
            setDrawer(null);
            setTab("threads");
          }}
        >
          {drawer === "workspaces" ? <WorkspacesTab actions={actions} onChange={refreshStatus} /> : null}
          {drawer === "inbox" ? (
            <div className="grid gap-4">
              <SubTabs
                activeId={inboxTab}
                className="border-b-0 pb-0"
                onChange={(id) => setInboxTab(id as CodexInboxTab)}
                tabs={INBOX_TABS.map((item) => ({
                  ...item,
                  href: inboxTabHref(item.id),
                  badge: item.id === "approvals" && pending > 0 ? (
                    <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{pending}</span>
                  ) : undefined,
                }))}
              />
              {inboxTab === "approvals" ? <ApprovalsTab actions={actions} onChange={refreshStatus} /> : null}
              {inboxTab === "notifications" ? <NotificationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
              {inboxTab === "automations" ? <AutomationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
            </div>
          ) : null}
          {drawer === "runtime" ? (
            <div className="grid gap-4">
              <SubTabs
                activeId={runtimeTab}
                className="border-b-0 pb-0"
                onChange={(id) => setRuntimeTab(id as CodexRuntimeTab)}
                tabs={RUNTIME_TABS.map((item) => ({ ...item, href: runtimeTabHref(item.id) }))}
              />
              {runtimeTab === "diagnostics" ? <DiagnosticsTab actions={actions} status={status} onChange={refreshStatus} /> : null}
              {runtimeTab === "capabilities" ? <CapabilitiesTab actions={actions} /> : null}
              {runtimeTab === "settings" ? <CodexSettingsTab actions={actions} onChange={refreshStatus} /> : null}
            </div>
          ) : null}
        </CodexDrawer>
      ) : null}
    </section>
  );
}

function WorkbenchButton({ badge, href, label, onClick }: { badge?: number; href: string; label: string; onClick: () => void }) {
  return (
    <a
      className="inline-flex min-h-8 items-center gap-1.5 rounded-md border border-[var(--line)] bg-[var(--surface)] px-2.5 text-xs text-[var(--muted-strong)] no-underline transition hover:border-[var(--line-strong)] hover:bg-[var(--surface-strong)]"
      href={href}
      onClick={(event) => {
        if (!shouldHandleQueryLinkClick(event)) return;
        event.preventDefault();
        onClick();
      }}
    >
      {label}
      {badge ? <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-[var(--warn)]">{badge}</span> : null}
    </a>
  );
}

function CodexDrawer({ children, onClose, title }: { children: ReactNode; onClose: () => void; title: string }) {
  const drawerRef = useRef<HTMLElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    const previousFocus = document.activeElement;
    closeButtonRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      if (event.key !== "Tab" || !drawerRef.current) return;
      const focusable = Array.from(
        drawerRef.current.querySelectorAll<HTMLElement>("button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])"),
      ).filter((element) => !element.hasAttribute("disabled"));
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      if (previousFocus instanceof HTMLElement) previousFocus.focus();
    };
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-40 overscroll-contain">
      <button aria-label="关闭 Codex 辅助面板" className="absolute inset-0 h-full w-full bg-black/[0.04]" onClick={onClose} type="button" />
      <aside aria-modal="true" className="absolute top-4 right-4 bottom-4 flex w-[min(920px,calc(100vw-2rem))] flex-col overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]" ref={drawerRef} role="dialog">
        <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] px-4 py-3">
          <div className="min-w-0">
            <h2 className="m-0 text-sm font-semibold">{title}</h2>
            <p className="muted mt-1 mb-0 text-xs">低频配置、跨 thread 汇总和运行诊断保留在辅助入口。</p>
          </div>
          <Button className="h-8 min-h-8 px-2 text-xs" onClick={onClose} ref={closeButtonRef}>关闭</Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-4">{children}</div>
      </aside>
    </div>
  );
}

function drawerTitle(tab: Exclude<CodexTab, "threads">): string {
  return tab === "workspaces" ? "项目" : tab === "inbox" ? "收件箱" : "运行时";
}
