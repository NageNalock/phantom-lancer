import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type {
  DockerControlStatus,
  DockerContainerInspectSummary,
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
  DockerStats,
  DockerStatus,
  DockerVolumeSummary,
  EventRecord,
  ObjectStorageProfile,
} from "../app/types";
import { Button, ContextList, EmptyState, Field, Metric, Panel, Pill, SubTabs, useDangerConfirm } from "../components/ui";
import { formatBytesZero } from "../utils/format";
import { friendlyError } from "../api/client";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { DockerTable, DockerValue } from "./docker/DockerTable";
import { HostOperationsPanel } from "./docker/HostOperationsPanel";
import { RegistryPanel } from "./docker/RegistryPanel";

type DockerTab = "overview" | "registry" | "containers" | "images" | "volumes" | "networks" | "events" | "settings";
type DockerOperationResult = { job?: DockerJob; eventScope?: string; eventScopeId?: string };

const TABS: { id: DockerTab; label: string }[] = [
  { id: "overview", label: "总览" },
  { id: "registry", label: "Registry" },
  { id: "containers", label: "容器" },
  { id: "images", label: "镜像" },
  { id: "volumes", label: "卷" },
  { id: "networks", label: "网络" },
  { id: "events", label: "Events / Jobs" },
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
  if (event.type === "docker.job.cancel.requested") return "已请求取消任务";
  if (event.type === "docker.job.cancelled") return "任务已取消";
  return event.type;
}

function streamTone(event: EventRecord): string {
  const stream = event.payload?.stream;
  if (event.type === "docker.job.failed" || stream === "stderr") return "text-[var(--danger)]";
  if (event.type === "docker.job.cancelled" || event.type === "docker.job.cancel.requested") return "text-[var(--warn)]";
  return "";
}

function containerName(container: DockerContainerSummary): string {
  return container.names[0] || container.id;
}

function registryHostFromPublicUrl(publicUrl: string | undefined): string {
  return (publicUrl || "").trim().replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").replace(/\/+$/, "");
}

function registryTagPullBusyKey(tag: DockerRegistryTag): string {
  return `registry-pull-${tag.repository}:${tag.tag}`;
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
  const [jobs, setJobs] = useState<DockerJob[]>([]);
  const [jobEvents, setJobEvents] = useState<EventRecord[]>([]);
  const [recentDockerEvents, setRecentDockerEvents] = useState<EventRecord[]>([]);
  const [selectedContainer, setSelectedContainer] = useState<DockerContainerSummary | null>(null);
  const [containerDetails, setContainerDetails] = useState<DockerContainerInspectSummary | null>(null);
  const [containerStats, setContainerStats] = useState<DockerStats | null>(null);
  const [containerDetailsLoading, setContainerDetailsLoading] = useState(false);
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

  const loadRegistry = useCallback(async (preferredRepo?: string) => {
    const [statusRes, settingsRes, reposRes, credsRes, profilesRes] = await Promise.all([
      actions.api<{ status?: DockerRegistryStatus }>("/api/docker/registry/status"),
      actions.api<{ settings?: DockerRegistrySettings }>("/api/docker/registry/settings"),
      actions.api<{ items?: DockerRegistryRepository[] }>("/api/docker/registry/repositories"),
      actions.api<{ items?: DockerRegistryCredential[] }>("/api/docker/registry/credentials"),
      actions.api<{ items?: ObjectStorageProfile[] }>("/api/object-storage/profiles"),
    ]);
    const nextRepos = reposRes.items || [];
    setRegistryStatus(statusRes.status || null);
    setRegistrySettings(settingsRes.settings || {});
    setRepositories(nextRepos);
    setCredentials(credsRes.items || []);
    setObjectProfiles(profilesRes.items || []);
    const nextRepo = nextRepos.some((item) => item.name === (preferredRepo || selectedRepo))
      ? (preferredRepo || selectedRepo)
      : nextRepos[0]?.name || "";
    if (!nextRepo) {
      setSelectedRepo("");
      setRepoTags([]);
      return;
    }
    setSelectedRepo(nextRepo);
    try {
      const tagsRes = await actions.api<{ items?: DockerRegistryTag[] }>(`/api/docker/registry/repositories/${nextRepo}/tags`);
      setRepoTags(tagsRes.items || []);
    } catch (error) {
      setRepoTags([]);
      actions.setToast(friendlyError(error), "danger");
    }
  }, [actions, selectedRepo]);

  const loadJobsAndEvents = useCallback(async () => {
    const [jobsRes, eventsRes] = await Promise.all([
      actions.api<{ items?: DockerJob[] }>("/api/docker/jobs?limit=80"),
      actions.api<{ items?: EventRecord[] }>("/api/docker/host/events?limit=120"),
    ]);
    setJobs(jobsRes.items || []);
    setRecentDockerEvents(eventsRes.items || []);
  }, [actions]);

  const loadTab = useCallback(
    async (active: DockerTab, available: boolean) => {
      if (active === "registry" || active === "settings") {
        setLoading(true);
        try {
          await loadRegistry();
          if (active === "settings") await loadJobsAndEvents();
        } finally {
          setLoading(false);
        }
        return;
      }
      if (active === "overview") {
        setLoading(true);
        try {
          await Promise.all([loadRegistry(), loadJobsAndEvents()]);
        } finally {
          setLoading(false);
        }
        return;
      }
      if (active === "events") {
        setLoading(true);
        try {
          await loadJobsAndEvents();
        } catch (error) {
          actions.setToast(friendlyError(error), "danger");
        } finally {
          setLoading(false);
        }
        return;
      }
      if (!available) return;
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
    [actions, loadJobsAndEvents, loadRegistry],
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
      setJobs((current) => [result.job as DockerJob, ...current.filter((item) => item.id !== result.job?.id)].slice(0, 80));
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

  async function openContainerDetails(container: DockerContainerSummary) {
    setSelectedContainer(container);
    setLogsFor(container);
    setContainerDetails(null);
    setContainerStats(null);
    setContainerDetailsLoading(true);
    setLogLines([]);
    setLogsLoading(true);
    try {
      const [detailsRes, statsRes, logsRes] = await Promise.all([
        actions.api<{ container?: DockerContainerInspectSummary }>(`/api/docker/containers/${container.id}/inspect`),
        actions.api<{ stats?: DockerStats }>(`/api/docker/containers/${container.id}/stats`).catch(() => ({ stats: undefined })),
        actions.api<{ lines?: DockerLogLine[] }>(`/api/docker/containers/${container.id}/logs?tail=200`),
      ]);
      setContainerDetails(detailsRes.container || null);
      setContainerStats(statsRes.stats || null);
      setLogLines(logsRes.lines || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setContainerDetailsLoading(false);
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
      setCreateImage(ref);
      attachJob(result, "镜像拉取已提交");
      await loadTab("images", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function pullRegistryTag(tag: DockerRegistryTag) {
    const host = registryHostFromPublicUrl(registrySettings.publicUrl || registryStatus?.publicUrl);
    if (!host) {
      actions.setToast("请先配置 Registry 公开 URL", "warn");
      return;
    }
    const ref = `${host}/${tag.repository}:${tag.tag}`;
    setBusy(registryTagPullBusyKey(tag));
    try {
      const result = await actions.api<DockerOperationResult>("/api/docker/images/pull", { method: "POST", csrf: actions.csrf, body: { reference: ref } });
      setPullRef(ref);
      setCreateImage(ref);
      attachJob(result, "Registry 镜像拉取已提交");
      setTab("images");
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

  async function createCredential(scopes: string[]) {
    if (!credentialName.trim()) {
      actions.setToast("请填写凭据名称", "warn");
      return;
    }
    setBusy("credential-create");
    try {
      const response = await actions.api<{ credential?: DockerRegistryCredential; secret?: string }>("/api/docker/registry/credentials", {
        method: "POST",
        csrf: actions.csrf,
        body: { name: credentialName.trim(), repositoryPrefix: credentialPrefix.trim(), scopes },
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

  async function setCredentialStatus(item: DockerRegistryCredential, status: "active" | "disabled") {
    setBusy(`cred-status-${item.id}`);
    try {
      await actions.api<{ credential?: DockerRegistryCredential }>(`/api/docker/registry/credentials/${item.id}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: { ...item, status },
      });
      await loadRegistry();
      actions.setToast(status === "active" ? "Registry 凭据已启用" : "Registry 凭据已停用", "good");
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

  function useTagForContainer(item: DockerRegistryTag) {
    const host = registryHostFromPublicUrl(registrySettings.publicUrl || registryStatus?.publicUrl);
    if (!host) {
      actions.setToast("请先配置 Registry 公开 URL", "warn");
      return;
    }
    const ref = `${host}/${item.repository}:${item.tag}`;
    setCreateImage(ref);
    setPullRef(ref);
    setTab("settings");
    actions.setToast("已带入容器创建表单", "good");
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
      setTab("containers");
      await loadTab("containers", true);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function cancelJob(item: DockerJob) {
    const confirmed = await confirmDanger({
      title: "取消 Docker job",
      objectName: item.title,
      body: "该操作会向后端受控任务发送取消信号；已经由 Docker daemon 完成的副作用不会自动回滚。",
      confirmLabel: "取消 job",
      impact: [`Job: ${item.id}`, item.target ? `目标：${item.target}` : "目标：-", "任务事件流会记录取消请求和最终状态。"],
      recovery: "如果取消后资源处于中间状态，请回到对应对象列表确认并手动修复。",
    });
    if (!confirmed) return;
    setBusy(`job-cancel-${item.id}`);
    try {
      await actions.api(`/api/docker/jobs/${item.id}/cancel`, { method: "POST", csrf: actions.csrf });
      actions.setToast("已请求取消 Docker job", "good");
      await Promise.all([loadControl(), loadJobsAndEvents()]);
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
        if (record.type === "docker.job.completed" || record.type === "docker.job.failed" || record.type === "docker.job.cancelled") {
          void refresh().catch(() => undefined);
          void loadControl().catch(() => undefined);
          void loadJobsAndEvents().catch(() => undefined);
        }
      } catch {
        // 下一次状态刷新会对齐任务状态。
      }
    };
    ["docker.job.created", "docker.job.started", "docker.job.output", "docker.job.completed", "docker.job.failed", "docker.job.cancel.requested", "docker.job.cancelled"].forEach((name) => source.addEventListener(name, handle));
    source.onerror = () => {
      if (!closed) source.close();
    };
    return () => {
      closed = true;
      ["docker.job.created", "docker.job.started", "docker.job.output", "docker.job.completed", "docker.job.failed", "docker.job.cancel.requested", "docker.job.cancelled"].forEach((name) => source.removeEventListener(name, handle));
      source.close();
    };
  }, [actions, job?.eventScope, job?.eventScopeId, loadControl, loadJobsAndEvents]);

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
	          createCredential={(scopes) => void createCredential(scopes)}
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
	          pullRegistryTag={(item) => void pullRegistryTag(item)}
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
	          setCredentialStatus={(item, status) => void setCredentialStatus(item, status)}
	          useTagForContainer={(item) => useTagForContainer(item)}
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
	      ) : tab === "overview" ? (
	        <DockerOverview
	          job={job}
	          recentEvents={recentDockerEvents}
	          registrySettings={registrySettings}
	          registryStatus={registryStatus}
	          setTab={setTab}
	          status={status}
	        />
	      ) : tab === "events" ? (
	        <DockerEventsPanel
	          busy={busy}
	          events={recentDockerEvents}
	          job={job}
	          jobEvents={jobEvents}
	          jobs={jobs}
	          loading={loading}
	          onCancelJob={(item) => void cancelJob(item)}
	          onSelectJob={setJob}
	        />
	      ) : !available ? (
	        <DockerUnavailablePanel control={control} lastError={status?.lastError} setTab={setTab} />
	      ) : tab === "containers" ? (
	        <div className="grid grid-cols-[minmax(0,1fr)_360px] gap-4 max-xl:grid-cols-1">
	          <Panel title="容器" subtitle="选中容器后在右侧查看状态、stats、端口、挂载、网络、labels 和最近日志。">
	            <DockerTable
	              columns={[
	                { header: "名称", width: "20%" },
	                { header: "状态", width: "16%" },
	                { header: "镜像", width: "30%" },
	                { header: "端口", width: "18%" },
	                { header: "操作", width: "250px" },
	              ]}
	              empty="暂无容器"
	              loading={loading}
	              rows={containers.map((item) => ({
	                key: item.id,
	                cells: [
	                  <DockerValue value={item.names[0] || item.id} />,
	                  <span className="flex flex-col gap-1">
	                    <Pill tone={containerTone(item.state)}>{item.state}</Pill>
	                    <span className="muted truncate text-xs" title={item.status}>{item.status}</span>
	                  </span>,
	                  <DockerValue value={item.image} />,
	                  <DockerValue value={(item.ports || []).join(", ") || "-"} />,
	                  <ContainerActions
	                    busy={busy}
	                    item={item}
	                    onAction={(action) => void containerAction(item, action)}
	                    onDetails={() => void openContainerDetails(item)}
	                    onRemove={() => void removeContainer(item)}
	                  />,
	                ],
	              }))}
	            />
	          </Panel>
	          <ContainerInspector
	            details={containerDetails}
	            detailsLoading={containerDetailsLoading}
	            job={job}
	            jobEvents={jobEvents}
	            logLines={logLines}
	            logsFor={logsFor}
	            logsLoading={logsLoading}
	            onClose={() => {
	              setSelectedContainer(null);
	              setLogsFor(null);
	              setContainerDetails(null);
	              setContainerStats(null);
	              setLogLines([]);
	            }}
	            selected={selectedContainer}
	            stats={containerStats}
	          />
	        </div>
	      ) : tab === "images" ? (
	        <Panel title="镜像">
	          <div className="grid gap-3">
	            <div className="flex flex-wrap items-end gap-2">
	              <div className="min-w-60 flex-1">
	                <Field label="拉取镜像" help="例如 nginx:latest 或 registry.example.com/app:tag。">
	                  <input className="input mono" onChange={(event) => setPullRef(event.target.value)} placeholder="repository:tag" value={pullRef} />
	                </Field>
	              </div>
	              <Button disabled={busy === "pull"} tone="primary" onClick={() => void pullImage()}>
	                {busy === "pull" ? "拉取中" : "拉取"}
	              </Button>
	            </div>
	            <DockerTable
	              columns={[
	                { header: "标签", width: "42%" },
	                { header: "ID", width: "22%" },
	                { header: "大小", width: "110px" },
	                { header: "创建时间", width: "160px" },
	                { header: "操作", width: "90px" },
	              ]}
	              empty="暂无镜像"
	              loading={loading}
	              rows={images.map((item) => ({
	                key: item.id,
	                cells: [
	                  <DockerValue clamp={false} value={item.tags && item.tags.length ? item.tags.join(", ") : item.id} />,
	                  <DockerValue value={item.id} />,
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
	            columns={[
	              { header: "名称", width: "28%" },
	              { header: "驱动", width: "120px" },
	              { header: "挂载点", width: "52%" },
	            ]}
	            empty="暂无卷"
	            loading={loading}
	            rows={volumes.map((item) => ({
	              key: item.name,
	              cells: [
	                <DockerValue value={item.name} />,
	                <span className="text-xs">{item.driver}</span>,
	                <DockerValue clamp={false} value={item.mountpoint || "-"} />,
	              ],
	            }))}
	          />
	        </Panel>
	      ) : (
	        <Panel title="网络">
	          <DockerTable
	            columns={[
	              { header: "名称", width: "30%" },
	              { header: "驱动", width: "120px" },
	              { header: "范围", width: "120px" },
	              { header: "ID", width: "32%" },
	            ]}
	            empty="暂无网络"
	            loading={loading}
	            rows={networks.map((item) => ({
	              key: item.id,
	              cells: [
	                <DockerValue value={item.name} />,
	                <span className="text-xs">{item.driver}</span>,
	                <span className="text-xs">{item.scope}</span>,
	                <DockerValue value={item.id} />,
	              ],
	            }))}
	          />
	        </Panel>
	      )}
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function jobTone(status: string | undefined): "good" | "warn" | "danger" | "neutral" {
  if (status === "completed") return "good";
  if (status === "failed") return "danger";
  if (status === "cancelled") return "warn";
  if (status === "queued" || status === "running") return "warn";
  return "neutral";
}

function formatDate(value: string | undefined): string {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function DockerOverview({
  status,
  registryStatus,
  registrySettings,
  job,
  recentEvents,
  setTab,
}: {
  status: DockerStatus | null;
  registryStatus: DockerRegistryStatus | null;
  registrySettings: DockerRegistrySettings;
  job: DockerJob | null;
  recentEvents: EventRecord[];
  setTab: (tab: DockerTab) => void;
}) {
  const available = Boolean(status?.available);
  const registryReady = Boolean(registryStatus?.enabled && registryStatus.ready);
  const risks = [
    !available ? "Docker daemon 不可用，容器/镜像/网络/卷管理暂不可操作。" : "",
    registryStatus?.enabled && !registryStatus.ready ? "Registry 已启用但未就绪，请检查存储和公开 URL。" : "",
    registrySettings.publicUrl?.startsWith("http://") ? "Registry 使用 HTTP，仅适合 localhost/127.0.0.1 调试。" : "",
    job?.status === "failed" ? `最近 job 失败：${job.title}` : "",
  ].filter(Boolean);
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-4 gap-2 max-xl:grid-cols-2">
        <Metric label="Daemon" tone={available ? "good" : "warn"} value={available ? "可用" : "不可用"} detail={status?.serverVersion || status?.lastError || "未探测"} />
        <Metric label="Registry" tone={registryReady ? "good" : registryStatus?.enabled ? "warn" : "neutral"} value={registryStatus?.enabled ? (registryReady ? "就绪" : "已启用") : "关闭"} detail={registryStatus?.publicUrl || "未配置"} />
        <Metric label="容器" value={`${status?.containersRunning || 0} / ${status?.containers || 0}`} detail="运行 / 总数" />
        <Metric label="Registry 用量" value={formatBytesZero(registryStatus?.usageBytes || 0)} detail={`${formatBytesZero(registryStatus?.quotaBytes || 0)} quota`} />
      </div>
      <div className="grid grid-cols-[minmax(0,1fr)_360px] gap-4 max-xl:grid-cols-1">
        <Panel title="主机摘要" subtitle="今天 Docker 是否正常，优先看 daemon、registry、最近任务和风险。">
          <ContextList
            items={[
              ["状态", available ? <Pill tone="good">可用</Pill> : <Pill tone="warn">不可用</Pill>],
              ["Server 版本", status?.serverVersion || "-"],
              ["API 版本", status?.apiVersion || "-"],
              ["操作系统", status?.os || "-"],
              ["架构", status?.architecture || "-"],
              ["存储驱动", status?.storageDriver || "-"],
              ["Rootless", status?.rootless ? "是" : "否"],
              ["镜像", String(status?.images || 0)],
            ]}
          />
        </Panel>
        <Panel title="风险与下一步" actions={<Button onClick={() => setTab(!available ? "settings" : "events")}>{!available ? "打开主机操作" : "查看 Jobs"}</Button>}>
          <div className="grid gap-3">
            {risks.length ? (
              risks.map((item) => (
                <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3 text-xs" key={item}>
                  {item}
                </div>
              ))
            ) : (
              <div className="rounded-lg border border-[rgba(18,132,79,0.18)] bg-[var(--good-soft)] p-3 text-xs text-[var(--good)]">
                当前没有需要立即处理的 Docker 风险。
              </div>
            )}
            <div className="grid grid-cols-2 gap-2">
              <Button onClick={() => setTab("registry")}>Registry</Button>
              <Button onClick={() => setTab("containers")}>容器</Button>
            </div>
          </div>
        </Panel>
      </div>
      <Panel title="最近事件" subtitle="跨 Docker job 的近期事件，用于快速确认任务是否推进。">
        <EventList events={recentEvents.slice(0, 8)} />
      </Panel>
    </div>
  );
}

function DockerUnavailablePanel({ control, lastError, setTab }: { control: DockerControlStatus | null; lastError?: string; setTab: (tab: DockerTab) => void }) {
  return (
    <Panel title="Docker daemon 不可用" subtitle="Registry 可独立工作；Host 能力需要本机 Docker daemon 已安装、运行并允许 socket 访问。">
      <div className="grid gap-4">
        <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3 text-sm">
          <strong className="block">当前原因</strong>
          <p className="muted mt-1 mb-0 text-xs">{lastError || "未检测到本机 Docker daemon。请确认 Docker 已安装并运行，且服务对 docker socket 有访问权限。"}</p>
        </div>
        <ContextList
          items={[
            ["安装探测", control?.install?.installed ? <Pill tone="good">installed</Pill> : <Pill tone="warn">not installed</Pill>],
            ["systemd", control?.systemd?.available ? "available" : "unavailable"],
            ["docker.service", control?.systemd?.activeState || "-"],
            ["权限", control?.privilegeMethod || "-"],
          ]}
        />
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setTab("settings")} tone="primary">打开主机操作</Button>
          <Button onClick={() => setTab("registry")}>打开 Registry</Button>
        </div>
      </div>
    </Panel>
  );
}

function DockerEventsPanel({
  busy,
  events,
  job,
  jobEvents,
  jobs,
  loading,
  onCancelJob,
  onSelectJob,
}: {
  busy: string;
  events: EventRecord[];
  job: DockerJob | null;
  jobEvents: EventRecord[];
  jobs: DockerJob[];
  loading: boolean;
  onCancelJob: (job: DockerJob) => void;
  onSelectJob: (job: DockerJob) => void;
}) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_360px] gap-4 max-xl:grid-cols-1">
      <Panel title="Jobs" subtitle="当前服务进程内的 Docker 长任务历史；事件流可用于回看失败原因。">
        <DockerTable
          columns={[
            { header: "任务", width: "30%" },
            { header: "状态", width: "100px" },
            { header: "目标", width: "26%" },
            { header: "时间", width: "180px" },
            { header: "操作", width: "160px" },
          ]}
          empty="暂无 Docker job"
          loading={loading}
          rows={jobs.map((item) => ({
            key: item.id,
            cells: [
              <DockerValue value={item.title} />,
              <Pill tone={jobTone(item.status)}>{item.status}</Pill>,
              <DockerValue value={item.target || "-"} />,
              <span className="text-xs">{formatDate(item.completedAt || item.startedAt || item.createdAt)}</span>,
              <span className="flex flex-wrap gap-1">
                <Button onClick={() => onSelectJob(item)}>事件</Button>
                {item.status === "queued" || item.status === "running" ? (
                  <Button disabled={busy === `job-cancel-${item.id}`} onClick={() => onCancelJob(item)} tone="danger">取消</Button>
                ) : null}
              </span>,
            ],
          }))}
        />
      </Panel>
      <div className="grid content-start gap-4">
        <DockerJobPanel job={job} jobEvents={jobEvents} onCancelJob={onCancelJob} busy={busy} />
        <Panel title="近期事件" subtitle="按创建时间倒序显示所有 Docker job 事件。">
          <EventList events={events} />
        </Panel>
      </div>
    </div>
  );
}

function DockerJobPanel({ job, jobEvents, onCancelJob, busy }: { job: DockerJob | null; jobEvents: EventRecord[]; onCancelJob?: (job: DockerJob) => void; busy?: string }) {
  if (!job) {
    return (
      <Panel title="Job 事件">
        <EmptyState title="未选择 job" body="提交操作或在 Jobs 表中选择任务后，这里会显示事件流。" />
      </Panel>
    );
  }
  const canCancel = job.status === "queued" || job.status === "running";
  return (
    <Panel
      title={`Docker Job · ${job.title}`}
      subtitle={`${job.status}${job.target ? ` · ${job.target}` : ""}`}
      actions={
        <span className="flex gap-2">
          <Pill tone={jobTone(job.status)}>{job.status}</Pill>
          {canCancel && onCancelJob ? (
            <Button disabled={busy === `job-cancel-${job.id}`} onClick={() => onCancelJob(job)} tone="danger">取消</Button>
          ) : null}
        </span>
      }
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
  );
}

function EventList({ events }: { events: EventRecord[] }) {
  if (!events.length) {
    return <EmptyState title="暂无事件" body="Docker job 运行后会在这里显示近期事件。" />;
  }
  return (
    <div className="grid max-h-96 gap-2 overflow-auto">
      {events.map((event) => (
        <div className="grid gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={event.id || `${event.scopeId}-${event.sequence}-${event.type}`}>
          <div className="flex min-w-0 items-center justify-between gap-2">
            <DockerValue className={streamTone(event)} value={eventMessage(event)} />
            <span className="muted shrink-0">{formatDate(event.createdAt)}</span>
          </div>
          <span className="muted mono truncate">{event.scopeId || "-"}</span>
        </div>
      ))}
    </div>
  );
}

function ContainerInspector({
  selected,
  details,
  detailsLoading,
  stats,
  logsFor,
  logLines,
  logsLoading,
  job,
  jobEvents,
  onClose,
}: {
  selected: DockerContainerSummary | null;
  details: DockerContainerInspectSummary | null;
  detailsLoading: boolean;
  stats: DockerStats | null;
  logsFor: DockerContainerSummary | null;
  logLines: DockerLogLine[];
  logsLoading: boolean;
  job: DockerJob | null;
  jobEvents: EventRecord[];
  onClose: () => void;
}) {
  return (
    <div className="grid content-start gap-4">
      <Panel
        title="容器 Inspector"
        subtitle={selected ? "状态、资源、端口、挂载、网络和最近日志。" : "从左侧容器列表选择一个对象。"}
        actions={selected ? <Button onClick={onClose}>关闭</Button> : null}
      >
        {!selected ? (
          <EmptyState title="未选择容器" body="点击容器行的详情后，会在这里显示诊断上下文。" />
        ) : detailsLoading ? (
          <p className="muted text-sm">正在加载容器详情。</p>
        ) : (
          <div className="grid gap-4">
            <ContextList
              items={[
                ["名称", details?.name || selected.names[0] || selected.id],
                ["ID", <DockerValue value={details?.id || selected.id} />],
                ["状态", <Pill tone={containerTone(details?.state || selected.state)}>{details?.state || selected.state}</Pill>],
                ["Exit code", String(details?.exitCode ?? "-")],
                ["Restart count", String(details?.restartCount ?? "-")],
                ["Image", <DockerValue value={selected.image} />],
              ]}
            />
            <div className="grid grid-cols-2 gap-2">
              <Metric label="CPU" value={`${stats?.cpuPercent ?? 0}%`} />
              <Metric label="内存" value={formatBytesZero(stats?.memoryUsageBytes || 0)} detail={`${stats?.memoryPercent ?? 0}% / ${formatBytesZero(stats?.memoryLimitBytes || 0)}`} />
            </div>
            <InspectorList title="端口" items={(details?.ports || []).map((item) => `${item.privatePort}${item.public ? ` -> ${item.public}` : ""}`)} />
            <InspectorList title="挂载" items={(details?.mounts || []).map((item) => `${item.type} ${item.source || item.name || "-"} -> ${item.destination}${item.rw ? "" : " (ro)"}`)} />
            <InspectorList title="网络" items={(details?.networks || []).map((item) => `${item.name}${item.ipAddress ? ` · ${item.ipAddress}` : ""}`)} />
            <InspectorList title="Labels" items={(details?.labels || []).map((item) => `${item.key}=${item.value}`)} />
          </div>
        )}
      </Panel>
      {logsFor ? (
        <Panel title={`日志 · ${logsFor.names[0] || logsFor.id}`} subtitle="最近 200 行，已脱敏并限制长度。">
          {logsLoading ? (
            <p className="muted text-sm">正在加载日志。</p>
          ) : logLines.length ? (
            <pre className="mono max-h-80 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed">
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
      <DockerJobPanel job={job} jobEvents={jobEvents} />
    </div>
  );
}

function InspectorList({ title, items }: { title: string; items: string[] }) {
  return (
    <div className="grid gap-2 border-t border-[var(--line)] pt-3">
      <strong className="text-xs text-[var(--muted-strong)]">{title}</strong>
      {items.length ? (
        <div className="grid gap-1">
          {items.map((item, index) => (
            <DockerValue clamp={false} key={`${item}-${index}`} value={item} />
          ))}
        </div>
      ) : (
        <span className="muted text-xs">无</span>
      )}
    </div>
  );
}

function ContainerActions({
  busy,
  item,
  onAction,
  onDetails,
  onRemove,
}: {
  busy: string;
  item: DockerContainerSummary;
  onAction: (action: "start" | "stop" | "restart" | "kill") => void;
  onDetails: () => void;
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
      <Button onClick={onDetails}>详情</Button>
      {running ? (
        <Button disabled={busy === `restart-${item.id}`} onClick={() => onAction("restart")}>
          重启
        </Button>
      ) : null}
      {running ? (
        <Button disabled={busy === `kill-${item.id}`} tone="danger" onClick={() => onAction("kill")}>
          Kill
        </Button>
      ) : null}
      <Button disabled={busy === `remove-${item.id}`} tone="danger" onClick={onRemove}>
        删除
      </Button>
    </span>
  );
}
