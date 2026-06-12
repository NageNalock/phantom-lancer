import { useEffect, useState } from "react";
import type { DockerControlStatus } from "../../app/types";
import { Button, ContextList, Field, Panel, Pill } from "../../components/ui";

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
  daemonAction,
  installDocker,
  loadControl,
  registryPublicUrl,
  saveDockerSettings,
  applyDaemonPullConcurrency,
}: {
  busy: string;
  control: DockerControlStatus | null;
  daemonAction: (action: "start" | "stop" | "restart") => void;
  installDocker: () => void;
  loadControl: () => void;
  registryPublicUrl?: string;
  saveDockerSettings: (next: { installEnabled?: boolean; daemonControlEnabled?: boolean; containerCreateEnabled?: boolean; pullConcurrency?: number; daemonPullConcurrency?: number }) => void;
  applyDaemonPullConcurrency?: (value: number) => void;
}) {
  const install = control?.install;
  const systemd = control?.systemd;
  const settings = control?.settings || {};
  const host = registryHost(registryPublicUrl);
  const [pullConcurrencyInput, setPullConcurrencyInput] = useState<string>(String(settings.pullConcurrency ?? 1));
  const [daemonPullInput, setDaemonPullInput] = useState<string>(String(settings.daemonPullConcurrency ?? 3));

  useEffect(() => {
    setPullConcurrencyInput(String(settings.pullConcurrency ?? 1));
    setDaemonPullInput(String(settings.daemonPullConcurrency ?? 3));
  }, [settings.pullConcurrency, settings.daemonPullConcurrency]);

  const pullConcurrencyNum = Number(pullConcurrencyInput);
  const daemonPullNum = Number(daemonPullInput);
  const pullConcurrencyValid = !isNaN(pullConcurrencyNum) && pullConcurrencyNum >= 1 && pullConcurrencyNum <= 10;
  const daemonPullValid = !isNaN(daemonPullNum) && daemonPullNum >= 1 && daemonPullNum <= 10;

  return (
    <div className="grid gap-4">
      <Panel
        title="主机操作"
        subtitle="高级主机管理：Docker 安装、daemon 启停和创建开关。常规容器创建请使用『容器』页。"
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

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="mb-3 flex items-start justify-between gap-3">
              <div>
                <h3 className="m-0 text-sm font-medium">容器创建总开关</h3>
                <p className="muted mt-1 mb-0 text-xs">全局安全开关。关闭后『容器』页无法创建新容器；Registry 和 Images 页的『创建容器』动作也会被阻止。</p>
              </div>
              <Pill tone="neutral">{settings.containerCreateEnabled ? "已开启" : "已关闭"}</Pill>
            </div>
             <ContextList
               items={[
                 ["允许的镜像来源", registryPublicUrl ? <span className="mono text-xs">{host}/*</span> : "未启用 Registry 来源限制"],
                 ["不允许的参数", "host path、privileged、host network、自由任意参数"],
                 ["创建位置", "『容器』页 或 从 Registry / Images 唤起创建 drawer"],
               ]}
             />
            <div className="mt-3">
              <Button disabled={busy === "docker-settings"} onClick={() => saveDockerSettings({ containerCreateEnabled: !settings.containerCreateEnabled })}>
                {settings.containerCreateEnabled ? "关闭容器创建总开关" : "开启容器创建总开关"}
              </Button>
            </div>
          </div>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="mb-3 flex items-start justify-between gap-3">
              <div>
                <h3 className="m-0 text-sm font-medium">镜像拉取并发限制</h3>
                <p className="muted mt-1 mb-0 text-xs">限制同时进行的镜像拉取数量，避免 containerd 解压 layer 时占满 CPU。</p>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
              <Field label="应用层并发数" help="控制台内部排队，立即生效，范围 1–10。">
                <input
                  className="input mono"
                  onChange={(event) => setPullConcurrencyInput(event.target.value)}
                  type="number"
                  value={pullConcurrencyInput}
                />
              </Field>
              <Field label="Daemon 层并发数" help={`修改 daemon.json 的 max-concurrent-downloads，需重启 Docker。当前实际值：${control?.daemonPullConcurrency ?? "-"}`}>
                <input
                  className="input mono"
                  onChange={(event) => setDaemonPullInput(event.target.value)}
                  type="number"
                  value={daemonPullInput}
                />
              </Field>
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              <Button disabled={busy === "docker-settings" || !pullConcurrencyValid} onClick={() => saveDockerSettings({ pullConcurrency: pullConcurrencyNum })}>
                保存应用层设置
              </Button>
              <Button disabled={!systemd?.canControl || !daemonPullValid || busy === "daemon-pull-concurrency" || !applyDaemonPullConcurrency} tone="primary" onClick={() => applyDaemonPullConcurrency?.(daemonPullNum)}>
                应用到 daemon 并重启
              </Button>
            </div>
          </div>
        </div>
      </Panel>
    </div>
  );
}
