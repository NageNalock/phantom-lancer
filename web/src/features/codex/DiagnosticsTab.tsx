import { useCallback, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexAppServerStatus, CodexStatus } from "../../app/types";
import { Button, ContextList, Notice, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { codexAppServerStateLabel, codexInstallStatusLabel, formatDate } from "../../domain/labels";

export function DiagnosticsTab({ actions, status, onChange }: { actions: AppActions; status?: CodexStatus; onChange: () => void }) {
  const [busy, setBusy] = useState(false);
  const install = status?.installation;
  const caps = (install?.capabilities || {}) as Record<string, unknown>;
  const appServer = status?.appServer;

  const probe = useCallback(async () => {
    setBusy(true);
    try {
      await actions.api("/api/codex/status/probe", { method: "POST", csrf: actions.csrf });
      onChange();
      actions.setToast("已重新探测 Codex CLI", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy(false);
    }
  }, [actions, onChange]);

  const control = useCallback(
    async (action: "start" | "stop" | "restart") => {
      setBusy(true);
      try {
        await actions.api(`/api/codex/app-server/${action}`, { method: "POST", csrf: actions.csrf });
        onChange();
      } catch (error) {
        actions.setToast(friendlyError(error), "danger");
      } finally {
        setBusy(false);
      }
    },
    [actions, onChange],
  );

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-4 max-lg:grid-cols-1">
      <Panel actions={<Button disabled={busy} onClick={() => void probe()}>{busy ? "处理中" : "重新探测"}</Button>} subtitle="本机 codex CLI 安装、版本、认证与沙箱能力。" title="CLI 安装">
        <ContextList
          items={[
            ["状态", <Pill tone={installTone(install?.status)}>{codexInstallStatusLabel(install?.status)}</Pill>],
            ["二进制", install?.binaryPath ? <span className="mono break-all">{install.binaryPath}</span> : "未找到"],
            ["版本", install?.version ? <span className="mono">{install.version}</span> : "-"],
            ["认证", authLabel(String(caps.authState || ""))],
            ["沙箱", String(caps.sandboxState || "unknown")],
            ["app-server", caps.appServer ? "支持" : "不支持"],
            ["exec", caps.exec ? "支持" : "不支持"],
            ["探测时间", formatDate(install?.detectedAt) || "-"],
          ]}
        />
        {install?.lastProbeError ? (
          <div className="mt-3">
            <Notice tone="warn">{install.lastProbeError}</Notice>
          </div>
        ) : null}
      </Panel>

      <Panel subtitle="主程序管理的内部 app-server runtime，不对浏览器开放。" title="app-server runtime">
        <AppServerStrip status={appServer} />
        <div className="mt-3 flex flex-wrap gap-2">
          {appServer?.state === "running" ? (
            <>
              <Button disabled={busy} onClick={() => void control("restart")}>
                重启
              </Button>
              <Button disabled={busy} tone="danger" onClick={() => void control("stop")}>
                停止
              </Button>
            </>
          ) : (
            <Button disabled={busy} tone="primary" onClick={() => void control("start")}>
              {appServer?.state === "failed" ? "重试启动" : "启动 app-server"}
            </Button>
          )}
        </div>
        {appServer?.lastError ? (
          <div className="mt-3">
            <Notice tone="danger">{appServer.lastError}</Notice>
          </div>
        ) : null}
        {status?.legacyTables && status.legacyTables.length ? (
          <div className="mt-3">
            <Notice tone="warn">检测到旧版 Codex 数据残留表：{status.legacyTables.join(", ")}。新模块使用独立 codex_cli_ 表，不受影响。</Notice>
          </div>
        ) : null}
      </Panel>
    </div>
  );
}

function AppServerStrip({ status }: { status?: CodexAppServerStatus }) {
  return (
    <ContextList
      items={[
        ["运行状态", <Pill tone={status?.state === "running" ? "good" : status?.state === "failed" ? "danger" : status?.state === "starting" ? "warn" : "neutral"}>{codexAppServerStateLabel(status?.state)}</Pill>],
        ["PID", status?.pid ? <span className="mono">{status.pid}</span> : "-"],
        ["已运行", status?.uptimeSeconds ? `${status.uptimeSeconds}s` : "-"],
        ["最近探测", formatDate(status?.lastProbeAt) || "-"],
        ["开关", status?.enabled ? "已启用" : "已禁用"],
      ]}
    />
  );
}

function installTone(value?: string) {
  if (value === "ready") return "good" as const;
  if (value === "degraded") return "warn" as const;
  if (value === "unavailable") return "danger" as const;
  return "neutral" as const;
}

function authLabel(value: string): string {
  return (
    {
      logged_in: "已登录",
      logged_out: "未登录",
      unknown: "不可判定",
    }[value] ||
    "不可判定"
  );
}
