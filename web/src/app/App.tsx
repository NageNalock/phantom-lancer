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
  Tone,
  V2RayPayload,
} from "./types";
import { AuthView } from "../features/AuthView";
import { AppShell } from "../features/AppShell";

type AuthMode = "checking" | "bootstrap" | "login" | "ready" | "failed";
const MAIN_TAB_IDS: MainTab[] = ["dashboard", "codex", "codex-gateway", "logs", "images", "docker", "v2ray", "settings"];
const MAIN_TAB_CHILD_KEYS = ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings", "drv", "drrepo", "drtag", "dcreate", "dcform", "dselc", "dseli"];

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

  const loadAppData = useCallback(async () => {
    const [dashboard, audit, codexGateway, settings, v2ray, imagesSettings, imageStorageSettings, imageJobs, imageAssets, imagePrompts] = await Promise.all([
      api<AppData["dashboard"]>("/api/dashboard/summary"),
      api<{ items?: AppData["audit"] }>("/api/audit/events"),
      loadCodexGatewayData(),
      api<SettingsPayload>("/api/settings"),
      api<V2RayPayload>("/api/v2ray/settings"),
      api<ImagesPayload>("/api/images/settings"),
      api<{ settings?: ImagesPayload["storageSettings"] }>("/api/images/storage-settings"),
      api<{ items?: ImagesPayload["jobs"]; count?: number }>("/api/images/jobs?limit=40"),
      api<{ items?: ImagesPayload["assets"] }>("/api/images/library/assets?limit=80"),
      api<{ items?: ImagesPayload["prompts"] }>("/api/images/prompts?limit=120"),
    ]);

    setData({
      dashboard,
      audit: audit.items || [],
      codexGateway,
      settings,
      v2ray,
      images: { ...imagesSettings, storageSettings: imageStorageSettings.settings, jobs: imageJobs.items || [], assets: imageAssets.items || [], prompts: imagePrompts.items || [], count: imageJobs.count || 0 },
    });
  }, [loadCodexGatewayData]);

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
          await loadAppData();
          if (active) setAuthMode("ready");
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
  }, [loadAppData]);

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
        const [settings, storageSettings, jobs, assets, prompts] = await Promise.allSettled([
          api<ImagesPayload>("/api/images/settings"),
          api<{ settings?: ImagesPayload["storageSettings"] }>("/api/images/storage-settings"),
          api<{ items?: ImagesPayload["jobs"]; count?: number }>("/api/images/jobs?limit=40"),
          api<{ items?: ImagesPayload["assets"] }>("/api/images/library/assets?limit=80"),
          api<{ items?: ImagesPayload["prompts"] }>("/api/images/prompts?limit=120"),
        ]);
        setData((current) => ({
          ...current,
          images: {
            ...current.images,
            ...(settings.status === "fulfilled" ? settings.value : {}),
            storageSettings: storageSettings.status === "fulfilled" ? storageSettings.value.settings : current.images.storageSettings,
            jobs: jobs.status === "fulfilled" ? jobs.value.items || [] : current.images.jobs,
            assets: assets.status === "fulfilled" ? assets.value.items || [] : current.images.assets,
            prompts: prompts.status === "fulfilled" ? prompts.value.items || [] : current.images.prompts,
            count: jobs.status === "fulfilled" ? jobs.value.count || 0 : current.images.count,
          },
          dashboard: { ...current.dashboard, images: settings.status === "fulfilled" ? settings.value.status : current.dashboard.images },
        }));
      },
      setV2RayExportOpen,
      setV2RayExport,
    }),
    [csrf, loadAppData, loadCodexGatewayData, mainTabHref, setActiveTab, setToast],
  );

  async function handleAuth(mode: "bootstrap" | "login", username: string, password: string) {
    const result = await api<{ csrfToken?: string; session: AuthSession }>(mode === "bootstrap" ? "/api/auth/bootstrap" : "/api/auth/login", {
      method: "POST",
      body: { username, password },
    });
    setCsrf(result.csrfToken || readCookie("pl_csrf"));
    setSession(result.session);
    await loadAppData();
    setAuthMode("ready");
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
