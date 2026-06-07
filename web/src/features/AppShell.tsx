import type { AppActions } from "../app/App";
import type { AppData, MainTab, V2RayExport } from "../app/types";
import { Button, Pill } from "../components/ui";
import { NAV_ITEMS, v2rayStateLabel } from "../domain/labels";
import { CodexGatewayView } from "./CodexGatewayView";
import { CodexView } from "./CodexView";
import { DashboardView } from "./DashboardView";
import { ImagesView } from "./ImagesView";
import { LogsView } from "./LogsView";
import { SettingsView } from "./SettingsView";
import { V2RayView } from "./V2RayView";

export function AppShell({
  actions,
  activeTab,
  data,
  logout,
  v2rayExport,
  v2rayExportOpen,
}: {
  actions: AppActions;
  activeTab: MainTab;
  data: AppData;
  logout: () => Promise<void>;
  v2rayExport: unknown;
  v2rayExportOpen: boolean;
}) {
  const activeMeta = NAV_ITEMS.find((item) => item.id === activeTab);
  const gateway = data.codexGateway.status || data.dashboard.codexGateway;
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
              <button
                aria-pressed={active}
                className={`w-full rounded-lg px-3 py-2 text-left text-sm transition ${active ? "bg-[var(--surface)] shadow-[inset_2px_0_0_var(--accent)]" : "hover:bg-[var(--surface-strong)]"}`}
                key={item.id}
                onClick={() => actions.setMainTab(item.id)}
                type="button"
              >
                <span className="mr-2 font-mono text-xs text-[var(--muted)]">{item.short}</span>
                {item.label}
              </button>
            );
          })}
        </nav>
      </aside>

      <main className="min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)]" id="mainContent">
        <header className="flex items-start justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface-soft)] px-5 py-4 max-md:grid">
          <div>
            <h1 className="m-0 text-lg font-semibold">{activeMeta?.label}</h1>
            <p className="muted mt-1 mb-0 text-sm">{activeMeta?.description}</p>
          </div>
          <div className="flex flex-wrap justify-end gap-2 max-md:justify-start">
            <Pill tone={gateway?.enabled ? "good" : "warn"}>Gateway {gateway?.enabled ? "已启用" : "未启用"}</Pill>
            <Pill tone={images?.hasApiKey ? "good" : "warn"}>Images {images?.hasApiKey ? "已配置" : "未配置"}</Pill>
            <Pill tone={v2ray?.running ? "good" : "warn"}>V2Ray {v2rayStateLabel(v2ray)}</Pill>
            <Button onClick={() => void actions.reloadData()}>刷新</Button>
            <Button onClick={() => void logout()}>退出</Button>
          </div>
        </header>

        {activeTab === "dashboard" ? <DashboardView actions={actions} data={data} /> : null}
        {activeTab === "codex" ? <CodexView actions={actions} data={data} /> : null}
        {activeTab === "codex-gateway" ? <CodexGatewayView actions={actions} data={data} /> : null}
        {activeTab === "logs" ? <LogsView actions={actions} /> : null}
        {activeTab === "images" ? <ImagesView actions={actions} data={data} /> : null}
        {activeTab === "v2ray" ? <V2RayView actions={actions} data={data} exportOpen={v2rayExportOpen} exported={v2rayExport as V2RayExport | null} /> : null}
        {activeTab === "settings" ? <SettingsView actions={actions} data={data} /> : null}
      </main>
    </div>
  );
}
