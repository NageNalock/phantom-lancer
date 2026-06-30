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
const MAIN_TAB_IDS: MainTab[] = ["dashboard", "codex", "codex-gateway", "logs", "images", "docker", "stockv2", "v2ray", "settings"];
const MAIN_TAB_CHILD_KEYS = ["codex", "codexInbox", "codexRuntime", "codexSidebar", "gateway", "images", "docker", "stockv2", "settings", "drv", "drrepo", "drtag", "dcreate", "dcform", "dselc", "dseli"];

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

  const loadCoreData = useCallback(async () => {
    const [dashboard, audit, codexGateway, settings, v2ray] = await Promise.all([
      api<AppData["dashboard"]>("/api/dashboard/summary"),
      api<{ items?: AppData["audit"] }>("/api/audit/events"),
      loadCodexGatewayData(),
      api<SettingsPayload>("/api/settings"),
      api<V2RayPayload>("/api/v2ray/settings"),
    ]);

    setData((current) => ({
      ...current,
      dashboard,
      audit: audit.items || [],
      codexGateway,
      settings,
      v2ray,
    }));
  }, [loadCodexGatewayData]);

  const loadAppData = useCallback(async () => {
    const [dashboard, audit, codexGateway, settings, v2ray, stockv2, images] = await Promise.all([
      api<AppData["dashboard"]>("/api/dashboard/summary"),
      api<{ items?: AppData["audit"] }>("/api/audit/events"),
      loadCodexGatewayData(),
      api<SettingsPayload>("/api/settings"),
      api<V2RayPayload>("/api/v2ray/settings"),
      api<StockV2Payload>("/api/stockv2/snapshot"),
      loadImagesData(),
    ]);

    setData({
      dashboard,
      audit: audit.items || [],
      codexGateway,
      settings,
      v2ray,
      stockv2,
      images,
    });
  }, [loadCodexGatewayData, loadImagesData]);

  const loadDeferredData = useCallback(async () => {
    const [stockv2, images] = await Promise.allSettled([
      api<StockV2Payload>("/api/stockv2/snapshot"),
      loadImagesData(),
    ]);
    setData((current) => ({
      ...current,
      stockv2: stockv2.status === "fulfilled" ? stockv2.value : current.stockv2,
      images: images.status === "fulfilled" ? images.value : current.images,
      dashboard: {
        ...current.dashboard,
        images: images.status === "fulfilled" ? images.value.status : current.dashboard.images,
      },
    }));
  }, [loadImagesData]);

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
          await loadCoreData();
          if (active) {
            setAuthMode("ready");
            void loadDeferredData();
          }
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
  }, [loadCoreData, loadDeferredData]);

  const actions = useMemo<AppActions>(
    () => ({
      api,
      csrf,
      setToast,
      reloadData: loadAppData,
      setMainTab: setActiveTab,
      mainTabHref,
      refreshCodexGateway: async () => {
        const codexGateway = await loadCodexGatewayData();
        setData((current) => ({ ...current, codexGateway, dashboard: { ...current.dashboard, codexGateway: codexGateway.status } }));
      },
      refreshV2Ray: async () => {
        const next = await api<V2RayPayload>("/api/v2ray/settings");
        setData((current) => ({ ...current, v2ray: next, dashboard: { ...current.dashboard, v2ray: next.status } }));
      },
      refreshImages: async () => {
        const next = await loadImagesData();
        setData((current) => ({
          ...current,
          images: next,
          dashboard: { ...current.dashboard, images: next.status },
        }));
      },
      refreshStockV2: async () => {
        const stockv2 = await api<StockV2Payload>("/api/stockv2/snapshot");
        setData((current) => ({ ...current, stockv2 }));
      },
      setV2RayExportOpen,
      setV2RayExport,
    }),
    [csrf, loadAppData, loadCodexGatewayData, loadImagesData, mainTabHref, setActiveTab, setToast],
  );

  async function handleAuth(mode: "bootstrap" | "login", username: string, password: string) {
    const result = await api<{ csrfToken?: string; session: AuthSession }>(mode === "bootstrap" ? "/api/auth/bootstrap" : "/api/auth/login", {
      method: "POST",
      body: { username, password },
    });
    setCsrf(result.csrfToken || readCookie("pl_csrf"));
    setSession(result.session);
    await loadCoreData();
    setAuthMode("ready");
    void loadDeferredData();
  }

  async function logout() {
    await api<{ ok: boolean }>("/api/auth/logout", { method: "POST", csrf });
    setSession(null);
    setCsrf("");
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
        logout={logout}
        v2rayExport={v2rayExport}
        v2rayExportOpen={v2rayExportOpen}
      />
      {toast ? <Toast message={toast.message} tone={toast.tone} /> : null}
    </>
  );
}
