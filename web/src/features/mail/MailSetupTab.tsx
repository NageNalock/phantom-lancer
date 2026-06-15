import { useState } from "react";
import type { AppActions } from "../../app/App";
import { Button, Panel, Pill, useDangerConfirm } from "../../components/ui";
import {
  friendlyError,
  mailBinaryDetect,
  mailBinaryDownload,
  mailBinaryInstall,
  mailBinaryUninstall,
  mailSetupInitialize,
  mailSetupImport,
  mailSetupPreflightPorts,
  type MailBinaryDetectResponse,
  type MailPreflightPortsResponse,
} from "../../api/client";

const MOX_VERSION_CHOICES = ["0.8.11", "0.8.10", "0.8.9"] as const;

function fmtSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function BinaryCard({
  title,
  info,
  selected,
  tone,
}: {
  title: string;
  info?: MailBinaryDetectResponse["controlled"];
  selected?: boolean;
  tone: "good" | "warn" | "neutral";
}) {
  const ring = selected ? "ring-2 ring-[var(--accent)]" : "";
  const toneCls =
    tone === "good" ? "border-[var(--good)]" : tone === "warn" ? "border-[var(--warn)]" : "";
  if (!info) {
    return (
      <div className={`rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs muted ${ring}`}>
        <div className="mb-1 flex items-center justify-between">
          <strong>{title}</strong>
          <Pill tone="neutral">未检测到</Pill>
        </div>
        <div className="font-mono">—</div>
      </div>
    );
  }
  return (
    <div className={`rounded-lg border bg-[var(--surface)] p-3 text-xs ${toneCls || "border-[var(--line)]"} ${ring}`}>
      <div className="mb-1 flex items-center justify-between">
        <strong>{title}</strong>
        <Pill tone={tone}>{selected ? "已选中" : info.in_whitelist ? "白名单" : info.version || "已发现"}</Pill>
      </div>
      <div className="muted mb-1 font-mono truncate" title={info.path}>
        {info.path || "—"}
      </div>
      <div className="flex justify-between">
        <span>版本 {info.version || "—"}</span>
        <span>{fmtSize(info.size_bytes)}</span>
      </div>
      {info.source ? <div className="muted mt-1 text-[10px]">来源：{info.source}</div> : null}
    </div>
  );
}

export function MailSetupTab({ actions, reload }: { actions: AppActions; reload: () => Promise<void> }) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  // Panel 1 state
  const [detect, setDetect] = useState<MailBinaryDetectResponse | null>(null);
  const [downloadVersion, setDownloadVersion] = useState<string>(MOX_VERSION_CHOICES[0]);
  const [lastDownloadTempPath, setLastDownloadTempPath] = useState<string>("");
  const [installSrc, setInstallSrc] = useState<string>("");
  const [installForce, setInstallForce] = useState<boolean>(false);

  // Panel 2 state
  const [initHostname, setInitHostname] = useState("mail.example.com");
  const [initAdminEmail, setInitAdminEmail] = useState("");
  const [initWebmailAddr, setInitWebmailAddr] = useState("127.0.0.1:10444");
  const [initWebapiAddr, setInitWebapiAddr] = useState("127.0.0.1:10445");
  const [initUseControlled, setInitUseControlled] = useState(true);
  const [initOverwrite, setInitOverwrite] = useState(false);
  const [initNextSteps, setInitNextSteps] = useState<string[] | null>(null);

  // Panel 3 state
  const [importBinary, setImportBinary] = useState("");
  const [importConfig, setImportConfig] = useState("");
  const [importDataDir, setImportDataDir] = useState("");
  const [importLabel, setImportLabel] = useState("");

  // Panel 4 state
  const [portResult, setPortResult] = useState<MailPreflightPortsResponse | null>(null);

  async function handleDetect() {
    try {
      const res = await mailBinaryDetect({}, actions.csrf);
      setDetect(res);
      actions.setToast("检测完成", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleDownload() {
    try {
      const res = await mailBinaryDownload({ version: downloadVersion }, actions.csrf);
      setLastDownloadTempPath(res.temp_path);
      actions.setToast(
        `下载完成：${res.version} @ ${fmtSize(res.size_bytes)}，sha256=${res.checksum_sha256.slice(0, 16)}…`,
        "good",
      );
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleInstall() {
    try {
      const src =
        installSrc.trim() || lastDownloadTempPath || detect?.selected?.path || detect?.controlled?.path || undefined;
      const res = await mailBinaryInstall(
        { src: src || undefined, force: installForce || undefined },
        actions.csrf,
      );
      actions.setToast(
        res.installed
          ? `安装成功：${res.installed_version} @ ${res.installed_path}`
          : `安装未变更：${res.installed_version}`,
        "good",
      );
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleUninstall() {
    const ok = await confirmDanger({
      title: "卸载 Mox 二进制",
      body: "此操作将删除受管目录中的 Mox 二进制与相关 sidecar 备份。不会删除 mox.conf 或数据目录。",
      confirmLabel: "确认卸载",
      impact: ["失去受管 Mox 二进制", "旧版本备份一并删除", "外部 mox 系统路径不受影响"],
      recovery: "可在本面板重新下载安装，或通过「外部接入」重新指向系统 mox。",
      confirmationText: "uninstall-mox",
      confirmationLabel: "请输入 uninstall-mox 以继续",
      confirmationPlaceholder: "uninstall-mox",
    });
    if (!ok) return;
    try {
      const res = await mailBinaryUninstall({}, actions.csrf);
      actions.setToast(
        `已卸载 ${res.uninstalled_version}，备份删除 ${res.backups_removed}，受管目录：${res.controlled_dir}`,
        "good",
      );
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleInitialize() {
    if (!initHostname.trim() || !initAdminEmail.trim()) {
      actions.setToast("请至少填写 hostname 与 admin_email", "warn");
      return;
    }
    if (initOverwrite) {
      const ok = await confirmDanger({
        title: "覆盖现有 mox.conf",
        body: "已勾选「覆盖现有配置」，将删除/重写现存的 mox.conf，域名与账户配置可能丢失。",
        confirmLabel: "确认覆盖",
        impact: ["现有 mox.conf 将被备份后重写", "已存在的域名、账户元数据可能不再可识别"],
        recovery: "可在 mox 根目录中查找 .bak 备份恢复。",
        confirmationText: `OVERWRITE-${initHostname}`,
        confirmationLabel: `请输入 OVERWRITE-${initHostname} 以继续`,
        confirmationPlaceholder: `OVERWRITE-${initHostname}`,
      });
      if (!ok) return;
    }
    try {
      const res = await mailSetupInitialize(
        {
          hostname: initHostname.trim(),
          admin_email: initAdminEmail.trim(),
          webmail_addr: initWebmailAddr.trim(),
          webapi_addr: initWebapiAddr.trim(),
          use_controlled_binary: initUseControlled,
          overwrite_existing_conf: initOverwrite,
        },
        actions.csrf,
      );
      setInitNextSteps(res.next_steps || []);
      actions.setToast(`初始化完成：配置=${res.config_path} 数据=${res.data_dir}`, "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleImport() {
    if (!importBinary.trim() || !importConfig.trim() || !importDataDir.trim() || !importLabel.trim()) {
      actions.setToast("请完整填写四项导入参数", "warn");
      return;
    }
    try {
      const res = await mailSetupImport(
        {
          binary_path: importBinary.trim(),
          config_path: importConfig.trim(),
          data_dir: importDataDir.trim(),
          label: importLabel.trim(),
        },
        actions.csrf,
      );
      actions.setToast(res.imported ? `已接入外部 Mox「${res.label}」（只读）` : "未变更", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handlePreflightPorts() {
    try {
      const res = await mailSetupPreflightPorts(actions.csrf);
      setPortResult(res);
      actions.setToast(
        res.all_ok ? `所有端口空闲（${res.ports.length} 项）` : `存在端口冲突，请检查 ${res.ports.filter((p) => !p.free).length} 项`,
        res.all_ok ? "good" : "warn",
      );
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}

      {/* Panel 1: Binary management */}
      <Panel
        title="Mox 二进制管理"
        subtitle="检测系统、受管目录与用户提示路径中的 Mox 二进制；从白名单下载并原子安装。"
        actions={
          <div className="flex flex-wrap gap-2">
            <Button onClick={handleDetect}>检测</Button>
            <Button onClick={handleDownload}>下载</Button>
            <Button onClick={handleInstall} tone="primary">
              安装
            </Button>
            <Button onClick={handleUninstall} tone="danger">
              卸载
            </Button>
          </div>
        }
      >
        <div className="grid gap-3">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <label className="field">
              <span>下载版本（白名单）</span>
              <select
                value={downloadVersion}
                onChange={(e) => setDownloadVersion(e.target.value)}
                className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              >
                {MOX_VERSION_CHOICES.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
              {lastDownloadTempPath ? (
                <small className="muted text-xs">
                  最近下载：<code className="font-mono">{lastDownloadTempPath}</code>
                </small>
              ) : null}
            </label>
            <label className="field">
              <span>安装源路径（留空则自动选择）</span>
              <input
                className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
                value={installSrc}
                onChange={(e) => setInstallSrc(e.target.value)}
                placeholder="或：上次下载 temp_path / detected selected path"
              />
            </label>
            <label className="flex items-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs">
              <input
                type="checkbox"
                checked={installForce}
                onChange={(e) => setInstallForce(e.target.checked)}
              />
              <span>
                <strong>强制覆盖</strong>
                <div className="muted">已安装版本更高/相同仍重写（危险）</div>
              </span>
            </label>
          </div>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            <BinaryCard
              title="受管目录"
              info={detect?.controlled}
              selected={!!detect?.selected && detect.selected.path === detect.controlled?.path}
              tone={detect?.controlled?.in_whitelist ? "good" : detect?.controlled ? "warn" : "neutral"}
            />
            <BinaryCard
              title="系统 PATH"
              info={detect?.path}
              selected={!!detect?.selected && detect.selected.path === detect.path?.path}
              tone={detect?.path?.in_whitelist ? "good" : detect?.path ? "warn" : "neutral"}
            />
            <BinaryCard
              title="用户提示路径"
              info={detect?.hint}
              selected={!!detect?.selected && detect.selected.path === detect.hint?.path}
              tone={detect?.hint?.in_whitelist ? "good" : detect?.hint ? "warn" : "neutral"}
            />
          </div>
        </div>
      </Panel>

      {/* Panel 2: Initialize managed Mox */}
      <Panel
        title="初始化托管 Mox 实例"
        subtitle="为受管 Mox 生成 mox.conf 基本骨架、数据目录、管理邮箱与 WebAPI/WebMail 监听地址。"
        actions={
          <Button tone="primary" onClick={handleInitialize}>
            提交初始化
          </Button>
        }
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="field">
            <span>hostname *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={initHostname}
              onChange={(e) => setInitHostname(e.target.value)}
              required
            />
          </label>
          <label className="field">
            <span>admin_email *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={initAdminEmail}
              onChange={(e) => setInitAdminEmail(e.target.value)}
              placeholder="postmaster@example.com"
              required
            />
          </label>
          <label className="field">
            <span>WebMail 监听</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={initWebmailAddr}
              onChange={(e) => setInitWebmailAddr(e.target.value)}
            />
          </label>
          <label className="field">
            <span>WebAPI 监听</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={initWebapiAddr}
              onChange={(e) => setInitWebapiAddr(e.target.value)}
            />
          </label>
          <label className="flex items-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs">
            <input
              type="checkbox"
              checked={initUseControlled}
              onChange={(e) => setInitUseControlled(e.target.checked)}
            />
            <span>
              <strong>使用受管二进制</strong>
              <div className="muted">取消则使用检测到的 PATH 中的 mox</div>
            </span>
          </label>
          <label className="flex items-start gap-2 rounded-md border border-[var(--warn)] bg-[var(--surface-soft)] p-2 text-xs">
            <input
              type="checkbox"
              checked={initOverwrite}
              onChange={(e) => setInitOverwrite(e.target.checked)}
            />
            <span>
              <strong className="text-[var(--warn)]">覆盖现有配置（危险）</strong>
              <div className="muted">提交前将弹出危险二次确认</div>
            </span>
          </label>
        </div>
        {initNextSteps && initNextSteps.length > 0 ? (
          <div className="mt-4 rounded-md border border-[var(--good)] bg-[var(--surface-soft)] p-3 text-xs">
            <div className="mb-1 font-semibold">后续步骤</div>
            <ol className="list-decimal pl-5 text-[var(--muted-strong)]">
              {initNextSteps.map((s, i) => (
                <li key={i}>{s}</li>
              ))}
            </ol>
          </div>
        ) : null}
      </Panel>

      {/* Panel 3: Import external Mox */}
      <Panel
        title="接入外部 Mox（只读模式）"
        subtitle="指向一个外部/系统 mox 实例。只读模式下，Phantom 仅展示与审计，不执行启停与配置写入。"
        actions={
          <Button tone="primary" onClick={handleImport}>
            接入（只读）
          </Button>
        }
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="field">
            <span>binary_path *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={importBinary}
              onChange={(e) => setImportBinary(e.target.value)}
              placeholder="/usr/local/bin/mox"
              required
            />
          </label>
          <label className="field">
            <span>config_path *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={importConfig}
              onChange={(e) => setImportConfig(e.target.value)}
              placeholder="/etc/mox/mox.conf"
              required
            />
          </label>
          <label className="field">
            <span>data_dir *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={importDataDir}
              onChange={(e) => setImportDataDir(e.target.value)}
              placeholder="/var/lib/mox"
              required
            />
          </label>
          <label className="field">
            <span>label *</span>
            <input
              className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              value={importLabel}
              onChange={(e) => setImportLabel(e.target.value)}
              placeholder="production-mail01"
              required
            />
          </label>
        </div>
      </Panel>

      {/* Panel 4: Port preflight */}
      <Panel
        title="端口预检"
        subtitle="检查 SMTP/MSA/SMTPS/IMAP/IMAPS/WebMail/WebAPI 监听端口是否空闲。"
        actions={
          <Button tone="primary" onClick={handlePreflightPorts}>
            执行端口预检
          </Button>
        }
      >
        {portResult ? (
          <div className="grid gap-1">
            {portResult.ports.map((p) => (
              <div
                key={`${p.host}-${p.port}-${p.name}`}
                className="flex items-center gap-3 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs"
              >
                <span
                  className="inline-block h-3 w-3 rounded-full border border-[var(--line)]"
                  style={{ backgroundColor: p.free ? "var(--good)" : "var(--danger)" }}
                  title={p.free ? "空闲" : "占用"}
                />
                <span className="w-24 shrink-0 font-semibold">{p.name}</span>
                <span className="muted w-16 shrink-0 font-mono">{p.host}</span>
                <span className="w-12 shrink-0 font-mono">{p.port}</span>
                <span className={`flex-1 ${p.free ? "" : "text-[var(--danger)]"}`}>
                  {p.free ? "空闲" : "占用"}
                  {p.conflict ? ` — ${p.conflict}` : ""}
                </span>
              </div>
            ))}
            <div className="muted mt-2 text-right text-xs">
              {portResult.all_ok ? (
                <Pill tone="good">全部通过</Pill>
              ) : (
                <Pill tone="danger">存在冲突</Pill>
              )}
            </div>
          </div>
        ) : (
          <div className="muted rounded-md border border-dashed border-[var(--line)] p-6 text-center text-xs">
            尚未执行预检，点击右上角「执行端口预检」开始。
          </div>
        )}
      </Panel>
    </div>
  );
}
