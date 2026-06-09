import { Button } from "../../../components/ui";
import type { CodexStatus } from "../../../app/types";

export function AppServerStrip({ status, onStart }: { status?: CodexStatus; onStart: () => Promise<void> }) {
  const state = status?.appServer?.state || "unknown";
  const canStart = state === "stopped" || state === "failed" || state === "degraded" || state === "unknown";
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs text-[var(--muted)]">
      <span className={`h-2 w-2 rounded-full ${state === "running" ? "bg-[var(--success)]" : state === "failed" || state === "degraded" ? "bg-[var(--warning)]" : "bg-[var(--muted)]"}`} />
      <span>app-server {state}</span>
      {status?.appServer?.pid ? <span className="mono">pid {status.appServer.pid}</span> : null}
      {status?.appServer?.lastError ? <span className="truncate text-[var(--warning)]">{status.appServer.lastError}</span> : null}
      {canStart ? <Button onClick={() => void onStart()}>{state === "failed" ? "重试启动" : "启动 app-server"}</Button> : null}
    </div>
  );
}
