import type { DockerControlStatus } from "../../app/types";
import { Button, ContextList, Field, Panel, Pill } from "../../components/ui";

// registryHost derives a valid image-reference host from the configured
// Registry public URL by stripping the scheme and trailing slash.
function registryHost(publicUrl: string | undefined): string {
  const raw = (publicUrl || "").trim();
  if (!raw) {
    return "registry.example.com";
  }
  return raw.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").replace(/\/+$/, "") || "registry.example.com";
}

export function HostOperationsPanel({
  busy,
  control,
  createContainer,
  createImage,
  createName,
  daemonAction,
  installDocker,
  loadControl,
  registryPublicUrl,
  saveDockerSettings,
  setCreateImage,
  setCreateName,
}: {
  busy: string;
  control: DockerControlStatus | null;
  createContainer: () => void;
  createImage: string;
  createName: string;
  daemonAction: (action: "start" | "stop" | "restart") => void;
  installDocker: () => void;
  loadControl: () => void;
  registryPublicUrl?: string;
  saveDockerSettings: (next: { installEnabled?: boolean; daemonControlEnabled?: boolean; containerCreateEnabled?: boolean }) => void;
  setCreateImage: (value: string) => void;
  setCreateName: (value: string) => void;
}) {
  const install = control?.install;
  const systemd = control?.systemd;
  const settings = control?.settings || {};
  const host = registryHost(registryPublicUrl);

  return (
    <Panel
      title="Host Operations"
      subtitle="安装 Docker daemon 与控制 systemd docker 服务。默认关闭；开启后仍需每次危险操作确认。"
      actions={<Button onClick={() => loadControl()}>刷新控制状态</Button>}
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="mb-3 flex items-start justify-between gap-3">
              <div>
                <h3 className="m-0 text-sm font-medium">一键安装 Docker daemon</h3>
                <p className="muted mt-1 mb-0 text-xs">使用 Docker 官方公开源；不提供一键卸载。</p>
              </div>
              <Pill tone={settings.installEnabled ? "warn" : "neutral"}>{settings.installEnabled ? "已开启" : "已关闭"}</Pill>
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
              <Pill tone={settings.daemonControlEnabled ? "warn" : "neutral"}>{settings.daemonControlEnabled ? "已开启" : "已关闭"}</Pill>
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

        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
          <div className="mb-3 flex items-start justify-between gap-3">
            <div>
              <h3 className="m-0 text-sm font-medium">模板化容器创建</h3>
              <p className="muted mt-1 mb-0 text-xs">高权限扩展：仅允许从本机 Registry 的 personal/ 前缀拉起容器，不支持 host path、privileged、host network 或自由参数。</p>
            </div>
            <Pill tone={settings.containerCreateEnabled ? "warn" : "neutral"}>{settings.containerCreateEnabled ? "已开启" : "已关闭"}</Pill>
          </div>
          <div className="grid gap-3">
            <Field label="Container name">
              <input className="input mono" onChange={(event) => setCreateName(event.target.value)} value={createName} />
            </Field>
            <Field label="Image reference" help={`必须位于 ${host}/personal/ 前缀下。`}>
              <input className="input mono" onChange={(event) => setCreateImage(event.target.value)} placeholder={`${host}/personal/app:tag`} value={createImage} />
            </Field>
            <div className="flex flex-wrap gap-2">
              <Button disabled={busy === "docker-settings"} onClick={() => saveDockerSettings({ containerCreateEnabled: !settings.containerCreateEnabled })}>
                {settings.containerCreateEnabled ? "关闭创建开关" : "开启创建开关"}
              </Button>
              <Button disabled={!settings.containerCreateEnabled || busy === "container-create"} tone="danger" onClick={() => createContainer()}>
                创建并启动容器
              </Button>
            </div>
          </div>
        </div>
      </div>
    </Panel>
  );
}
