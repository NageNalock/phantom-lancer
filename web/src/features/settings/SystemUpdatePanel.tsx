import { useCallback, useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { EventRecord, SystemUpdateJob, SystemUpdateStatus } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CheckLabel, ContextList, Notice, Panel, Pill } from "../../components/ui";
import { formatBytesIEC } from "../../utils/format";
import { formatDate } from "../../domain/labels";

const supervisorReasonHints: Record<string, string> = {
  manual_upgrade_detected: "当前 binary 检测到手动替换,已按实际版本同步。",
};

const updateEventNames = [
  "update.job.created",
  "update.job.updated",
  "update.job.interrupted",
  "update.download.started",
  "update.download.progress",
  "update.download.completed",
  "update.verify.completed",
  "update.extract.completed",
  "update.install.completed",
  "update.restart.requested",
  "update.failed",
  "update.cancelled",
  "update.rollback.applied",
  "update.completed",
];

export function SystemUpdatePanel({ actions }: { actions: AppActions }) {
  const [status, setStatus] = useState<SystemUpdateStatus>({});
  const [busy, setBusy] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [ownerPassword, setOwnerPassword] = useState("");
  const [confirmService, setConfirmService] = useState(false);
  const [confirmTasks, setConfirmTasks] = useState(false);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [reconnecting, setReconnecting] = useState(false);
  const [reconnectTimedOut, setReconnectTimedOut] = useState(false);
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackPassword, setRollbackPassword] = useState("");

  const activeJob = status.activeJob;
  const latestJob = status.latestJob;
  const check = status.latestCheck;
  const visibleJob = activeJob || latestJob;

  const loadStatus = useCallback(async () => {
    const next = await actions.api<SystemUpdateStatus>("/api/system/update/status");
    setStatus(next);
    return next;
  }, [actions]);

  useEffect(() => {
    void loadStatus().catch((error) => actions.setToast(friendlyError(error), "danger"));
  }, [actions, loadStatus]);

  useEffect(() => {
    if (!activeJob?.id) return;
    let closed = false;
    const jobId = activeJob.id;

    async function loadHistory() {
      const history = await actions.api<{ items?: EventRecord[] }>(`/api/events/history?scope=system_update&id=${encodeURIComponent(jobId)}`);
      if (!closed) setEvents(history.items || []);
    }

    void loadHistory().catch(() => undefined);
    const source = new EventSource(`/api/events/stream?scope=system_update&id=${encodeURIComponent(jobId)}`);
    const handle = (event: MessageEvent<string>) => {
      try {
        const record = JSON.parse(event.data) as EventRecord;
        setEvents((current) => [...current.slice(-80), record]);
        if (record.type === "update.restart.requested") setReconnecting(true);
        void loadStatus().catch(() => undefined);
      } catch {
        // Ignore malformed stream frames; the next status poll will reconcile.
      }
    };
    updateEventNames.forEach((name) => source.addEventListener(name, handle));
    source.onerror = () => {
      if (!closed) source.close();
    };
    return () => {
      closed = true;
      updateEventNames.forEach((name) => source.removeEventListener(name, handle));
      source.close();
    };
  }, [actions, activeJob?.id, loadStatus]);

  useEffect(() => {
    if (!reconnecting) return;
    const started = Date.now();
    const timeout = Math.max(status.restartTimeoutSeconds || 120, 10) * 1000;
    const timer = window.setInterval(() => {
      void actions
        .api<{ ok?: boolean }>("/api/health")
        .then(() => loadStatus())
        .then((next) => {
          if (next.version?.version && next.latestJob?.targetVersion === next.version.version) {
            setReconnecting(false);
            setReconnectTimedOut(false);
            actions.setToast("系统更新已完成", "good");
          }
        })
        .catch(() => {
          if (Date.now() - started > timeout) {
            setReconnecting(false);
            setReconnectTimedOut(true);
          }
        });
    }, 1800);
    return () => window.clearInterval(timer);
  }, [actions, loadStatus, reconnecting, status.restartTimeoutSeconds]);

  const progress = useMemo(() => {
    const total = visibleJob?.totalBytes || 0;
    const done = visibleJob?.bytesDownloaded || 0;
    if (!total) return 0;
    return Math.max(0, Math.min(100, Math.round((done / total) * 100)));
  }, [visibleJob?.bytesDownloaded, visibleJob?.totalBytes]);

  async function checkUpdates() {
    setBusy("check");
    try {
      const result = await actions.api<{ status?: SystemUpdateStatus }>("/api/system/update/check", { method: "POST", csrf: actions.csrf });
      setStatus(result.status || (await loadStatus()));
      actions.setToast("更新检查已完成", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function applyUpdate() {
    if (!check?.latestVersion || !check.releaseId) return;
    setBusy("apply");
    try {
      await actions.api("/api/system/update/apply", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          targetVersion: check.latestVersion,
          releaseId: check.releaseId,
          confirmServiceInterruption: confirmService,
          confirmTaskInterruption: confirmTasks,
          ownerPassword,
        },
      });
      setOwnerPassword("");
      setConfirmOpen(false);
      setConfirmService(false);
      setConfirmTasks(false);
      await loadStatus();
      actions.setToast("系统更新已开始", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function cancelUpdate(job: SystemUpdateJob) {
    setBusy("cancel");
    try {
      await actions.api(`/api/system/update/jobs/${encodeURIComponent(job.id)}/cancel`, { method: "POST", csrf: actions.csrf });
      await loadStatus();
      actions.setToast("更新任务已取消", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function rollbackLatest(job: SystemUpdateJob) {
    setBusy("rollback");
    try {
      const result = await actions.api<{ execPath?: string; job?: SystemUpdateJob }>(`/api/system/update/jobs/${encodeURIComponent(job.id)}/rollback`, {
        method: "POST",
        csrf: actions.csrf,
        body: { ownerPassword: rollbackPassword },
      });
      setRollbackPassword("");
      setRollbackOpen(false);
      await loadStatus();
      if (result.execPath && status.restartMode === "self-exec") {
        setReconnectTimedOut(false);
        setReconnecting(true);
      }
      actions.setToast("已回滚到更新前的 binary" + (result.execPath ? `（${result.execPath}）` : ""), "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  const canApply = Boolean(check?.canApply && check.latestVersion && !activeJob);
  const canSubmit = canApply && confirmService && confirmTasks && ownerPassword.trim().length > 0;

  return (
    <Panel
      title="系统更新"
      subtitle="手动检查 GitHub Releases，下载校验后只替换服务 binary"
      actions={
        <>
          <Button disabled={busy === "check"} onClick={() => void checkUpdates()}>
            检查更新
          </Button>
          <Button disabled={!canApply} onClick={() => setConfirmOpen(true)} tone="primary">
            下载并更新
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(280px,0.8fr)] gap-4 max-lg:grid-cols-1">
          <ContextList
            items={[
              ["当前版本", <span className="mono">{status.version?.version || "-"}</span>],
              ["Commit", <span className="mono">{status.version?.commit || "-"}</span>],
              ["构建时间", formatDate(status.version?.date) || "-"],
              ["平台", <span className="mono">{status.version?.os || "-"} / {status.version?.arch || "-"}</span>],
            ]}
          />
          <ContextList
            items={[
              ["最新版本", <span className="mono">{check?.latestVersion || "未检查"}</span>],
              ["检查时间", formatDate(check?.checkedAt) || "-"],
              ["安装包", <span className="mono">{check?.assetName || "-"}</span>],
              ["状态", <UpdateStatePill status={status} />],
            ]}
          />
        </div>

        <SupervisorCard status={status} />

        {check?.releaseUrl ? (
          <a className="mono text-xs text-[var(--accent)] underline decoration-[rgba(207,77,16,0.34)] underline-offset-2" href={check.releaseUrl} rel="noreferrer" target="_blank">
            {check.releaseUrl}
          </a>
        ) : null}

        {check?.reason && !check.canApply ? <Notice>{updateReasonLabel(check.reason)}</Notice> : null}
        {check?.errorMessage ? <Notice>{check.errorMessage}</Notice> : null}

        {confirmOpen ? (
          <div className="grid gap-3 card-soft">
            <div className="grid gap-1">
              <strong className="text-sm">确认更新到 <span className="mono">{check?.latestVersion}</span></strong>
              <span className="muted text-xs">更新会短暂中断服务，当前运行中的异步任务可能停止。数据目录、SQLite 和配置文件不会被覆盖。</span>
            </div>
            <label className="field">
              <span>管理员密码</span>
              <input className="input" onChange={(event) => setOwnerPassword(event.target.value)} type="password" value={ownerPassword} />
            </label>
            <CheckLabel
              align="start"
              checked={confirmService}
              onChange={(checked) => setConfirmService(checked)}
            >
              我确认服务会短暂不可用，并由 supervisor 或手动方式重新启动。
            </CheckLabel>
            <CheckLabel
              align="start"
              checked={confirmTasks}
              onChange={(checked) => setConfirmTasks(checked)}
            >
              我确认正在执行的异步任务可能被中断。
            </CheckLabel>
            <div className="flex flex-wrap justify-end gap-2">
              <Button onClick={() => setConfirmOpen(false)}>取消</Button>
              <Button disabled={!canSubmit || busy === "apply"} onClick={() => void applyUpdate()} tone="primary">
                确认更新
              </Button>
            </div>
          </div>
        ) : null}

        {visibleJob ? (
          <div className="grid gap-3 card-soft">
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div>
                <strong className="text-sm">更新任务 <span className="mono">{visibleJob.id}</span></strong>
                <p className="muted mt-1 mb-0 text-xs">{jobPhaseLabel(visibleJob.phase)} · {jobStatusLabel(visibleJob.status)}</p>
              </div>
              <div className="flex flex-wrap justify-end gap-2">
                {activeJob && activeJob.phase !== "installing" && activeJob.phase !== "restarting" ? (
                  <Button disabled={busy === "cancel"} onClick={() => void cancelUpdate(activeJob)}>
                    取消下载
                  </Button>
                ) : null}
                {visibleJob && (visibleJob.status === "failed" || visibleJob.status === "completed") && status.backupBinaryPath ? (
                  <Button disabled={busy === "rollback"} onClick={() => { setRollbackPassword(""); setRollbackOpen(true); }} tone="danger">
                    回滚
                  </Button>
                ) : null}
              </div>
            </div>
            <div className="grid gap-2">
              <div className="h-2 overflow-hidden rounded-md bg-[var(--line)]">
                <div className="h-full bg-[var(--accent)] transition-[width]" style={{ width: `${progress}%` }} />
              </div>
              <div className="flex flex-wrap justify-between gap-2 text-xs">
                <span className="mono">{formatBytesIEC(visibleJob.bytesDownloaded)} / {formatBytesIEC(visibleJob.totalBytes)}</span>
                <span className="mono">{progress}%</span>
              </div>
            </div>
            {visibleJob.errorMessage ? <Notice>{visibleJob.errorMessage}</Notice> : null}
            {reconnecting ? <Notice>服务正在重启。页面会自动尝试重新连接并确认版本。</Notice> : null}
            {reconnectTimedOut ? (
              <Notice tone="danger">
                服务重启超时,新版本可能无法启动。如果备份 binary 可用,请使用「回滚」按钮回到上一稳定版本,或手动检查 binary 运行状态。
              </Notice>
            ) : null}
            {activeJob?.phase === "restarting" && status.restartMode === "none" ? (
              <Notice tone="warn">
                更新已完成,当前 restart_mode = none,请手动重启 binary 使新版本生效。
              </Notice>
            ) : null}
            {rollbackOpen && visibleJob ? (
              <div className="grid gap-3 card-soft">
                <div className="grid gap-1">
                  <strong className="text-sm">确认回滚到上一版本 <span className="mono">{visibleJob.currentVersion || "备份"}</span></strong>
                  <span className="muted text-xs">会用保存的备份 binary 原子覆盖当前 binary,回滚成功后会自动重启服务。</span>
                </div>
                <label className="field">
                  <span>管理员密码</span>
                  <input className="input" onChange={(event) => setRollbackPassword(event.target.value)} type="password" value={rollbackPassword} />
                </label>
                <div className="flex flex-wrap justify-end gap-2">
                  <Button onClick={() => { setRollbackOpen(false); setRollbackPassword(""); }}>取消</Button>
                  <Button
                    disabled={busy === "rollback" || !rollbackPassword.trim()}
                    onClick={() => void rollbackLatest(visibleJob)}
                    tone="danger"
                  >
                    确认回滚
                  </Button>
                </div>
              </div>
            ) : null}
            {events.length ? (
              <div className="grid gap-1 border-t border-[var(--line)] pt-2">
                {events.slice(-6).map((event) => (
                  <div className="grid grid-cols-[150px_minmax(0,1fr)] gap-2 text-xs max-sm:grid-cols-1" key={`${event.sequence}-${event.type}`}>
                    <span className="muted mono">{formatDate(event.createdAt) || "-"}</span>
                    <span>{eventLabel(event)}</span>
                  </div>
                ))}
              </div>
            ) : null}
            {(status.installBinaryPath || status.backupBinaryPath) ? (
              <div className="grid gap-1 text-xs muted mono border-t border-[var(--line)] pt-2">
                <span>安装路径: {status.installBinaryPath || "—"}</span>
                <span>备份路径: {status.backupBinaryPath || "—"}</span>
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
    </Panel>
  );
}

function UpdateStatePill({ status }: { status: SystemUpdateStatus }) {
  const check = status.latestCheck;
  if (status.activeJob) return <Pill tone="warn">更新中</Pill>;
  if (!status.enabled) return <Pill tone="warn">已禁用</Pill>;
  if (!check) return <Pill>未检查</Pill>;
  if (check.errorMessage) return <Pill tone="danger">检查失败</Pill>;
  if (check.canApply) return <Pill tone="good">可更新</Pill>;
  if (check.updateAvailable) return <Pill tone="warn">不可安装</Pill>;
  return <Pill tone="good">已是最新</Pill>;
}

// SupervisorCard renders a health panel for the phantom-supervisor process.
// It reports whether the supervisor is actually alive (via real PID+kill-0),
// shows the supervisor and child PIDs, and surfaces any diagnostic error so
// operators can debug "why was the process not restarted" right from the UI.
function SupervisorCard({ status }: { status: SystemUpdateStatus }) {
  const sup = status.supervisor;
  const underSupervisor = sup?.underSupervisor ?? status.underSupervisor ?? false;
  const alive = sup?.alive ?? false;
  const pid = sup?.pid ?? status.supervisorPID ?? 0;
  const childPID = sup?.childPID ?? 0;
  const lastError = sup?.lastError;
  const pidSource = sup?.pidSource;

  // When no supervisor is present we render a gentle "not configured" card
  // instead of hiding the section — the card acts as a discoverability hint.
  const configured = underSupervisor || pid > 0;

  const pill = (() => {
    if (!configured) return <Pill tone="neutral">未启用</Pill>;
    if (alive) return <Pill tone="good">运行中</Pill>;
    if (underSupervisor) return <Pill tone="danger">存活检查失败</Pill>;
    return <Pill tone="warn">PID 存在但不可达</Pill>;
  })();

  return (
    <div className="card-soft grid gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="grid gap-0.5">
          <strong className="text-sm">Supervisor 守护进程</strong>
          <span className="muted text-xs">
            由 <span className="mono">scripts/start.sh</span> 启动的外部保活进程，负责退出重启与版本回滚。
          </span>
        </div>
        {pill}
      </div>
      <ContextList
        items={[
          [
            "保活模式",
            configured
              ? <span className="mono">supervisor / {status.restartMode || "-"}</span>
              : <span className="mono">{status.restartMode || "未配置"}</span>,
          ],
          ["Supervisor PID", pid ? <span className="mono">{pid}{pidSource ? `（${pidSourceLabel(pidSource)}）` : ""}</span> : "-"],
          ["主进程 PID", childPID ? <span className="mono">{childPID}</span> : "-"],
          ["自动重启", configured && alive ? <Pill tone="good">是</Pill> : configured ? <Pill tone="warn">不可达</Pill> : <Pill tone="neutral">否</Pill>],
        ]}
      />
      {!configured ? (
        <Notice tone="warn">
          当前部署未启用 Supervisor。裸部署场景请改用 <span className="mono">scripts/manage.sh start</span> 启动服务，以便新版升级后能够被自动拉起。
        </Notice>
      ) : null}
      {configured && !alive ? (
        <Notice tone="danger">
          {lastError ? (
            <>Supervisor 存活检查失败: <span className="mono">{lastError}</span>。请使用 <span className="mono">scripts/manage.sh status</span> 确认守护进程状态。</>
          ) : (
            <>Supervisor 响应异常，请使用 <span className="mono">scripts/manage.sh status</span> 确认守护进程状态。</>
          )}
        </Notice>
      ) : null}
    </div>
  );
}

function pidSourceLabel(value?: string): string {
  switch (value) {
    case "env":
      return "环境变量";
    case "pidfile":
      return "PID 文件";
    default:
      return value || "";
  }
}

function updateReasonLabel(reason: string): string {
  // supervisorReasonHints covers reconciler-injected reasons such as
  // "manual_upgrade_detected" — they are not produced by the upstream
  // checker, so they live in a separate lookup table.
  if (supervisorReasonHints[reason]) return supervisorReasonHints[reason];
  const labels: Record<string, string> = {
    "current version is up to date": "当前版本已是最新。",
    "current platform is not supported by the release asset": "当前平台没有匹配的 release 包。",
    "matching release asset is missing": "最新 release 缺少当前平台的安装包。",
    "checksum asset is missing": "最新 release 缺少 checksum 文件，不能安装。",
    "release check failed": "检查 release 失败。",
    "updates are disabled": "系统更新功能已禁用。",
  };
  return labels[reason] || reason;
}

function jobPhaseLabel(value?: string): string {
  return (
    {
      created: "准备中",
      downloading: "下载中",
      verifying: "校验中",
      extracting: "解包中",
      installing: "安装中",
      restarting: "重启中",
      completed: "已完成",
    }[value || ""] ||
    value ||
    "未知阶段"
  );
}

function jobStatusLabel(value?: string): string {
  return (
    {
      queued: "排队中",
      running: "运行中",
      restarting: "等待重启",
      completed: "成功",
      failed: "失败",
      cancelled: "已取消",
    }[value || ""] ||
    value ||
    "未知状态"
  );
}

function eventLabel(event: EventRecord): string {
  const labels: Record<string, string> = {
    "update.job.created": "更新任务已创建",
    "update.job.updated": "更新任务状态已更新",
    "update.job.interrupted": "更新任务在服务重启时被自动置为失败",
    "update.download.started": "开始下载 release 包",
    "update.download.progress": "下载进度已更新",
    "update.download.completed": "下载完成",
    "update.verify.completed": "checksum 校验完成",
    "update.extract.completed": "安装包解包完成",
    "update.install.completed": "新版本 binary 已安装",
    "update.restart.requested": "已请求服务重启",
    "update.failed": "更新失败",
    "update.cancelled": "更新已取消",
    "update.completed": "更新已完成并确认生效",
    "update.rollback.applied": "已回滚到备份 binary",
    "system.update.rollback": "手动回滚已执行",
    "system.update.rollback_auto": "自动回滚已执行",
  };
  return labels[event.type] || event.type;
}
