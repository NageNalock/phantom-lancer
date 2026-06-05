import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, friendlyError, readCookie } from "../api/client";
import { Toast } from "../components/ui";
import type {
  AppData,
  AuthSession,
  CodexGatewayPayload,
  CodexSession,
  CodexSessionDetail,
  CodexStatus,
  CodexTab,
  EventRecord,
  ImagesPayload,
  MainTab,
  SettingsPayload,
  Tone,
  V2RayPayload,
  Workspace,
} from "./types";
import { AuthView } from "../features/AuthView";
import { AppShell } from "../features/AppShell";

type AuthMode = "checking" | "bootstrap" | "login" | "ready" | "failed";

export interface AppActions {
  api: typeof api;
  csrf: string;
  setToast: (message: string, tone?: Tone) => void;
  reloadData: () => Promise<void>;
  setMainTab: (tab: MainTab) => void;
  setCodexTab: (tab: CodexTab) => void;
  setSelectedWorkspaceId: (id: string) => void;
  setActiveSessionId: (id: string) => Promise<void>;
  setSessionEvents: (events: EventRecord[] | ((events: EventRecord[]) => EventRecord[])) => void;
  patchActiveSession: (patch: Partial<CodexSession>) => void;
  refreshSessions: () => Promise<void>;
  refreshCodexGateway: () => Promise<void>;
  refreshV2Ray: () => Promise<void>;
  refreshImages: () => Promise<void>;
  setV2RayExportOpen: (open: boolean) => void;
  setV2RayExport: (value: unknown) => void;
}

const emptyData: AppData = {
  dashboard: {},
  workspaces: [],
  audit: [],
  pendingApprovals: [],
  permissionProfiles: [],
  codexStatus: {},
  codexSessions: [],
  codexGateway: {},
  settings: {},
  v2ray: {},
  images: {},
};

export function App() {
  const [authMode, setAuthMode] = useState<AuthMode>("checking");
  const [fatal, setFatal] = useState("");
  const [csrf, setCsrf] = useState(readCookie("pl_csrf"));
  const [session, setSession] = useState<AuthSession | null>(null);
  const [data, setData] = useState<AppData>(emptyData);
  const [activeTab, setActiveTab] = useState<MainTab>("dashboard");
  const [activeCodexTab, setActiveCodexTab] = useState<CodexTab>("sessions");
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState("");
  const [activeSessionId, setActiveSessionIdValue] = useState("");
  const activeSessionIdRef = useRef("");
  const [activeSession, setActiveSession] = useState<CodexSession | null>(null);
  const [activeSessionWorkspace, setActiveSessionWorkspace] = useState<Workspace | null>(null);
  const [sessionEvents, setSessionEvents] = useState<EventRecord[]>([]);
  const [toast, setToastState] = useState<{ message: string; tone: Tone } | null>(null);
  const [v2rayExport, setV2RayExport] = useState<unknown>(null);
  const [v2rayExportOpen, setV2RayExportOpen] = useState(false);

  useEffect(() => {
    activeSessionIdRef.current = activeSessionId;
  }, [activeSessionId]);

  const setToast = useCallback((message: string, tone: Tone = "warn") => {
    setToastState({ message, tone });
    window.setTimeout(() => setToastState(null), 5200);
  }, []);

  const loadActiveSession = useCallback(async (id: string) => {
    const [detail, history] = await Promise.all([
      api<CodexSessionDetail>(`/api/codex/sessions/${encodeURIComponent(id)}`),
      api<{ items?: EventRecord[] }>(`/api/events/history?scope=codex_session&id=${encodeURIComponent(id)}`),
    ]);
    setActiveSession(detail.session);
    setActiveSessionWorkspace(detail.workspace);
    setSessionEvents(history.items || []);
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
    const [dashboard, workspaces, audit, codexStatus, codexGateway, approvals, profiles, sessions, settings, v2ray, imagesSettings, imageStorageSettings, imageJobs, imageAssets] = await Promise.all([
      api<AppData["dashboard"]>("/api/dashboard/summary"),
      api<{ items?: Workspace[] }>("/api/workspaces"),
      api<{ items?: AppData["audit"] }>("/api/audit/events"),
      api<CodexStatus>("/api/codex/status"),
      loadCodexGatewayData(),
      api<{ items?: unknown[] }>("/api/approvals/pending"),
      api<{ items?: AppData["permissionProfiles"] }>("/api/permissions/profiles"),
      api<{ items?: CodexSession[] }>("/api/codex/sessions"),
      api<SettingsPayload>("/api/settings"),
      api<V2RayPayload>("/api/v2ray/settings"),
      api<ImagesPayload>("/api/images/settings"),
      api<{ settings?: ImagesPayload["storageSettings"] }>("/api/images/storage-settings"),
      api<{ items?: ImagesPayload["jobs"]; count?: number }>("/api/images/jobs?limit=40"),
      api<{ items?: ImagesPayload["assets"] }>("/api/images/library/assets?limit=80"),
    ]);

    const nextSessions = sessions.items || [];
    setData({
      dashboard,
      workspaces: workspaces.items || [],
      audit: audit.items || [],
      pendingApprovals: approvals.items || [],
      permissionProfiles: profiles.items || [],
      codexStatus,
      codexSessions: nextSessions,
      codexGateway,
      settings,
      v2ray,
      images: { ...imagesSettings, storageSettings: imageStorageSettings.settings, jobs: imageJobs.items || [], assets: imageAssets.items || [], count: imageJobs.count || 0 },
    });

    const currentSessionId = activeSessionIdRef.current;
    const selected = currentSessionId && nextSessions.some((item) => item.id === currentSessionId) ? currentSessionId : nextSessions[0]?.id || "";
    activeSessionIdRef.current = selected;
    setActiveSessionIdValue(selected);
    if (selected) await loadActiveSession(selected);
    else {
      setActiveSession(null);
      setActiveSessionWorkspace(null);
      setSessionEvents([]);
    }
  }, [loadActiveSession, loadCodexGatewayData]);

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
      setCodexTab: setActiveCodexTab,
      setSelectedWorkspaceId,
      setActiveSessionId: async (id: string) => {
        activeSessionIdRef.current = id;
        setActiveSessionIdValue(id);
        await loadActiveSession(id);
      },
      setSessionEvents,
      patchActiveSession: (patch) => setActiveSession((current) => (current ? { ...current, ...patch } : current)),
      refreshSessions: async () => {
        const sessions = await api<{ items?: CodexSession[] }>("/api/codex/sessions");
        setData((current) => ({ ...current, codexSessions: sessions.items || [] }));
      },
      refreshCodexGateway: async () => {
        const codexGateway = await loadCodexGatewayData();
        setData((current) => ({ ...current, codexGateway, dashboard: { ...current.dashboard, codexGateway: codexGateway.status } }));
      },
      refreshV2Ray: async () => {
        const next = await api<V2RayPayload>("/api/v2ray/settings");
        setData((current) => ({ ...current, v2ray: next, dashboard: { ...current.dashboard, v2ray: next.status } }));
      },
      refreshImages: async () => {
        const [settings, storageSettings, jobs, assets] = await Promise.all([
          api<ImagesPayload>("/api/images/settings"),
          api<{ settings?: ImagesPayload["storageSettings"] }>("/api/images/storage-settings"),
          api<{ items?: ImagesPayload["jobs"]; count?: number }>("/api/images/jobs?limit=40"),
          api<{ items?: ImagesPayload["assets"] }>("/api/images/library/assets?limit=80"),
        ]);
        setData((current) => ({
          ...current,
          images: { ...settings, storageSettings: storageSettings.settings, jobs: jobs.items || [], assets: assets.items || [], count: jobs.count || 0 },
          dashboard: { ...current.dashboard, images: settings.status },
        }));
      },
      setV2RayExportOpen,
      setV2RayExport,
    }),
    [csrf, loadActiveSession, loadAppData, loadCodexGatewayData, setToast],
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
        activeCodexTab={activeCodexTab}
        activeSession={activeSession}
        activeSessionId={activeSessionId}
        activeSessionWorkspace={activeSessionWorkspace}
        activeTab={activeTab}
        data={data}
        logout={logout}
        selectedWorkspaceId={selectedWorkspaceId}
        sessionEvents={sessionEvents}
        v2rayExport={v2rayExport}
        v2rayExportOpen={v2rayExportOpen}
      />
      {toast ? <Toast message={toast.message} tone={toast.tone} /> : null}
    </>
  );
}
