import { useCallback, useEffect, useMemo, useState } from "react";
import { api, friendlyError, readCookie } from "../api/client";
import { Toast } from "../components/ui";
import { useQueryParamState } from "../hooks/useQueryParamState";
import type {
  AppData,
  AuthSession,
  CodexGatewayPayload,
  ImagesPayload,
  MainTab,
  SettingsPayload,
  StockV2Payload,
  Tone,
  V2RayPayload,
} from "./types";
import { AuthView } from "../features/AuthView";
import { AppShell } from "../features/AppShell";

type AuthMode = "checking" | "bootstrap" | "login" | "ready" | "failed";
type DataScope = "dashboard" | "codex-gateway" | "images" | "stockv2" | "v2ray" | "settings";
type LoadStatus = "idle" | "loading" | "ready" | "error";
const MAIN_TAB_IDS: MainTab[] = ["dashboard", "codex", "codex-gateway", "logs", "images", "docker", "stockv2", "v2ray", "settings"];
const MAIN_TAB_CHILD_KEYS = ["codex", "codexInbox", "codexRuntime", "codexSidebar", "gateway", "images", "docker", "stockv2", "settings", "drv", "drrepo", "drtag", "dcreate", "dcform", "dselc", "dseli"];
const INITIAL_SCOPE_STATUS: Record<DataScope, LoadStatus> = {
  dashboard: "idle",
  "codex-gateway": "idle",
  images: "idle",
  stockv2: "idle",
  v2ray: "idle",
  settings: "idle",
};
const INITIAL_SCOPE_ERRORS: Record<DataScope, string> = {
  dashboard: "",
  "codex-gateway": "",
  images: "",
  stockv2: "",
  v2ray: "",
  settings: "",
};

export interface AppActions {
  api: typeof api;
  csrf: string;
  setToast: (message: string, tone?: Tone) => void;
  reloadData: () => Promise<void>;
  setMainTab: (tab: MainTab) => void;
  mainTabHref: (tab: MainTab) => string;
  refreshCodexGateway: () => Promise<void>;
  refreshV2Ray: () => Promise<void>;
  refreshImages: () => Promise<void>;
  refreshStockV2: () => Promise<void>;
  setV2RayExportOpen: (open: boolean) => void;
  setV2RayExport: (value: unknown) => void;
}

const emptyData: AppData = {
  dashboard: {},
  audit: [],
  codexGateway: {},
  settings: {},
  v2ray: {},
  images: {},
  stockv2: {},
};

export function App() {
  const [authMode, setAuthMode] = useState<AuthMode>("checking");
  const [fatal, setFatal] = useState("");
  const [csrf, setCsrf] = useState(readCookie("pl_csrf"));
  const [, setSession] = useState<AuthSession | null>(null);
  const [data, setData] = useState<AppData>(emptyData);
  const [activeTab, setActiveTab, mainTabHref] = useQueryParamState<MainTab>("tab", MAIN_TAB_IDS, "dashboard", { clearKeys: MAIN_TAB_CHILD_KEYS });
  const [toast, setToastState] = useState<{ message: string; tone: Tone } | null>(null);
  const [v2rayExport, setV2RayExport] = useState<unknown>(null);
  const [v2rayExportOpen, setV2RayExportOpen] = useState(false);
  const [scopeStatus, setScopeStatus] = useState<Record<DataScope, LoadStatus>>(INITIAL_SCOPE_STATUS);
  const [scopeErrors, setScopeErrors] = useState<Record<DataScope, string>>(INITIAL_SCOPE_ERRORS);

  const setToast = useCallback((message: string, tone: Tone = "warn") => {
    setToastState({ message, tone });
    window.setTimeout(() => setToastState(null), 5200);
  }, []);

  const loadCodexGatewayData = useCallback(async (): Promise<CodexGatewayPayload> => {
    const [status, settings, apiKeys, accounts, models, requestLogs] = await Promise.all([
      api<CodexGatewayPayload["status"]>("/api/codex-gateway/status"),
      api<{ settings?: CodexGatewayPayload["settings"] }>("/api/codex-gateway/settings"),
      api<{ items?: CodexGatewayPayload["apiKeys"] }>("/api/codex-gateway/api-keys"),
      api<{ items?: CodexGatewayPayload["accounts"] }>("/api/codex-gateway/accounts"),
      api<{ items?: CodexGatewayPayload["models"] }>("/api/codex-gateway/models"),
      api<{ items?: CodexGatewayPayload["requestLogs"] }>("/api/codex-gateway/request-logs?limit=40"),
    ]);
    return {
      status,
      settings: settings.settings,
      apiKeys: apiKeys.items || [],
      accounts: accounts.items || [],
      models: models.items || [],
      requestLogs: requestLogs.items || [],
    };
  }, []);

  const loadImagesData = useCallback(async (): Promise<ImagesPayload> => {
    const [settings, storageSettings, jobs, assets, prompts] = await Promise.all([
      api<ImagesPayload>("/api/images/settings"),
      api<{ settings?: ImagesPayload["storageSettings"] }>("/api/images/storage-settings"),
      api<{ items?: ImagesPayload["jobs"]; count?: number }>("/api/images/jobs?limit=200"),
      api<{ items?: ImagesPayload["assets"] }>("/api/images/library/assets?limit=200"),
      api<{ items?: ImagesPayload["prompts"] }>("/api/images/prompts?limit=120"),
    ]);
    return {
      ...settings,
      storageSettings: storageSettings.settings,
      jobs: jobs.items || [],
      assets: assets.items || [],
      prompts: prompts.items || [],
      count: jobs.count || 0,
    };
  }, []);

  const runScopedLoad = useCallback(async (scope: DataScope, load: () => Promise<void>) => {
    setScopeStatus((current) => ({ ...current, [scope]: "loading" }));
    setScopeErrors((current) => ({ ...current, [scope]: "" }));
    try {
      await load();
      setScopeStatus((current) => ({ ...current, [scope]: "ready" }));
    } catch (error) {
      setScopeErrors((current) => ({ ...current, [scope]: friendlyError(error) }));
      setScopeStatus((current) => ({ ...current, [scope]: "error" }));
      throw error;
    }
  }, []);

  const refreshDashboardData = useCallback(() => runScopedLoad("dashboard", async () => {
    const dashboard = await api<AppData["dashboard"]>("/api/dashboard/summary");
    setData((current) => ({ ...current, dashboard }));
  }), [runScopedLoad]);

  const loadAuditData = useCallback(async () => {
    const audit = await api<{ items?: AppData["audit"] }>("/api/audit/events");
    setData((current) => ({ ...current, audit: audit.items || [] }));
  }, []);

  const refreshSettingsData = useCallback(() => runScopedLoad("settings", async () => {
    const settings = await api<SettingsPayload>("/api/settings");
    setData((current) => ({ ...current, settings }));
  }), [runScopedLoad]);

  const refreshCodexGatewayData = useCallback(() => runScopedLoad("codex-gateway", async () => {
    const codexGateway = await loadCodexGatewayData();
    setData((current) => ({
      ...current,
      codexGateway,
      dashboard: { ...current.dashboard, codexGateway: codexGateway.status },
    }));
  }), [loadCodexGatewayData, runScopedLoad]);

  const refreshV2RayData = useCallback(() => runScopedLoad("v2ray", async () => {
    const v2ray = await api<V2RayPayload>("/api/v2ray/settings");
    setData((current) => ({ ...current, v2ray, dashboard: { ...current.dashboard, v2ray: v2ray.status } }));
  }), [runScopedLoad]);

  const refreshImagesData = useCallback(() => runScopedLoad("images", async () => {
    const images = await loadImagesData();
    setData((current) => ({
      ...current,
      images,
      dashboard: { ...current.dashboard, images: images.status },
    }));
  }), [loadImagesData, runScopedLoad]);

  const refreshStockV2Data = useCallback(() => runScopedLoad("stockv2", async () => {
    const stockv2 = await api<StockV2Payload>("/api/stockv2/snapshot");
    setData((current) => ({ ...current, stockv2 }));
  }), [runScopedLoad]);

  const loadScopeData = useCallback((scope: DataScope): Promise<void> => {
    switch (scope) {
      case "dashboard":
        return refreshDashboardData();
      case "codex-gateway":
        return refreshCodexGatewayData();
      case "images":
        return refreshImagesData();
      case "stockv2":
        return refreshStockV2Data();
      case "v2ray":
        return refreshV2RayData();
      case "settings":
        return refreshSettingsData();
    }
  }, [refreshCodexGatewayData, refreshDashboardData, refreshImagesData, refreshSettingsData, refreshStockV2Data, refreshV2RayData]);

  const loadBaseData = useCallback(async () => {
    await Promise.allSettled([
      refreshDashboardData(),
      loadAuditData(),
      refreshSettingsData(),
    ]);
  }, [loadAuditData, refreshDashboardData, refreshSettingsData]);

  const loadAppData = useCallback(async () => {
    const results = await Promise.allSettled([
      refreshDashboardData(),
      loadAuditData(),
      refreshSettingsData(),
      refreshCodexGatewayData(),
      refreshV2RayData(),
      refreshStockV2Data(),
      refreshImagesData(),
    ]);
    const failure = results.find((result) => result.status === "rejected");
    if (failure?.status === "rejected") throw failure.reason;
  }, [loadAuditData, refreshCodexGatewayData, refreshDashboardData, refreshImagesData, refreshSettingsData, refreshStockV2Data, refreshV2RayData]);

  useEffect(() => {
    let active = true;
    async function boot() {
      try {
        const bootstrap = await api<{ ownerConfigured?: boolean }>("/api/auth/bootstrap-status");
        if (!active) return;
        if (!bootstrap.ownerConfigured) {
          setAuthMode("bootstrap");
          return;
        }
        try {
          const me = await api<{ session: AuthSession }>("/api/auth/me");
          if (!active) return;
          setSession(me.session);
          setAuthMode("ready");
        } catch {
          if (active) setAuthMode("login");
        }
      } catch (error) {
        if (active) {
          setFatal(friendlyError(error));
          setAuthMode("failed");
        }
      }
    }
    void boot();
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (authMode !== "ready") return;
    void loadBaseData();
  }, [authMode, loadBaseData]);

  const activeDataScope = dataScopeForTab(activeTab);

  useEffect(() => {
    if (authMode !== "ready" || !activeDataScope) return;
    if (activeDataScope === "dashboard" || activeDataScope === "settings") return;
    if (scopeStatus[activeDataScope] !== "idle") return;
    void loadScopeData(activeDataScope).catch(() => undefined);
  }, [activeDataScope, authMode, loadScopeData, scopeStatus]);

  const actions = useMemo<AppActions>(
    () => ({
      api,
      csrf,
      setToast,
      reloadData: async () => {
        try {
          await loadAppData();
          setToast("已刷新全部数据", "good");
        } catch (error) {
          setToast(`刷新失败：${friendlyError(error)}`, "danger");
        }
      },
      setMainTab: setActiveTab,
      mainTabHref,
      refreshCodexGateway: refreshCodexGatewayData,
      refreshV2Ray: refreshV2RayData,
      refreshImages: refreshImagesData,
      refreshStockV2: refreshStockV2Data,
      setV2RayExportOpen,
      setV2RayExport,
    }),
    [csrf, loadAppData, mainTabHref, refreshCodexGatewayData, refreshImagesData, refreshStockV2Data, refreshV2RayData, setActiveTab, setToast],
  );

  const retryActiveData = useCallback(async () => {
    if (!activeDataScope) return;
    await loadScopeData(activeDataScope);
  }, [activeDataScope, loadScopeData]);

  async function handleAuth(mode: "bootstrap" | "login", username: string, password: string) {
    const result = await api<{ csrfToken?: string; session: AuthSession }>(mode === "bootstrap" ? "/api/auth/bootstrap" : "/api/auth/login", {
      method: "POST",
      body: { username, password },
    });
    setCsrf(result.csrfToken || readCookie("pl_csrf"));
    setSession(result.session);
    setAuthMode("ready");
  }

  async function logout() {
    await api<{ ok: boolean }>("/api/auth/logout", { method: "POST", csrf });
    setSession(null);
    setCsrf("");
    setData(emptyData);
    setScopeStatus(INITIAL_SCOPE_STATUS);
    setScopeErrors(INITIAL_SCOPE_ERRORS);
    setAuthMode("login");
  }

  if (authMode === "checking") {
    return <div className="grid min-h-dvh place-items-center text-sm text-[var(--muted)]">Loading Phantom Lancer</div>;
  }

  if (authMode === "failed") {
    return <div className="grid min-h-dvh place-items-center p-6 text-sm text-[var(--danger)]">{fatal}</div>;
  }

  if (authMode === "bootstrap" || authMode === "login") {
    return <AuthView mode={authMode} onSubmit={handleAuth} />;
  }

  return (
    <>
      <AppShell
        actions={actions}
        activeTab={activeTab}
        data={data}
        dataLoadError={activeDataScope && scopeStatus[activeDataScope] === "error" ? scopeErrors[activeDataScope] : ""}
        dataLoading={Boolean(activeDataScope && (scopeStatus[activeDataScope] === "idle" || scopeStatus[activeDataScope] === "loading"))}
        logout={logout}
        retryActiveData={retryActiveData}
        v2rayExport={v2rayExport}
        v2rayExportOpen={v2rayExportOpen}
      />
      {toast ? <Toast message={toast.message} tone={toast.tone} /> : null}
    </>
  );
}

function dataScopeForTab(tab: MainTab): DataScope | undefined {
  switch (tab) {
    case "dashboard":
    case "codex-gateway":
    case "images":
    case "stockv2":
    case "v2ray":
    case "settings":
      return tab;
    case "codex":
    case "logs":
    case "docker":
      return undefined;
  }
}
