import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type {
  DockerControlStatus,
  DockerContainerSummary,
  DockerImageSummary,
  DockerJob,
  DockerLogLine,
  DockerNetworkSummary,
  DockerRegistryCredential,
  DockerRegistryRepository,
  DockerRegistrySettings,
  DockerRegistryStatus,
  DockerRegistryTag,
  DockerStatus,
  DockerVolumeSummary,
  EventRecord,
  ObjectStorageProfile,
} from "../app/types";
import { Button, ContextList, EmptyState, Field, Panel, Pill, SubTabs, useDangerConfirm } from "../components/ui";
import { formatBytesZero } from "../utils/format";
import { friendlyError } from "../api/client";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { DockerTable } from "./docker/DockerTable";
import { HostOperationsPanel } from "./docker/HostOperationsPanel";
import { RegistryPanel } from "./docker/RegistryPanel";

type DockerTab = "overview" | "registry" | "containers" | "images" | "volumes" | "networks" | "settings";
type DockerOperationResult = { job?: DockerJob; eventScope?: string; eventScopeId?: string };

const TABS: { id: DockerTab; label: string }[] = [
  { id: "overview", label: "总览" },
  { id: "registry", label: "Registry" },
  { id: "containers", label: "容器" },
  { id: "images", label: "镜像" },
  { id: "volumes", label: "卷" },
  { id: "networks", label: "网络" },
  { id: "settings", label: "主机操作" },
];
const DOCKER_TAB_IDS: DockerTab[] = TABS.map((item) => item.id);
const DOCKER_CLEAR_KEYS = ["codex", "codexInbox", "codexRuntime", "gateway", "images", "settings"];


function formatUnix(seconds: number): string {
  if (!seconds) return "-";
  return new Date(seconds * 1000).toLocaleString();
}

function containerTone(state: string): "good" | "warn" | "danger" | "neutral" {
  if (state === "running") return "good";
  if (state === "exited" || state === "dead") return "danger";
  if (state === "paused" || state === "restarting" || state === "created") return "warn";
  return "neutral";
}

function eventMessage(event: EventRecord): string {
  const payload = event.payload || {};
  const message = typeof payload.message === "string" ? payload.message : "";
  const error = typeof payload.error === "string" ? payload.error : "";
  if (message) return message;
  if (error) return error;
  if (event.type === "docker.job.created") return "任务已创建";
  if (event.type === "docker.job.started") return "任务已开始";
  if (event.type === "docker.job.completed") return "任务已完成";
  if (event.type === "docker.job.failed") return "任务失败";
  return event.type;
}

function streamTone(event: EventRecord): string {
  const stream = event.payload?.stream;
  if (event.type === "docker.job.failed" || stream === "stderr") return "text-[var(--danger)]";
  return "";
}

function containerName(container: DockerContainerSummary): string {
  return container.names[0] || container.id;
}

export function DockerView({ actions }: { actions: AppActions }) {
  const [tab, setTab, tabHref] = useQueryParamState<DockerTab>("docker", DOCKER_TAB_IDS, "overview", { clearKeys: DOCKER_CLEAR_KEYS });
  const [status, setStatus] = useState<DockerStatus | null>(null);
  const [control, setControl] = useState<DockerControlStatus | null>(null);
  const [containers, setContainers] = useState<DockerContainerSummary[]>([]);
  const [images, setImages] = useState<DockerImageSummary[]>([]);
  const [volumes, setVolumes] = useState<DockerVolumeSummary[]>([]);
  const [networks, setNetworks] = useState<DockerNetworkSummary[]>([]);
  const [registryStatus, setRegistryStatus] = useState<DockerRegistryStatus | null>(null);
  const [registrySettings, setRegistrySettings] = useState<DockerRegistrySettings>({});
  const [repositories, setRepositories] = useState<DockerRegistryRepository[]>([]);
  const [credentials, setCredentials] = useState<DockerRegistryCredential[]>([]);
  const [objectProfiles, setObjectProfiles] = useState<ObjectStorageProfile[]>([]);
  const [selectedRepo, setSelectedRepo] = useState("");
  const [repoTags, setRepoTags] = useState<DockerRegistryTag[]>([]);
  const [newCredentialSecret, setNewCredentialSecret] = useState("");
  const [credentialName, setCredentialName] = useState("personal-laptop");
  const [credentialPrefix, setCredentialPrefix] = useState("personal/");
  const [createName, setCreateName] = useState("managed-app");
  const [createImage, setCreateImage] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [pullRef, setPullRef] = useState("");
  const [logsFor, setLogsFor] = useState<DockerContainerSummary | null>(null);
  const [logLines, setLogLines] = useState<DockerLogLine[]>([]);
  const [logsLoading, setLogsLoading] = useState(false);
  const [job, setJob] = useState<DockerJob | null>(null);
  const [jobEvents, setJobEvents] = useState<EventRecord[]>([]);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const loadStatus = useCallback(async () => {
    try {
      const response = await actions.api<{ status: DockerStatus; control?: DockerControlStatus }>("/api/docker/status");
      setStatus(response.status);
      if (response.control) {
        setControl(response.control);
        setJob(response.control.activeJob || response.control.latestJob || null);
      }
      return response.status;
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      return null;
    }
  }, [actions]);

  async function loadRegistry() {
    const [statusRes, settingsRes, reposRes, credsRes, profilesRes] = await Promise.all([
      actions.api<{ status?: DockerRegistryStatus }>("/api/docker/registry/status"),
      actions.api<{ settings?: DockerRegistrySettings }>("/api/docker/registry/settings"),
      actions.api<{ items?: DockerRegistryRepository[] }>("/api/docker/registry/repositories"),
      actions.api<{ items?: DockerRegistryCredential[] }>("/api/docker/registry/credentials"),
      actions.api<{ items?: ObjectStorageProfile[] }>("/api/object-storage/profiles"),
    ]);
    setRegistryStatus(statusRes.status || null);
    setRegistrySettings(settingsRes.settings || {});
    setRepositories(reposRes.items || []);
    setCredentials(credsRes.items || []);
    setObjectProfiles(profilesRes.items || []);
  }

  const loadTab = useCallback(
    async (active: DockerTab, available: boolean) => {
      if (active === "registry" || active === "settings") {
        setLoading(true);
        try {
          await loadRegistry();
        } finally {
          setLoading(false);
        }
        return;
      }
      if (!available || active === "overview") return;
      setLoading(true);
      try {
        if (active === "containers") {
          const response = await actions.api<{ items?: DockerContainerSummary[] }>("/api/docker/containers");
          setContainers(response.items || []);
        } else if (active === "images") {
          const response = await actions.api<{ items?: DockerImageSummary[] }>("/api/docker/images");
          setImages(response.items || []);
        } else if (active === "volumes") {
          const response = await actions.api<{ items?: DockerVolumeSummary[] }>("/api/docker/volumes");
          setVolumes(response.items || []);
        } else if (active === "networks") {
          const response = await actions.api<{ items?: DockerNetworkSummary[] }>("/api/docker/networks");
          setNetworks(response.items || []);
        }
      } catch (error) {
        actions.setToast(friendlyError(error), "danger");
      } finally {
        setLoading(false);
      }
    },
    [actions],
  );

  const loadControl = useCallback(async () => {
    const response = await actions.api<DockerControlStatus>("/api/docker/control-status");
    setControl(response);
    setJob(response.activeJob || response.latestJob || null);
    return response;
  }, [actions]);

  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  useEffect(() => {
    if (status) void loadTab(tab, status.available);
  }, [tab, status, loadTab]);

  const available = Boolean(status?.available);

  async function refresh() {
    const next = await loadStatus();
    if (next) await loadTab(tab, next.available);
  }

  function attachJob(result: DockerOperationResult, message: string) {
    if (result.job) {
      setJob(result.job);
      setJobEvents([]);
      actions.setToast(`${message}，任务已开始`, "good");
      return;
    }
    actions.setToast(message, "good");
  }

  async function containerAction(container: DockerContainerSummary, action: "start" | "stop" | "restart" | "kill") {
    if (action === "kill") {
      const confirmed = await confirmDanger({
        title: "强制结束容器",
        objectName: containerName(container),
        body: "该操作会立即终止容器进程，不等待应用完成优雅退出。",
        confirmLabel: "Kill 容器",
        impact: ["容器内正在写入的数据可能丢失。", "不会删除容器、镜像或命名卷。"],
        recovery: "如果只是常规停机，优先使用停止或重启。",
      });
      if (!confirmed) return;
    }
    setBusy(`${action}-${container.id}`);
    try {
      const result = await actions.api<DockerOperationResult>(`/api/docker/containers/${container.id}/${action}`, { method: "POST", csrf: actions.csrf });
      attachJob(result, "操作已提交");
      await loadTab("containers", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function removeContainer(container: DockerContainerSummary) {
    const running = container.state === "running";
    const confirmed = await confirmDanger({
      title: "删除容器",
      objectName: containerName(container),
      body: running ? "容器仍在运行，删除请求会强制移除该容器。" : "该操作会移除容器记录和可写层。",
      confirmLabel: "删除容器",
      impact: [
        "容器可写层会被删除。",
        "命名卷不会被删除。",
        running ? "运行中的进程会被停止。" : "容器镜像不受影响。",
      ],
      recovery: "删除容器通常不可恢复；需要时只能从镜像和持久化卷重新创建。",
    });
    if (!confirmed) return;
    setBusy(`remove-${container.id}`);
    try {
      const result = await actions.api<DockerOperationResult>(`/api/docker/containers/${container.id}${running ? "?force=true" : ""}`, { method: "DELETE", csrf: actions.csrf });
      attachJob(result, "删除容器已提交");
      await loadTab("containers", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function openLogs(container: DockerContainerSummary) {
    setLogsFor(container);
    setLogLines([]);
    setLogsLoading(true);
    try {
      const response = await actions.api<{ lines?: DockerLogLine[] }>(`/api/docker/containers/${container.id}/logs?tail=200`);
      setLogLines(response.lines || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLogsLoading(false);
    }
  }

  async function pullImage() {
    const ref = pullRef.trim();
    if (!ref) {
      actions.setToast("请填写镜像引用", "warn");
      return;
    }
    setBusy("pull");
    try {
      const result = await actions.api<DockerOperationResult>("/api/docker/images/pull", { method: "POST", csrf: actions.csrf, body: { reference: ref } });
      setPullRef("");
      attachJob(result, "镜像拉取已提交");
      await loadTab("images", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function removeImage(image: DockerImageSummary) {
    const label = image.tags && image.tags.length ? image.tags[0] : image.id;
    const confirmed = await confirmDanger({
      title: "删除镜像",
      objectName: label,
      body: "该操作会从本机 Docker image store 移除镜像引用，并使用 force 删除。",
      confirmLabel: "删除镜像",
      impact: ["依赖该镜像创建新容器会失败，除非重新 pull 或 build。", "已运行容器不会被直接删除。"],
      recovery: "镜像删除后通常需要从 registry 重新拉取或重新构建。",
    });
    if (!confirmed) return;
    setBusy(`rmi-${image.id}`);
    try {
      const result = await actions.api<DockerOperationResult>(`/api/docker/images/${encodeURIComponent(image.id)}?force=true`, { method: "DELETE", csrf: actions.csrf });
      attachJob(result, "删除镜像已提交");
      await loadTab("images", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveDockerSettings(next: { installEnabled?: boolean; daemonControlEnabled?: boolean; containerCreateEnabled?: boolean }) {
    const current = control?.settings || {};
    setBusy("docker-settings");
    try {
      const response = await actions.api<{ control?: DockerControlStatus }>("/api/docker/settings", {
        method: "PATCH",
        csrf: actions.csrf,
        body: { ...current, ...next },
      });
      setControl(response.control || (await loadControl()));
      actions.setToast("Docker 高权限开关已更新", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function installDocker() {
    const confirmed = await confirmDanger({
      title: "安装 Docker daemon",
      body: "该操作会通过后端受控任务修改系统包源和 systemd 服务。",
      confirmLabel: "安装 Docker",
      impact: ["会写入系统级包和服务配置。", "安装任务会进入 Docker job 事件流。"],
      recovery: "本控制台不提供一键卸载，执行前请确认当前主机环境可信。",
    });
    if (!confirmed) return;
    setBusy("install");
    try {
      const result = await actions.api<DockerOperationResult>("/api/docker/install", { method: "POST", csrf: actions.csrf });
      attachJob(result, "Docker 安装已提交");
      await loadControl();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function daemonAction(action: "start" | "stop" | "restart") {
    const labels = { start: "启动", stop: "停止", restart: "重启" };
    const confirmed = await confirmDanger({
      title: `${labels[action]} Docker daemon`,
      body: "该操作会影响本机 Docker 服务，而不只影响当前控制台。",
      confirmLabel: `${labels[action]} daemon`,
      impact: ["本机所有 Docker 容器都会受到 daemon 状态变化影响。", "操作结果会进入 Docker job 事件流。"],
      recovery: action === "stop" ? "停止 daemon 后，依赖 Docker 的任务需要重新启动服务才能恢复。" : "如果操作失败，保留当前状态并显示错误摘要。",
    });
    if (!confirmed) return;
    setBusy(`daemon-${action}`);
    try {
      const result = await actions.api<DockerOperationResult>(`/api/docker/daemon/${action}`, { method: "POST", csrf: actions.csrf });
      attachJob(result, `Docker daemon ${labels[action]}已提交`);
      await loadControl();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveRegistrySettings(next: DockerRegistrySettings) {
    setBusy("registry-settings");
    try {
      const response = await actions.api<{ settings?: DockerRegistrySettings; status?: DockerRegistryStatus }>("/api/docker/registry/settings", {
        method: "PUT",
        csrf: actions.csrf,
        body: { ...registrySettings, ...next },
      });
      setRegistrySettings(response.settings || {});
      setRegistryStatus(response.status || registryStatus);
      actions.setToast("Registry 设置已保存", "good");
      await loadRegistry();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function createCredential() {
    if (!credentialName.trim()) {
      actions.setToast("请填写凭据名称", "warn");
      return;
    }
    setBusy("credential-create");
    try {
      const response = await actions.api<{ credential?: DockerRegistryCredential; secret?: string }>("/api/docker/registry/credentials", {
        method: "POST",
        csrf: actions.csrf,
        body: { name: credentialName.trim(), repositoryPrefix: credentialPrefix.trim(), scopes: ["registry.pull", "registry.push"] },
      });
      setNewCredentialSecret(response.secret || "");
      await loadRegistry();
      actions.setToast("Registry 凭据已创建", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function rotateCredential(item: DockerRegistryCredential) {
    const confirmed = await confirmDanger({
      title: "轮换 Registry 凭据",
      objectName: item.name,
      body: "系统会生成新的 secret，旧 secret 将立即失效。",
      confirmLabel: "轮换凭据",
      impact: ["使用旧 secret 的 docker login 会失效。", "新 secret 只会在本次结果中展示一次。"],
      recovery: "如客户端没有及时更新，需要再次轮换或重新创建凭据。",
    });
    if (!confirmed) return;
    setBusy(`cred-rotate-${item.id}`);
    try {
      const response = await actions.api<{ secret?: string }>(`/api/docker/registry/credentials/${item.id}/rotate`, { method: "POST", csrf: actions.csrf });
      setNewCredentialSecret(response.secret || "");
      await loadRegistry();
      actions.setToast("Registry 凭据已轮换", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function deleteCredential(item: DockerRegistryCredential) {
    const confirmed = await confirmDanger({
      title: "删除 Registry 凭据",
      objectName: item.name,
      body: "该凭据会立即失效，使用它的客户端无法继续 push 或 pull。",
      confirmLabel: "删除凭据",
      impact: [`Repository prefix: ${item.repositoryPrefix || "-"}`, "已分发到客户端的 secret 不再可用。"],
      recovery: "删除不可恢复；需要重新创建凭据并重新登录客户端。",
    });
    if (!confirmed) return;
    setBusy(`cred-delete-${item.id}`);
    try {
      await actions.api(`/api/docker/registry/credentials/${item.id}`, { method: "DELETE", csrf: actions.csrf });
      await loadRegistry();
      actions.setToast("Registry 凭据已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function openRepository(repo: string) {
    setSelectedRepo(repo);
    try {
      const response = await actions.api<{ items?: DockerRegistryTag[] }>(`/api/docker/registry/repositories/${repo}/tags`);
      setRepoTags(response.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function deleteTag(item: DockerRegistryTag) {
    const confirmed = await confirmDanger({
      title: "删除 Registry tag",
      objectName: `${item.repository}:${item.tag}`,
      body: "该操作会删除 registry manifest tag，但不会立即释放底层 blob。",
      confirmLabel: "删除 tag",
      impact: ["客户端不能再通过该 tag 拉取 manifest。", "释放 layer/config blob 需要后续执行 Registry GC。"],
      recovery: "删除 tag 后通常需要重新 push 才能恢复同名 tag。",
    });
    if (!confirmed) return;
    setBusy(`tag-delete-${item.repository}-${item.tag}`);
    try {
      await actions.api(`/api/docker/registry/repositories/${item.repository}/tags/${item.tag}`, { method: "DELETE", csrf: actions.csrf });
      await openRepository(item.repository);
      await loadRegistry();
      actions.setToast("Registry tag 已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function runRegistryGC() {
    const confirmed = await confirmDanger({
      title: "执行 Registry GC",
      body: "该任务会清理过期 upload 临时文件，并回收未被任何 manifest 引用的 layer/config blob。",
      confirmLabel: "执行 GC",
      impact: ["未被 manifest 引用的 blob 会被删除。", "运行期间 registry 读写可能短暂受影响。"],
      recovery: "GC 删除的 blob 不可恢复；请确认没有并发 push 依赖这些临时对象。",
    });
    if (!confirmed) return;
    setBusy("registry-gc");
    try {
      const result = await actions.api<DockerOperationResult>("/api/docker/registry/gc", { method: "POST", csrf: actions.csrf });
      attachJob(result, "Registry GC 已提交");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function createContainer() {
    if (!control?.settings?.containerCreateEnabled) {
      actions.setToast("请先在主机操作中开启模板化容器创建", "warn");
      return;
    }
    if (!createName.trim() || !createImage.trim()) {
      actions.setToast("请填写容器名和镜像", "warn");
      return;
    }
    const confirmed = await confirmDanger({
      title: "创建并启动容器",
      objectName: createName,
      body: "该操作会使用受控模板创建容器并立即启动。",
      confirmLabel: "创建容器",
      impact: [`Image: ${createImage}`, "当前模板不允许 host path、privileged、host network 或自由参数。"],
      recovery: "创建失败不会保留半成品；创建成功后可在容器列表中停止或删除。",
    });
    if (!confirmed) return;
    setBusy("container-create");
    try {
      const result = await actions.api<DockerOperationResult>("/api/docker/containers", {
        method: "POST",
        csrf: actions.csrf,
        body: { name: createName.trim(), image: createImage.trim() },
      });
      attachJob(result, "容器创建已提交");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  useEffect(() => {
    if (!job?.eventScope || !job.eventScopeId) return;
    let closed = false;
    const params = new URLSearchParams({ scope: job.eventScope, id: job.eventScopeId });
    void actions
      .api<{ items?: EventRecord[] }>(`/api/events/history?${params.toString()}`)
      .then((history) => {
        if (!closed) setJobEvents(history.items || []);
      })
      .catch(() => undefined);
    const source = new EventSource(`/api/events/stream?${params.toString()}`);
    const handle = (event: MessageEvent<string>) => {
      try {
        const record = JSON.parse(event.data) as EventRecord;
        setJobEvents((current) => [...current.slice(-120), record]);
        if (record.type === "docker.job.completed" || record.type === "docker.job.failed") {
          void refresh().catch(() => undefined);
          void loadControl().catch(() => undefined);
        }
      } catch {
        // 下一次状态刷新会对齐任务状态。
      }
    };
    ["docker.job.created", "docker.job.started", "docker.job.output", "docker.job.completed", "docker.job.failed"].forEach((name) => source.addEventListener(name, handle));
    source.onerror = () => {
      if (!closed) source.close();
    };
    return () => {
      closed = true;
      ["docker.job.created", "docker.job.started", "docker.job.output", "docker.job.completed", "docker.job.failed"].forEach((name) => source.removeEventListener(name, handle));
      source.close();
    };
  }, [actions, job?.eventScope, job?.eventScopeId, loadControl]);

  return (
    <>
      <div className="grid gap-4 p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="m-0 text-sm font-semibold">Docker 主机</h2>
          <p className="muted mt-1 mb-0 text-xs">本机 Docker 守护进程控制面。容器生命周期由 dockerd 管理，独立于 Phantom Lancer。</p>
        </div>
        <div className="flex items-center gap-2">
          <Pill tone={available ? "good" : "warn"}>{available ? "daemon 可用" : "daemon 不可用"}</Pill>
          <Button onClick={() => void refresh()}>{loading ? "加载中" : "刷新"}</Button>
        </div>
      </div>
	      <SubTabs
	        activeId={tab}
	        onChange={(id) => setTab(id as DockerTab)}
	        tabs={TABS.map((item) => ({ ...item, href: tabHref(item.id) }))}
	      />
      {tab === "registry" ? (
          <RegistryPanel
            busy={busy}
            createCredential={() => void createCredential()}
            credentialName={credentialName}
            credentialPrefix={credentialPrefix}
            credentials={credentials}
            deleteCredential={(item) => void deleteCredential(item)}
            deleteTag={(item) => void deleteTag(item)}
            formatBytes={formatBytesZero}
            loading={loading}
            newCredentialSecret={newCredentialSecret}
            objectProfiles={objectProfiles}
            openRepository={(repo) => void openRepository(repo)}
            registrySettings={registrySettings}
            registryStatus={registryStatus}
            repoTags={repoTags}
            repositories={repositories}
            rotateCredential={(item) => void rotateCredential(item)}
            runRegistryGC={() => void runRegistryGC()}
            saveRegistrySettings={(settings) => void saveRegistrySettings(settings)}
            selectedRepo={selectedRepo}
            setCredentialName={setCredentialName}
            setCredentialPrefix={setCredentialPrefix}
            setRegistrySettings={setRegistrySettings}
          />
        ) : tab === "settings" ? (
          <HostOperationsPanel
            busy={busy}
            control={control}
            createContainer={() => void createContainer()}
            createImage={createImage}
            createName={createName}
            daemonAction={(action) => void daemonAction(action)}
            installDocker={() => void installDocker()}
            loadControl={() => void loadControl()}
            registryPublicUrl={registrySettings.publicUrl}
            saveDockerSettings={(next) => void saveDockerSettings(next)}
            setCreateImage={setCreateImage}
            setCreateName={setCreateName}
          />
        ) : !available ? (
          <Panel>
            <EmptyState
              title="Docker daemon 不可用"
              body={status?.lastError ? `原因：${status.lastError}` : "未检测到本机 Docker daemon。请确认 Docker 已安装并运行，且服务对 docker socket 有访问权限。"}
            />
          </Panel>
        ) : tab === "overview" ? (
          <Panel title="Docker 概览" subtitle="只读展示本机 daemon 能力和资源摘要。">
            <ContextList
              items={[
                ["状态", <Pill tone="good">可用</Pill>],
                ["Server 版本", status?.serverVersion || "-"],
                ["API 版本", status?.apiVersion || "-"],
                ["操作系统", status?.os || "-"],
                ["架构", status?.architecture || "-"],
                ["存储驱动", status?.storageDriver || "-"],
                ["Rootless", status?.rootless ? "是" : "否"],
                ["容器", `${status?.containersRunning || 0} 运行 / ${status?.containers || 0} 总数`],
                ["镜像", String(status?.images || 0)],
              ]}
            />
          </Panel>
        ) : tab === "containers" ? (
          <Panel title="容器" subtitle="常用生命周期操作保持可见，危险操作收进更多菜单。">
            <DockerTable
              empty="暂无容器"
              loading={loading}
              headers={["名称", "状态", "镜像", "端口", "操作"]}
              rows={containers.map((item) => ({
                key: item.id,
                cells: [
                  <span className="mono text-xs">{item.names[0] || item.id}</span>,
                  <span className="flex flex-col gap-1">
                    <Pill tone={containerTone(item.state)}>{item.state}</Pill>
                    <span className="muted text-xs">{item.status}</span>
                  </span>,
                  <span className="mono text-xs">{item.image}</span>,
                  <span className="mono text-xs">{(item.ports || []).join(", ") || "-"}</span>,
                  <ContainerActions
                    busy={busy}
                    item={item}
                    onAction={(action) => void containerAction(item, action)}
                    onLogs={() => void openLogs(item)}
                    onRemove={() => void removeContainer(item)}
                  />,
                ],
              }))}
            />
          </Panel>
        ) : tab === "images" ? (
          <Panel title="镜像">
            <div className="grid gap-3">
              <div className="flex flex-wrap items-end gap-2">
                <div className="min-w-60 flex-1">
                  <Field label="拉取镜像" help="例如 nginx:latest 或 registry.example.com/app:tag。">
                    <input className="input" onChange={(event) => setPullRef(event.target.value)} placeholder="repository:tag" value={pullRef} />
                  </Field>
                </div>
                <Button disabled={busy === "pull"} tone="primary" onClick={() => void pullImage()}>
                  {busy === "pull" ? "拉取中" : "拉取"}
                </Button>
              </div>
              <DockerTable
                empty="暂无镜像"
                loading={loading}
                headers={["标签", "ID", "大小", "创建时间", "操作"]}
                rows={images.map((item) => ({
                  key: item.id,
                  cells: [
                    <span className="mono text-xs">{item.tags && item.tags.length ? item.tags.join(", ") : item.id}</span>,
                    <span className="mono text-xs">{item.id}</span>,
                    <span className="text-xs">{formatBytesZero(item.sizeBytes)}</span>,
                    <span className="text-xs">{formatUnix(item.created)}</span>,
                    <Button disabled={busy === `rmi-${item.id}`} tone="danger" onClick={() => void removeImage(item)}>删除</Button>,
                  ],
                }))}
              />
            </div>
          </Panel>
        ) : tab === "volumes" ? (
          <Panel title="卷">
            <DockerTable
              empty="暂无卷"
              loading={loading}
              headers={["名称", "驱动", "挂载点"]}
              rows={volumes.map((item) => ({
                key: item.name,
                cells: [
                  <span className="mono text-xs">{item.name}</span>,
                  <span className="text-xs">{item.driver}</span>,
                  <span className="mono text-xs">{item.mountpoint || "-"}</span>,
                ],
              }))}
            />
          </Panel>
        ) : (
          <Panel title="网络">
            <DockerTable
              empty="暂无网络"
              loading={loading}
              headers={["名称", "驱动", "范围", "ID"]}
              rows={networks.map((item) => ({
                key: item.id,
                cells: [
                  <span className="mono text-xs">{item.name}</span>,
                  <span className="text-xs">{item.driver}</span>,
                  <span className="text-xs">{item.scope}</span>,
                  <span className="mono text-xs">{item.id}</span>,
                ],
              }))}
            />
          </Panel>
        )}

      {job ? (
        <Panel
          title={`Docker Job · ${job.title}`}
          subtitle={`${job.status}${job.target ? ` · ${job.target}` : ""}`}
          actions={<Pill tone={job.status === "failed" ? "danger" : job.status === "completed" ? "good" : "warn"}>{job.status}</Pill>}
        >
          {jobEvents.length ? (
            <pre className="mono max-h-80 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed">
              {jobEvents.map((item) => (
                <div className={streamTone(item)} key={item.id || `${item.type}-${item.sequence}`}>
                  {eventMessage(item)}
                </div>
              ))}
            </pre>
          ) : (
            <EmptyState title="等待任务事件" body="任务事件会通过 SSE 实时追加；刷新后也会从历史事件恢复。" />
          )}
        </Panel>
      ) : null}

      {logsFor ? (
        <Panel
          title={`容器日志 · ${logsFor.names[0] || logsFor.id}`}
          subtitle="最近 200 行，已脱敏并限制长度。"
          actions={<Button onClick={() => setLogsFor(null)}>关闭</Button>}
        >
          {logsLoading ? (
            <p className="muted text-sm">正在加载日志。</p>
          ) : logLines.length ? (
            <pre className="mono max-h-96 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed">
              {logLines.map((line, index) => (
                <div className={line.stream === "stderr" ? "text-[var(--danger)]" : ""} key={index}>
                  {line.text}
                </div>
              ))}
            </pre>
          ) : (
            <EmptyState title="暂无日志" body="该容器当前没有可显示的日志输出。" />
          )}
        </Panel>
      ) : null}
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function ContainerActions({
  busy,
  item,
  onAction,
  onLogs,
  onRemove,
}: {
  busy: string;
  item: DockerContainerSummary;
  onAction: (action: "start" | "stop" | "restart" | "kill") => void;
  onLogs: () => void;
  onRemove: () => void;
}) {
  const running = item.state === "running";
  return (
    <span className="flex flex-wrap items-center gap-1">
      {running ? (
        <Button disabled={busy === `stop-${item.id}`} onClick={() => onAction("stop")}>
          停止
        </Button>
      ) : (
        <Button disabled={busy === `start-${item.id}`} tone="primary" onClick={() => onAction("start")}>
          启动
        </Button>
      )}
      <Button onClick={onLogs}>日志</Button>
      <details className="relative">
        <summary className="button min-h-8 cursor-pointer list-none px-2 text-xs">更多</summary>
        <div className="absolute right-0 z-20 mt-1 grid min-w-28 gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-1 shadow-[var(--shadow)]">
          {running ? (
            <>
              <Button className="justify-start px-2 text-xs" disabled={busy === `restart-${item.id}`} onClick={() => onAction("restart")}>
                重启
              </Button>
              <Button className="justify-start px-2 text-xs" disabled={busy === `kill-${item.id}`} tone="danger" onClick={() => onAction("kill")}>
                Kill
              </Button>
            </>
          ) : null}
          <Button className="justify-start px-2 text-xs" disabled={busy === `remove-${item.id}`} tone="danger" onClick={onRemove}>
            删除
          </Button>
        </div>
      </details>
    </span>
  );
}
