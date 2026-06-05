import type { AppActions } from "../app/App";
import type { AppData, CodexSession, CodexTab, EventRecord, MainTab, V2RayExport, Workspace } from "../app/types";
import { Button, Pill } from "../components/ui";
import { CODEX_TABS, NAV_ITEMS, v2rayStateLabel } from "../domain/labels";
import { ActivityView } from "./ActivityView";
import { CodexGatewayView } from "./CodexGatewayView";
import { CodexView } from "./CodexView";
import { DashboardView } from "./DashboardView";
import { ImagesView } from "./ImagesView";
import { LogsView } from "./LogsView";
import { PermissionsView } from "./PermissionsView";
import { ProjectsView } from "./ProjectsView";
import { SettingsView } from "./SettingsView";
import { V2RayView } from "./V2RayView";

export function AppShell({
  actions,
  activeCodexTab,
  activeSession,
  activeSessionId,
  activeSessionWorkspace,
  activeTab,
  data,
  logout,
  selectedWorkspaceId,
  sessionEvents,
  v2rayExport,
  v2rayExportOpen,
}: {
  actions: AppActions;
  activeCodexTab: CodexTab;
  activeSession: CodexSession | null;
  activeSessionId: string;
  activeSessionWorkspace: Workspace | null;
  activeTab: MainTab;
  data: AppData;
  logout: () => Promise<void>;
  selectedWorkspaceId: string;
  sessionEvents: EventRecord[];
  v2rayExport: unknown;
  v2rayExportOpen: boolean;
}) {
  const activeMeta = activeTab === "codex" ? CODEX_TABS.find((item) => item.id === activeCodexTab) : NAV_ITEMS.find((item) => item.id === activeTab);
  const codex = data.codexStatus;
  const v2ray = data.v2ray.status || data.dashboard.v2ray;
  const images = data.images.status || data.dashboard.images;

  return (
    <div className="grid min-h-dvh grid-cols-[232px_minmax(0,1fr)] gap-3 p-3 max-lg:grid-cols-1">
      <a className="fixed left-4 top-4 z-40 -translate-y-24 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm focus:translate-y-0" href="#mainContent">
        跳到主要内容
      </a>
      <aside className="rounded-xl border border-[var(--line)] bg-[var(--rail)] p-3 max-lg:static">
        <div className="mb-5 flex items-center gap-3 px-1">
          <div className="grid h-10 w-10 place-items-center rounded-lg border border-[var(--line-strong)] bg-[var(--surface)] font-mono text-xs font-bold text-[var(--accent)]">PL</div>
          <div>
            <strong className="block text-sm">My Server</strong>
            <span className="muted text-xs">Phantom Lancer</span>
          </div>
        </div>
        <nav className="grid gap-1">
          {NAV_ITEMS.map((item) => {
            const active = item.id === activeTab;
            return (
              <div key={item.id}>
                <button
                  aria-pressed={active}
                  className={`w-full rounded-lg px-3 py-2 text-left text-sm transition ${active ? "bg-[var(--surface)] shadow-[inset_2px_0_0_var(--accent)]" : "hover:bg-[var(--surface-strong)]"}`}
                  onClick={() => actions.setMainTab(item.id)}
                  type="button"
                >
                  <span className="mr-2 font-mono text-xs text-[var(--muted)]">{item.short}</span>
                  {item.label}
                </button>
                {item.id === "codex" && active ? (
                  <div className="ml-6 mt-1 grid gap-1 border-l border-[var(--line)] pl-2">
                    {CODEX_TABS.map((tab) => (
                      <button
                        className={`rounded-md px-2 py-1 text-left text-xs ${tab.id === activeCodexTab ? "bg-[var(--surface)] text-[var(--text)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-strong)]"}`}
                        key={tab.id}
                        onClick={() => actions.setCodexTab(tab.id)}
                        type="button"
                      >
                        {tab.label}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
            );
          })}
        </nav>
        <div className={`mt-5 rounded-lg border p-3 text-xs ${codex.available ? "border-[rgba(18,132,79,0.18)] bg-[var(--good-soft)] text-[var(--good)]" : "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] text-[var(--warn)]"}`}>
          <span className="block">{codex.available ? "Ready" : "Attention"}</span>
          <strong>{codex.available ? "Codex online" : "CLI missing"}</strong>
        </div>
      </aside>

      <main className="min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)]" id="mainContent">
        <header className="flex items-start justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface-soft)] px-5 py-4 max-md:grid">
          <div>
            <h1 className="m-0 text-lg font-semibold">{activeTab === "codex" ? "Codex" : activeMeta?.label}</h1>
            <p className="muted mt-1 mb-0 text-sm">{activeTab === "codex" ? `${activeMeta?.label}：${activeMeta?.description}` : activeMeta?.description}</p>
          </div>
          <div className="flex flex-wrap justify-end gap-2 max-md:justify-start">
            <Pill tone={codex.available ? "good" : "warn"}>Codex {codex.available ? "就绪" : "未找到"}</Pill>
            <Pill tone={images?.hasApiKey ? "good" : "warn"}>Images {images?.hasApiKey ? "已配置" : "未配置"}</Pill>
            <Pill tone={v2ray?.running ? "good" : "warn"}>V2Ray {v2rayStateLabel(v2ray)}</Pill>
            <Pill>{data.pendingApprovals.length} approvals</Pill>
            <Button onClick={() => void actions.reloadData()}>刷新</Button>
            <Button onClick={() => void logout()}>退出</Button>
          </div>
        </header>

        {activeTab === "dashboard" ? <DashboardView actions={actions} data={data} /> : null}
        {activeTab === "codex" && activeCodexTab === "sessions" ? (
          <CodexView actions={actions} activeSession={activeSession} activeSessionId={activeSessionId} activeSessionWorkspace={activeSessionWorkspace} data={data} sessionEvents={sessionEvents} />
        ) : null}
        {activeTab === "codex" && activeCodexTab === "projects" ? <ProjectsView actions={actions} data={data} selectedWorkspaceId={selectedWorkspaceId} /> : null}
        {activeTab === "codex" && activeCodexTab === "permissions" ? <PermissionsView data={data} /> : null}
        {activeTab === "codex" && activeCodexTab === "gateway" ? <CodexGatewayView actions={actions} data={data} /> : null}
        {activeTab === "codex" && activeCodexTab === "activity" ? <ActivityView audit={data.audit} /> : null}
        {activeTab === "logs" ? <LogsView actions={actions} /> : null}
        {activeTab === "images" ? <ImagesView actions={actions} data={data} /> : null}
        {activeTab === "v2ray" ? <V2RayView actions={actions} data={data} exportOpen={v2rayExportOpen} exported={v2rayExport as V2RayExport | null} /> : null}
        {activeTab === "settings" ? <SettingsView actions={actions} data={data} /> : null}
      </main>
    </div>
  );
}
