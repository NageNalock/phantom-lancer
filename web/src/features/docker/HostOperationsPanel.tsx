import { useEffect, useMemo, useRef } from "react";
import type { DockerControlStatus } from "../../app/types";
import { Button, CheckLabel, ContextList, Field, Panel, Pill } from "../../components/ui";

type CreatePort = { containerPort: number; hostPort?: number; protocol?: string; hostIp?: string };
type CreateVolume = { volumeName: string; destination: string; readOnly?: boolean };
type CreateEnv = { name: string; value: string };

export type CreateContainerTemplate = {
  name: string;
  image: string;
  restartPolicy?: string;
  ports?: CreatePort[];
  volumes?: CreateVolume[];
  env?: CreateEnv[];
};

function registryHost(publicUrl: string | undefined): string {
  const raw = (publicUrl || "").trim();
  if (!raw) {
    return "registry.example.com";
  }
  return raw.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").replace(/\/+$/, "") || "registry.example.com";
}

function parsePorts(raw: string): CreatePort[] {
  const lines = raw.split(/[\n,;]/).map((l) => l.trim()).filter(Boolean);
  const result: CreatePort[] = [];
  for (const line of lines) {
    const m = line.match(/^(?:(\d+)(?:[.:](\d+))?)(?:\/(tcp|udp|sctp))?$/);
    if (!m) continue;
    const [, container, host, proto] = m;
    const containerPort = Number(container);
    if (!containerPort || containerPort > 65535) continue;
    const hostPort = host ? Number(host) : containerPort;
    if (hostPort > 65535) continue;
    result.push({ containerPort, hostPort, protocol: proto || "tcp", hostIp: "127.0.0.1" });
  }
  return result;
}

function parseVolumes(raw: string): CreateVolume[] {
  const lines = raw.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
  const result: CreateVolume[] = [];
  for (const line of lines) {
    const [name, dest, flag] = line.split(":");
    if (!name || !dest) continue;
    if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(name)) continue;
    if (!dest.startsWith("/")) continue;
    result.push({ volumeName: name, destination: dest, readOnly: flag === "ro" });
  }
  return result;
}

function parseEnv(raw: string): CreateEnv[] {
  const lines = raw.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
  const result: CreateEnv[] = [];
  for (const line of lines) {
    const idx = line.indexOf("=");
    if (idx < 0) continue;
    const name = line.slice(0, idx).trim();
    const value = line.slice(idx + 1).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) continue;
    if (value.length > 4096) continue;
    result.push({ name, value });
  }
  return result;
}

export function HostOperationsPanel({
  busy,
  control,
  createContainer,
  createEnv,
  createFocus,
  createImage,
  createName,
  createPorts,
  createRestart,
  createVolumes,
  daemonAction,
  installDocker,
  loadControl,
  registryPublicUrl,
  saveDockerSettings,
  setCreateEnv,
  setCreateImage,
  setCreateName,
  setCreatePorts,
  setCreateRestart,
  setCreateVolumes,
  toggleCreateFocus,
}: {
  busy: string;
  control: DockerControlStatus | null;
  createContainer: (template: CreateContainerTemplate) => void;
  createEnv: string;
  createFocus: boolean;
  createImage: string;
  createName: string;
  createPorts: string;
  createRestart: string;
  createVolumes: string;
  daemonAction: (action: "start" | "stop" | "restart") => void;
  installDocker: () => void;
  loadControl: () => void;
  registryPublicUrl?: string;
  saveDockerSettings: (next: { installEnabled?: boolean; daemonControlEnabled?: boolean; containerCreateEnabled?: boolean }) => void;
  setCreateEnv: (value: string) => void;
  setCreateImage: (value: string) => void;
  setCreateName: (value: string) => void;
  setCreatePorts: (value: string) => void;
  setCreateRestart: (value: string) => void;
  setCreateVolumes: (value: string) => void;
  toggleCreateFocus: () => void;
}) {
  const install = control?.install;
  const systemd = control?.systemd;
  const settings = control?.settings || {};
  const host = registryHost(registryPublicUrl);
  const createRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (createFocus && createRef.current) {
      createRef.current.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  }, [createFocus]);

  const template = useMemo<CreateContainerTemplate>(() => {
    return {
      name: createName,
      image: createImage,
      restartPolicy: createRestart,
      ports: parsePorts(createPorts),
      volumes: parseVolumes(createVolumes),
      env: parseEnv(createEnv),
    };
  }, [createName, createImage, createRestart, createPorts, createVolumes, createEnv]);

  const portError = useMemo(() => {
    const lines = createPorts.split(/[\n,;]/).map((l) => l.trim()).filter(Boolean);
    for (const line of lines) {
      const m = line.match(/^(?:(\d+)(?:[.:](\d+))?)(?:\/(tcp|udp|sctp))?$/);
      if (!m) return `端口格式错误: "${line}"。示例: 8080:80 或 5432/tcp`;
    }
    return "";
  }, [createPorts]);

  const volumeError = useMemo(() => {
    const lines = createVolumes.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
    for (const line of lines) {
      const [name, dest] = line.split(":");
      if (!name || !dest) return `卷格式错误: "${line}"。示例: app-data:/data 或 app-logs:/var/log:ro`;
      if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(name)) return `卷名格式错误: "${name}"`;
      if (!dest.startsWith("/")) return `挂载点必须是绝对路径: "${dest}"`;
    }
    return "";
  }, [createVolumes]);

  const envError = useMemo(() => {
    const lines = createEnv.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
    for (const line of lines) {
      const idx = line.indexOf("=");
      if (idx < 0) return `环境变量格式错误: "${line}"。示例: NODE_ENV=production`;
      const name = line.slice(0, idx).trim();
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return `环境变量名非法: "${name}"`;
      const lower = name.toLowerCase();
      if (lower.includes("secret") || lower.includes("token") || lower.includes("password") || lower.includes("key")) {
        return `不允许敏感环境变量: "${name}"。secrets 请使用其他方式注入。`;
      }
    }
    return "";
  }, [createEnv]);

  return (
    <div className="grid gap-4">
      <Panel
        title="主机操作"
        subtitle="安装 Docker daemon 与控制 systemd docker 服务。默认关闭；开启后仍需每次危险操作确认。"
        actions={<Button onClick={() => loadControl()}>刷新控制状态</Button>}
      >
        <div className="grid gap-4">
          <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
            <div className="card-soft">
              <div className="mb-3 flex items-start justify-between gap-3">
                <div>
                  <h3 className="m-0 text-sm font-medium">一键安装 Docker daemon</h3>
                  <p className="muted mt-1 mb-0 text-xs">使用 Docker 官方公开源；不提供一键卸载。</p>
                </div>
                <Pill tone="neutral">{settings.installEnabled ? "已开启" : "已关闭"}</Pill>
              </div>
              <ContextList
                items={[
                  ["发行版", install?.distroName || install?.distroId || "-"],
                  ["安装状态", install?.installed ? <Pill tone="good">installed</Pill> : <Pill tone="warn">not installed</Pill>],
                  ["权限", install?.privilegeMethod || control?.privilegeMethod || "-"],
                  ["原因", install?.reason || "-"],
                ]}
              />
              {install?.commandPreview?.length ? (
                <pre className="mono mt-3 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-2 text-xs">{install.commandPreview.join("\n")}</pre>
              ) : null}
              <div className="mt-3 flex flex-wrap gap-2">
                <Button disabled={busy === "docker-settings"} onClick={() => saveDockerSettings({ installEnabled: !settings.installEnabled })}>
                  {settings.installEnabled ? "关闭安装开关" : "开启安装开关"}
                </Button>
                <Button disabled={!install?.canInstall || busy === "install"} tone="danger" onClick={() => installDocker()}>
                  {busy === "install" ? "提交中" : "安装 Docker"}
                </Button>
              </div>
            </div>

            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <div className="mb-3 flex items-start justify-between gap-3">
                <div>
                  <h3 className="m-0 text-sm font-medium">Docker daemon 控制</h3>
                  <p className="muted mt-1 mb-0 text-xs">通过 systemctl 控制 docker 服务，会影响本机所有容器。</p>
                </div>
                <Pill tone="neutral">{settings.daemonControlEnabled ? "已开启" : "已关闭"}</Pill>
              </div>
              <ContextList
                items={[
                  ["systemd", systemd?.available ? "available" : "unavailable"],
                  ["docker.service", systemd?.activeState || "-"],
                  ["权限", systemd?.privilegeMethod || control?.privilegeMethod || "-"],
                  ["原因", systemd?.reason || "-"],
                ]}
              />
              <div className="mt-3 flex flex-wrap gap-2">
                <Button disabled={busy === "docker-settings"} onClick={() => saveDockerSettings({ daemonControlEnabled: !settings.daemonControlEnabled })}>
                  {settings.daemonControlEnabled ? "关闭控制开关" : "开启控制开关"}
                </Button>
                <Button disabled={!systemd?.canControl || busy === "daemon-start"} onClick={() => daemonAction("start")}>启动</Button>
                <Button disabled={!systemd?.canControl || busy === "daemon-restart"} onClick={() => daemonAction("restart")}>重启</Button>
                <Button disabled={!systemd?.canControl || busy === "daemon-stop"} tone="danger" onClick={() => daemonAction("stop")}>停止</Button>
              </div>
            </div>
          </div>
        </div>
      </Panel>

      <div ref={createRef} className={createFocus ? "outline-offset-4 rounded-xl ring-2 ring-[var(--primary)] ring-offset-2" : ""}>
        <Panel
          title="模板化容器创建"
          subtitle={createFocus ? `从镜像仓库带入的镜像: ${createImage || "-"}。填写参数后创建并启动容器。` : "受控模板创建容器，支持端口、命名卷、非敏感环境变量和 restart policy。"}
          actions={
            <span className="flex gap-2">
              <Pill tone="neutral">{settings.containerCreateEnabled ? "已开启" : "已关闭"}</Pill>
              {createFocus ? <Button onClick={toggleCreateFocus}>取消聚焦</Button> : null}
            </span>
          }
        >
          <div className="grid gap-4">
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <div className="grid gap-3">
                <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                  <Field label="容器名称" help="字母、数字、下划线、点、短横，最长 64 字符。">
                    <input className="input mono" onChange={(event) => setCreateName(event.target.value)} value={createName} />
                  </Field>
                  <Field label="镜像引用" help={`必须位于 ${host}/personal/ 前缀下。`}>
                    <input className="input mono" onChange={(event) => setCreateImage(event.target.value)} placeholder={`${host}/personal/app:tag`} value={createImage} />
                  </Field>
                </div>

                <Field
                  label="Restart Policy"
                  help="unless-stopped 适合长期服务；on-failure 最多重试 5 次。"
                >
                  <select className="select mono" onChange={(event) => setCreateRestart(event.target.value)} value={createRestart}>
                    <option value="no">no — 不自动重启</option>
                    <option value="always">always — 总是重启</option>
                    <option value="unless-stopped">unless-stopped — 除非手动停止</option>
                    <option value="on-failure">on-failure — 失败时重启 (×5)</option>
                  </select>
                </Field>

                <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                  <Field
                    label="端口映射"
                    help="每行一条。格式: hostPort:containerPort[/proto] 或 containerPort[/proto]。示例: 8080:80 或 5432/tcp。默认绑定 127.0.0.1。"
                  >
                    <textarea
                      className="input mono min-h-[72px]"
                      onChange={(event) => setCreatePorts(event.target.value)}
                      placeholder={`8080:80
5432/tcp`}
                      value={createPorts}
                    />
                    {portError ? <p className="mt-1 mb-0 text-xs text-[var(--danger)]">{portError}</p> : null}
                    {template.ports?.length ? <p className="muted mt-1 mb-0 text-xs">解析为 {template.ports.length} 条端口规则。</p> : null}
                  </Field>

                  <Field
                    label="命名卷"
                    help="每行一条。格式: volumeName:/mount/point[:ro]。只支持命名卷，不支持 host path。"
                  >
                    <textarea
                      className="input mono min-h-[72px]"
                      onChange={(event) => setCreateVolumes(event.target.value)}
                      placeholder={`app-data:/app/data
app-logs:/var/log:ro`}
                      value={createVolumes}
                    />
                    {volumeError ? <p className="mt-1 mb-0 text-xs text-[var(--danger)]">{volumeError}</p> : null}
                    {template.volumes?.length ? <p className="muted mt-1 mb-0 text-xs">解析为 {template.volumes.length} 个命名卷。</p> : null}
                  </Field>
                </div>

                <Field
                  label="环境变量"
                  help="每行一条 KEY=VALUE。不允许包含 secret/token/password/key 的变量名；单条 VALUE 最长 4KB，最多 64 条。"
                >
                  <textarea
                    className="input mono min-h-[72px]"
                    onChange={(event) => setCreateEnv(event.target.value)}
                    placeholder={`NODE_ENV=production
TZ=Asia/Shanghai
LOG_LEVEL=info`}
                    value={createEnv}
                  />
                  {envError ? <p className="mt-1 mb-0 text-xs text-[var(--danger)]">{envError}</p> : null}
                  {template.env?.length ? <p className="muted mt-1 mb-0 text-xs">解析为 {template.env.length} 条环境变量。</p> : null}
                </Field>
              </div>
            </div>

            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3">
              <CheckLabel
                checked={Boolean(settings.containerCreateEnabled)}
                name="docker_container_create_enabled"
                onChange={(checked) => saveDockerSettings({ containerCreateEnabled: checked })}
              >
                <span className="grid gap-0.5">
                  <span>启用容器创建</span>
                  <span className="muted text-xs">关闭后即使表单已填也不会提交创建请求。</span>
                </span>
              </CheckLabel>
              <div className="flex flex-wrap gap-2 justify-end">
                <Button
                  disabled={
                    !settings.containerCreateEnabled ||
                    busy === "container-create" ||
                    Boolean(portError || volumeError || envError) ||
                    !createName.trim() ||
                    !createImage.trim()
                  }
                  tone="primary"
                  onClick={() => createContainer(template)}
                >
                  {busy === "container-create" ? "提交中" : "创建并启动容器"}
                </Button>
              </div>
            </div>
          </div>
        </Panel>
      </div>
    </div>
  );
}
