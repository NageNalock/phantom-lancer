import { useEffect, useMemo, useRef } from "react";
import { Button, Field, Panel, Pill } from "../../components/ui";

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

type ParsePortsResult = {
  ports: CreatePort[];
  errors: { line: string; reason: string }[];
};

function splitPortProtocol(line: string): { body: string; protocol: string } | null {
  const m = line.match(/^(.*?)(?:\/([A-Za-z]+))?$/);
  if (!m) return null;
  const protocol = (m[2] || "tcp").toLowerCase();
  if (protocol !== "tcp" && protocol !== "udp" && protocol !== "sctp") return null;
  return { body: m[1].trim(), protocol };
}

function splitBindIp(body: string): { bindIp?: string; mapping: string } {
  if (body.startsWith("0.0.0.0:")) {
    return { bindIp: "0.0.0.0", mapping: body.slice("0.0.0.0:".length) };
  }
  if (body.startsWith("127.0.0.1:")) {
    return { bindIp: "127.0.0.1", mapping: body.slice("127.0.0.1:".length) };
  }
  if (body.startsWith("[::]:")) {
    return { bindIp: "::", mapping: body.slice("[::]:".length) };
  }
  return { mapping: body };
}

function parsePortNumber(raw: string): number | null {
  if (!/^\d+$/.test(raw)) return null;
  const value = Number(raw);
  return value > 0 && value <= 65535 ? value : null;
}

function parsePortsStrict(raw: string): ParsePortsResult {
  const lines = raw.split(/[\n,;]/).map((l) => l.trim()).filter(Boolean);
  const ports: CreatePort[] = [];
  const errors: { line: string; reason: string }[] = [];
  for (const line of lines) {
    const parsed = splitPortProtocol(line);
    if (!parsed) {
      errors.push({ line, reason: "格式不匹配" });
      continue;
    }
    const { bindIp, mapping } = splitBindIp(parsed.body);
    const separator = mapping.includes(":") ? ":" : ".";
    const parts = mapping.split(separator);
    if (parts.length < 1 || parts.length > 2 || parts.some((part) => !part)) {
      errors.push({ line, reason: "格式不匹配" });
      continue;
    }
    const hostPort = parsePortNumber(parts[0]);
    if (!hostPort) {
      errors.push({ line, reason: `主机端口 ${parts[0]} 超出范围 (1-65535)` });
      continue;
    }
    const containerPort = parts[1] ? parsePortNumber(parts[1]) : hostPort;
    if (!containerPort) {
      errors.push({ line, reason: `容器端口 ${parts[1]} 超出范围 (1-65535)` });
      continue;
    }
    ports.push({ containerPort, hostPort, protocol: parsed.protocol, ...(bindIp ? { hostIp: bindIp } : {}) });
  }
  return { ports, errors };
}

type ParseVolumesResult = {
  volumes: CreateVolume[];
  errors: { line: string; reason: string }[];
};

function parseVolumesStrict(raw: string): ParseVolumesResult {
  const lines = raw.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
  const volumes: CreateVolume[] = [];
  const errors: { line: string; reason: string }[] = [];
  for (const line of lines) {
    const [name, dest, flag] = line.split(":");
    if (!name || !dest) {
      errors.push({ line, reason: "格式不匹配，需要 volumeName:destination" });
      continue;
    }
    if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(name)) {
      errors.push({ line, reason: `卷名 "${name}" 非法` });
      continue;
    }
    if (!dest.startsWith("/")) {
      errors.push({ line, reason: `挂载点 "${dest}" 必须是绝对路径` });
      continue;
    }
    volumes.push({ volumeName: name, destination: dest, readOnly: flag === "ro" });
  }
  return { volumes, errors };
}

type ParseEnvResult = {
  env: CreateEnv[];
  errors: { line: string; reason: string }[];
};

function parseEnvStrict(raw: string): ParseEnvResult {
  const lines = raw.split(/[\n;]/).map((l) => l.trim()).filter(Boolean);
  const env: CreateEnv[] = [];
  const errors: { line: string; reason: string }[] = [];
  for (const line of lines) {
    const idx = line.indexOf("=");
    if (idx < 0) {
      errors.push({ line, reason: "缺少 =，格式需要 KEY=VALUE" });
      continue;
    }
    const name = line.slice(0, idx).trim();
    const value = line.slice(idx + 1).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) {
      errors.push({ line, reason: `变量名 "${name}" 非法` });
      continue;
    }
    if (value.length > 4096) {
      errors.push({ line, reason: "VALUE 超过 4KB 上限" });
      continue;
    }
    env.push({ name, value });
  }
  return { env, errors };
}

export function CreateContainerDrawer({
  open,
  onClose,
  busy,
  containerCreateEnabled,
  createName,
  createImage,
  createRestart,
  createPorts,
  createVolumes,
  createEnv,
  createSource,
  setCreateName,
  setCreateImage,
  setCreateRestart,
  setCreatePorts,
  setCreateVolumes,
  setCreateEnv,
  registryHost: host,
  registryEnabled,
  onSubmit,
}: {
  open: boolean;
  onClose: () => void;
  busy: string;
  containerCreateEnabled: boolean;
  createName: string;
  createImage: string;
  createRestart: string;
  createPorts: string;
  createVolumes: string;
  createEnv: string;
  createSource: string;
  setCreateName: (value: string) => void;
  setCreateImage: (value: string) => void;
  setCreateRestart: (value: string) => void;
  setCreatePorts: (value: string) => void;
  setCreateVolumes: (value: string) => void;
  setCreateEnv: (value: string) => void;
  registryHost: string;
  registryEnabled: boolean;
  onSubmit: (template: CreateContainerTemplate) => void;
}) {
  const firstFieldRef = useRef<HTMLInputElement>(null);
  const previousActiveRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const portResult = useMemo(() => parsePortsStrict(createPorts), [createPorts]);
  const volumeResult = useMemo(() => parseVolumesStrict(createVolumes), [createVolumes]);
  const envResult = useMemo(() => parseEnvStrict(createEnv), [createEnv]);
  const publicBindCount = portResult.ports.filter((port) => port.hostIp === "0.0.0.0" || port.hostIp === "::").length;

  const template = useMemo<CreateContainerTemplate>(() => {
    return {
      name: createName,
      image: createImage,
      restartPolicy: createRestart,
      ports: portResult.ports,
      volumes: volumeResult.volumes,
      env: envResult.env,
    };
  }, [createName, createImage, createRestart, portResult.ports, volumeResult.volumes, envResult.env]);

  const hasParseError = portResult.errors.length > 0 || volumeResult.errors.length > 0 || envResult.errors.length > 0;

  useEffect(() => {
    if (!open) return;
    previousActiveRef.current = document.activeElement as HTMLElement | null;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    const raf = requestAnimationFrame(() => {
      firstFieldRef.current?.focus();
    });
    const handleTabTrap = (event: KeyboardEvent) => {
      if (event.key !== "Tab") return;
      const drawer = document.querySelector<HTMLElement>("[data-drawer='create-container']");
      if (!drawer) return;
      const focusables = drawer.querySelectorAll<HTMLElement>("a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex='-1'])");
      if (!focusables.length) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      if (event.shiftKey) {
        if (document.activeElement === first) {
          event.preventDefault();
          last.focus();
        }
      } else {
        if (document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", handleTabTrap);
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("keydown", handleTabTrap);
      previousActiveRef.current?.focus?.();
    };
  }, [open]);

  if (!open) return null;

  const allowedHint = registryEnabled
    ? `镜像必须位于 ${host}/* 下（Registry 受控来源）。`
    : `允许本机已有的任意镜像（未启用 Registry 前缀校验）。`;

  return (
    <div aria-hidden={false} className="fixed inset-0 z-50">
      <div
        aria-label="关闭创建容器面板"
        className="absolute inset-0 bg-black/30"
        onClick={onClose}
        role="presentation"
      />
      <div
        aria-describedby="create-container-subtitle"
        aria-label="创建容器"
        aria-modal="true"
        className="absolute right-0 top-0 flex h-full w-full max-w-2xl flex-col border-l border-[var(--line)] bg-[var(--bg)] shadow-2xl"
        data-drawer="create-container"
        role="dialog"
      >
        <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] p-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="m-0 text-sm font-semibold">创建容器</h3>
              {createSource ? <Pill tone="good">{createSource}</Pill> : null}
            </div>
            <p className="muted mt-1 mb-0 min-w-0 text-xs" id="create-container-subtitle">
              {allowedHint}
            </p>
          </div>
          <Button onClick={onClose}>关闭</Button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          <div className="grid gap-4">
            {!containerCreateEnabled ? (
              <div className="rounded-lg border-2 border-[var(--warn)] bg-[var(--warn-soft)] p-3 text-sm">
                <strong className="block">容器创建总开关未开启</strong>
                <p className="muted mt-1 mb-0 text-xs">请先到「主机操作」开启容器创建总开关。</p>
              </div>
            ) : null}

            <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
              <Field label="容器名称" help="字母、数字、下划线、点、短横，最长 64 字符。">
                <input
                  className="input mono"
                  onChange={(event) => setCreateName(event.target.value)}
                  ref={firstFieldRef}
                  value={createName}
                />
              </Field>
              <Field label="镜像引用" help={allowedHint}>
                <input className="input mono" onChange={(event) => setCreateImage(event.target.value)} placeholder={registryEnabled ? `${host}/repository:tag` : "repository:tag 或 image ID"} value={createImage} />
              </Field>
            </div>

            <Field
              label="Restart Policy"
              help="unless-stopped 适合长期服务；on-failure 最多重试 5 次。"
            >
              <select className="select mono" onChange={(event) => setCreateRestart(event.target.value)} value={createRestart}>
                <option value="no">no (不自动重启)</option>
                <option value="always">always (总是重启)</option>
                <option value="unless-stopped">unless-stopped (除非手动停止)</option>
                <option value="on-failure">on-failure (失败时重试，最多 5 次)</option>
              </select>
            </Field>

            <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
              <Field
                label="端口映射"
                help="每行一条。格式: hostPort:containerPort[/proto]、containerPort[/proto] 或 bindIp:hostPort:containerPort[/proto]。示例: 8080:80、5432/tcp、0.0.0.0:8080:80/tcp。"
              >
                <textarea
                  className="input mono min-h-[72px]"
                  onChange={(event) => setCreatePorts(event.target.value)}
                  placeholder={`8080:80
0.0.0.0:8080:80/tcp
5432/tcp`}
                  value={createPorts}
                />
                {portResult.errors.length > 0 ? (
                  <div className="mt-1 mb-0 text-xs">
                    {portResult.errors.map((e, i) => (
                      <div className="text-[var(--danger)]" key={i}>
                        行 "{e.line}": {e.reason}
                      </div>
                    ))}
                  </div>
                ) : portResult.ports.length > 0 ? (
                  <p className="muted mt-1 mb-0 text-xs">
                    解析为 {portResult.ports.length} 条端口规则{publicBindCount ? `，${publicBindCount} 条绑定所有网卡。` : "。"}
                  </p>
                ) : null}
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
                {volumeResult.errors.length > 0 ? (
                  <div className="mt-1 mb-0 text-xs">
                    {volumeResult.errors.map((e, i) => (
                      <div className="text-[var(--danger)]" key={i}>
                        行 "{e.line}": {e.reason}
                      </div>
                    ))}
                  </div>
                ) : volumeResult.volumes.length > 0 ? (
                  <p className="muted mt-1 mb-0 text-xs">解析为 {volumeResult.volumes.length} 个命名卷。</p>
                ) : null}
              </Field>
            </div>

            <Field
              label="环境变量"
              help="每行一条 KEY=VALUE。单条 VALUE 最长 4KB，最多 64 条。"
            >
              <textarea
                className="input mono min-h-[72px]"
                onChange={(event) => setCreateEnv(event.target.value)}
                placeholder={`NODE_ENV=production
TZ=Asia/Shanghai
LOG_LEVEL=info`}
                value={createEnv}
              />
              {envResult.errors.length > 0 ? (
                <div className="mt-1 mb-0 text-xs">
                  {envResult.errors.map((e, i) => (
                    <div className="text-[var(--danger)]" key={i}>
                      行 "{e.line}": {e.reason}
                    </div>
                  ))}
                </div>
              ) : envResult.env.length > 0 ? (
                <p className="muted mt-1 mb-0 text-xs">解析为 {envResult.env.length} 条环境变量。</p>
              ) : null}
            </Field>
          </div>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-[var(--line)] p-4">
          <div className="grid gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
            <div>
              端口 {template.ports?.length || 0} / 卷 {template.volumes?.length || 0} / 环境变量 {template.env?.length || 0}
            </div>
            <div className="muted">
              Restart: {template.restartPolicy || "no"}
            </div>
          </div>
          <div className="flex gap-2">
            <Button onClick={onClose}>取消</Button>
            <Button
              disabled={
                !containerCreateEnabled ||
                busy === "container-create" ||
                hasParseError ||
                !createName.trim() ||
                !createImage.trim()
              }
              tone="primary"
              onClick={() => onSubmit(template)}
            >
              {busy === "container-create" ? "提交中" : "创建并启动"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
