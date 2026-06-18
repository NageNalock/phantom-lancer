import { useEffect, useId, useRef, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, MainTab, V2RayExport } from "../app/types";
import { Button, Pill } from "../components/ui";
import { NAV_ITEMS, stockDataHealthLabel, stockStatusLabel, stockV2StatusLabel, stockV2InstrumentCountLabel, v2rayStateLabel } from "../domain/labels";
import { shouldHandleQueryLinkClick } from "../hooks/useQueryParamState";
import { CodexGatewayView } from "./CodexGatewayView";
import { CodexView } from "./CodexView";
import { DashboardView } from "./DashboardView";
import { DockerView } from "./DockerView";
import { ImagesView } from "./ImagesView";
import { LogsView } from "./LogsView";
import { SettingsView } from "./SettingsView";
import { StockView } from "./StockView";
import { StockV2View } from "./stockv2/StockV2View";
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
  const stock = data.stock.summary;
  const stockData = data.stock.dataHealth;
  const [statusOpen, setStatusOpen] = useState(false);
  const statusPanelId = useId();
  const statusButtonId = useId();
  const statusPanelRef = useRef<HTMLDivElement | null>(null);
  const activeStatus = activeStatusPill(activeTab, data);

  useEffect(() => {
    if (!statusOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      const trigger = document.getElementById(statusButtonId);
      if (statusPanelRef.current?.contains(target) || trigger?.contains(target)) return;
      setStatusOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setStatusOpen(false);
      document.getElementById(statusButtonId)?.focus();
    };
    document.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [statusButtonId, statusOpen]);

  return (
    <div className="grid min-h-dvh grid-cols-[232px_minmax(0,1fr)] gap-3 p-3 max-lg:grid-cols-1">
      <a className="fixed left-4 top-4 z-40 -translate-y-24 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm focus:translate-y-0" href="#mainContent">
        跳到主要内容
      </a>
      <aside className="rounded-lg border border-[var(--line)] bg-[var(--rail)] p-3 max-lg:static">
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
              <a
                aria-current={active ? "page" : undefined}
                className={`block w-full rounded-lg px-3 py-2 text-left text-sm no-underline transition ${active ? "bg-[var(--surface)] text-[var(--text)] shadow-[inset_2px_0_0_var(--accent)]" : "text-[var(--text)] hover:bg-[var(--surface-strong)]"}`}
                href={actions.mainTabHref(item.id)}
                key={item.id}
                onClick={(event) => {
                  if (!shouldHandleQueryLinkClick(event)) return;
                  event.preventDefault();
                  actions.setMainTab(item.id);
                }}
              >
                {item.label}
              </a>
            );
          })}
        </nav>
      </aside>

      <main className="min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]" id="mainContent">
        <header className="flex items-start justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface-soft)] px-5 py-4 max-md:grid">
          <div>
            <h1 className="m-0 text-lg font-semibold">{activeMeta?.label}</h1>
            <p className="muted mt-1 mb-0 text-sm">{activeMeta?.description}</p>
          </div>
          <div className="relative flex flex-wrap justify-end gap-2 max-md:justify-start">
            {activeStatus}
            <Button aria-controls={statusOpen ? statusPanelId : undefined} aria-expanded={statusOpen} aria-haspopup="dialog" id={statusButtonId} onClick={() => setStatusOpen((open) => !open)}>
              状态摘要
            </Button>
            {statusOpen ? (
              <div
                aria-labelledby={statusButtonId}
                className="absolute top-full right-0 z-30 mt-2 grid w-72 gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs shadow-[var(--shadow)] max-md:right-auto max-md:left-0"
                id={statusPanelId}
                ref={statusPanelRef}
                role="dialog"
              >
                <StatusSummaryRow label="Gateway" tone={gateway?.enabled ? "good" : "warn"} value={gateway?.enabled ? "已启用" : "未启用"} />
                <StatusSummaryRow label="多媒体" tone={images?.hasApiKey ? "good" : "warn"} value={images?.hasApiKey ? "已配置" : "未配置"} />
                <StatusSummaryRow label="股票" tone={stock?.pendingOperationCount || stock?.openAlertCount ? "warn" : stock?.portfolioCount ? "good" : "neutral"} value={stockStatusLabel(stock)} />
                <StatusSummaryRow label="股票数据" tone={stockData?.failedSources || stockData?.failedTaskCount ? "warn" : stockData?.sourceCount ? "good" : "neutral"} value={stockDataHealthLabel(stockData)} />
                <StatusSummaryRow label="股票V2" tone={data.stockv2?.updateJobs?.some(j => j.status === "running") ? "warn" : data.stockv2?.instruments?.length ? "good" : "neutral"} value={stockV2StatusLabel(data.stockv2)} />
                <StatusSummaryRow label="V2Ray" tone={v2ray?.running ? "good" : "warn"} value={v2rayStateLabel(v2ray)} />
              </div>
            ) : null}
            <Button onClick={() => void actions.reloadData()}>刷新全部</Button>
            <Button onClick={() => void logout()}>退出</Button>
          </div>
        </header>

        {activeTab === "dashboard" ? <DashboardView actions={actions} data={data} /> : null}
        {activeTab === "codex" ? <CodexView actions={actions} data={data} /> : null}
        {activeTab === "codex-gateway" ? <CodexGatewayView actions={actions} data={data} /> : null}
        {activeTab === "logs" ? <LogsView actions={actions} /> : null}
        {activeTab === "images" ? <ImagesView actions={actions} data={data} /> : null}
        {activeTab === "docker" ? <DockerView actions={actions} /> : null}
        {activeTab === "stock" ? <StockView actions={actions} data={data} /> : null}
        {activeTab === "stockv2" ? <StockV2View actions={actions} data={data} /> : null}
        {activeTab === "v2ray" ? <V2RayView actions={actions} data={data} exportOpen={v2rayExportOpen} exported={v2rayExport as V2RayExport | null} /> : null}
        {activeTab === "settings" ? <SettingsView actions={actions} data={data} /> : null}
      </main>
    </div>
  );
}

function activeStatusPill(activeTab: MainTab, data: AppData) {
  if (activeTab === "codex-gateway") {
    const gateway = data.codexGateway.status || data.dashboard.codexGateway;
    return <Pill tone={gateway?.enabled ? "good" : "warn"}>Gateway {gateway?.enabled ? "已启用" : "未启用"}</Pill>;
  }
  if (activeTab === "images") {
    const images = data.images.status || data.dashboard.images;
    return <Pill tone={images?.hasApiKey ? "good" : "warn"}>多媒体 {images?.hasApiKey ? "已配置" : "未配置"}</Pill>;
  }
  if (activeTab === "v2ray") {
    const v2ray = data.v2ray.status || data.dashboard.v2ray;
    return <Pill tone={v2ray?.running ? "good" : "warn"}>V2Ray {v2rayStateLabel(v2ray)}</Pill>;
  }
  if (activeTab === "stock") {
    const stock = data.stock.summary;
    const stockData = data.stock.dataHealth;
    return <Pill tone={stock?.pendingOperationCount || stock?.openAlertCount || stockData?.failedSources || stockData?.failedTaskCount ? "warn" : stock?.portfolioCount || stockData?.sourceCount ? "good" : "neutral"}>股票V1 {stockStatusLabel(stock)} / {stockDataHealthLabel(stockData)}</Pill>;
  }
  if (activeTab === "stockv2") {
    const stockv2 = data.stockv2;
    const running = stockv2?.updateJobs?.some(j => j.status === "running");
    const tone: "good" | "warn" | "neutral" = running ? "warn" : stockv2?.instruments?.length ? "good" : "neutral";
    return <Pill tone={tone}>股票V2 {stockV2InstrumentCountLabel(stockv2)}</Pill>;
  }
  if (activeTab === "codex") {
    const codex = data.dashboard.codex;
    return <Pill tone={codex?.pendingApprovals ? "warn" : codex?.enabled ? "good" : "warn"}>Codex {codex?.pendingApprovals ? `${codex.pendingApprovals} 待审批` : codex?.enabled ? "可用" : "需检查"}</Pill>;
  }
  return null;
}

function StatusSummaryRow({ label, tone, value }: { label: string; tone: "good" | "warn" | "danger" | "neutral"; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-[var(--muted-strong)]">{label}</span>
      <Pill tone={tone}>{value}</Pill>
    </div>
  );
}
