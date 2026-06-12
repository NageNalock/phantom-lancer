import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, ListenerEndpoint, RuntimeSettings, TLSProbeResult, Tone } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, Field, Panel, Toggle } from "../components/ui";
import { defaultRuntime, formatDate } from "../domain/labels";
import { SystemUpdatePanel } from "./settings/SystemUpdatePanel";
import { ObjectStoragePanel } from "./settings/ObjectStoragePanel";

const DOWNGRADE_PHRASE = "I understand disabling HTTPS will revoke all sessions";

type ListenerRequestBody = {
  addr: string;
  tlsEnabled: boolean;
  tlsCertFile: string;
  tlsKeyFile: string;
  tlsOwnerUidCheck: boolean;
  hstsEnabled: boolean;
  hstsMaxAgeSeconds: number;
  confirm_downgrade?: boolean;
  confirm_phrase?: string;
  confirm_hsts?: boolean;
};

export function SettingsView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [runtime, setRuntime] = useState<RuntimeSettings>(data.settings.runtime || defaultRuntime());
  const [allowedRootsText, setAllowedRootsText] = useState((data.settings.runtime?.allowedRoots || []).join("\n"));
  const [busy, setBusy] = useState("");

  // ---- listener state ----
  const r: RuntimeSettings = (data.settings.runtime ?? {}) as RuntimeSettings;
  const cur: ListenerEndpoint = (data.settings.listener ?? {}) as ListenerEndpoint;
  const [listenAddr, setListenAddr] = useState<string>(cur.addr || r.addr || data.settings.file?.addr || "");
  const [tlsEnabled, setTlsEnabled] = useState<boolean>(Boolean(cur.tlsEnabled || r.tlsEnabled));
  const [tlsCertFile, setTlsCertFile] = useState<string>(r.tlsCertFile || "");
  const [tlsKeyFile, setTlsKeyFile] = useState<string>(r.tlsKeyFile || "");
  const [tlsOwnerUidCheck, setTlsOwnerUidCheck] = useState<boolean>(r.tlsOwnerUidCheck ?? true);
  const [hstsEnabled, setHstsEnabled] = useState<boolean>(Boolean(cur.hstsEnabled || r.hstsEnabled));
  const [hstsMaxAgeSeconds, setHstsMaxAgeSeconds] = useState<number>(cur.hstsMaxAgeSeconds ?? r.hstsMaxAgeSeconds ?? 15724800);
  const [probeBusy, setProbeBusy] = useState(false);
  const [applyBusy, setApplyBusy] = useState(false);
  const [probeResult, setProbeResult] = useState<TLSProbeResult | null>(null);
  const prevEndpoint: ListenerEndpoint | null = (data.settings.listener || null) as any;

  // sync on settings refresh
  useEffect(() => {
    const next = data.settings.runtime || defaultRuntime();
    setRuntime(next);
    setAllowedRootsText((next.allowedRoots || []).join("\n"));
  }, [data.settings.runtime]);

  useEffect(() => {
    const lis = (data.settings.listener ?? {}) as ListenerEndpoint;
    const rt = (data.settings.runtime ?? {}) as RuntimeSettings;
    setListenAddr(lis?.addr || rt.addr || data.settings.file?.addr || "");
    setTlsEnabled(Boolean(lis?.tlsEnabled || rt.tlsEnabled));
    setTlsCertFile(rt.tlsCertFile || "");
    setTlsKeyFile(rt.tlsKeyFile || "");
    setTlsOwnerUidCheck(rt.tlsOwnerUidCheck ?? true);
    setHstsEnabled(Boolean(lis?.hstsEnabled || rt.hstsEnabled));
    setHstsMaxAgeSeconds(lis?.hstsMaxAgeSeconds ?? rt.hstsMaxAgeSeconds ?? 15724800);
  }, [data.settings.listener, data.settings.runtime, data.settings.file?.addr]);

  const listenerDirty = useMemo(() => {
    const lis = (data.settings.listener ?? {}) as ListenerEndpoint;
    const rt = (data.settings.runtime ?? {}) as RuntimeSettings;
    return (
      listenAddr.trim() !== (rt.addr || lis.addr || "").trim() ||
      tlsEnabled !== Boolean(rt.tlsEnabled || lis.tlsEnabled) ||
      tlsCertFile.trim() !== (rt.tlsCertFile || "").trim() ||
      tlsKeyFile.trim() !== (rt.tlsKeyFile || "").trim() ||
      tlsOwnerUidCheck !== (rt.tlsOwnerUidCheck ?? true) ||
      hstsEnabled !== Boolean(rt.hstsEnabled || lis.hstsEnabled) ||
      hstsMaxAgeSeconds !== (rt.hstsMaxAgeSeconds ?? lis.hstsMaxAgeSeconds ?? 15724800)
    );
  }, [
    listenAddr,
    tlsEnabled,
    tlsCertFile,
    tlsKeyFile,
    tlsOwnerUidCheck,
    hstsEnabled,
    hstsMaxAgeSeconds,
    data.settings,
  ]);

  // -------- runtime save --------
  async function saveRuntime() {
    const payload: RuntimeSettings = {
      ...runtime,
      allowedRoots: allowedRootsText
        .split("\n")
        .map((item) => item.trim())
        .filter(Boolean),
    };
    setBusy("runtime");
    try {
      await actions.api("/api/settings", { method: "PUT", csrf: actions.csrf, body: payload });
      await actions.reloadData();
      actions.setToast("运行设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  // -------- probe TLS --------
  async function probeTLS() {
    if (!tlsCertFile.trim() || !tlsKeyFile.trim()) {
      actions.setToast("请先填写证书和私钥文件路径", "warn");
      return;
    }
    setProbeBusy(true);
    try {
      const res = await actions.api<TLSProbeResult>("/api/settings/tls-probe", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          certFile: tlsCertFile.trim(),
          keyFile: tlsKeyFile.trim(),
          ownerUidCheck: tlsOwnerUidCheck,
        },
      });
      setProbeResult(res);
      actions.setToast(
        res.ok
          ? `证书校验通过（剩余 ${res.daysRemaining ?? "?"} 天）`
          : res.error || "证书校验失败",
        res.ok ? "good" : "danger"
      );
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    } finally {
      setProbeBusy(false);
    }
  }

  // -------- smart endpoint navigation --------
  async function navigateToEndpoint(opts: { addr: string; tlsEnabled: boolean; forceScheme?: "https" | "http" }) {
    const current = new URL(window.location.href);
    const idx = opts.addr.lastIndexOf(":");
    if (idx <= 0) return;
    const host = opts.addr.slice(0, idx);
    const port = opts.addr.slice(idx + 1);
    const target = new URL(current.toString());
    if (host === "0.0.0.0" || host === "::" || host === "[::]") {
      // wildcard bind: keep browser hostname, replace port/scheme
    } else {
      target.hostname = host.startsWith("[") ? host.slice(1, -1) : host;
    }
    target.port = port;
    target.protocol = (opts.forceScheme || (opts.tlsEnabled ? "https" : "http")) + ":";
    const targetStr = target.toString();

    // Lightweight probe — best effort, never blocks navigation.
    let networkErr = false;
    for (let i = 0; i < 3; i++) {
      await new Promise<void>((res) => setTimeout(() => res(), 350 + i * 250));
      try {
        const ctrl = new AbortController();
        setTimeout(() => ctrl.abort(), 1500);
        await fetch(targetStr, { method: "HEAD", signal: ctrl.signal, credentials: "omit", mode: "no-cors" });
        break;
      } catch (err: any) {
        if (err?.name === "AbortError") {
          // continue trying
        } else {
          networkErr = true;
          break;
        }
      }
    }

    const win = window.open(targetStr, "_blank", "noopener,noreferrer");
    if (!win) {
      actions.setToast("弹窗被拦截，请手动前往：" + targetStr, "info" as Tone);
    }
    if (networkErr && (opts.forceScheme === "https" || opts.tlsEnabled)) {
      actions.setToast(
        "提示：HTTPS 已启用，若新标签页无法打开，请在浏览器中信任自签证书后刷新",
        "info" as Tone
      );
    }
  }

  // -------- apply listener --------
  async function applyListener() {
    const addr = listenAddr.trim();
    if (!addr) {
      actions.setToast("监听地址不能为空", "warn");
      return;
    }
    const m = /^([^\s:]+):(\d{1,5})$/.exec(addr);
    if (!m) {
      actions.setToast("地址格式应为 host:port（例如 127.0.0.1:8080 或 0.0.0.0:8443）", "danger");
      return;
    }
    const port = Number(m[2]);
    if (port < 1 || port > 65535) {
      actions.setToast("端口必须在 1-65535 之间", "danger");
      return;
    }
    if (tlsEnabled && (!tlsCertFile.trim() || !tlsKeyFile.trim())) {
      actions.setToast("启用 HTTPS 时证书和私钥路径均不能为空", "warn");
      return;
    }

    const wasHTTPS =
      Boolean(prevEndpoint?.tlsEnabled) || Boolean(data.settings.runtime?.tlsEnabled);
    const nowHTTPS = tlsEnabled;

    const body: ListenerRequestBody = {
      addr,
      tlsEnabled: nowHTTPS,
      tlsCertFile: tlsCertFile.trim(),
      tlsKeyFile: tlsKeyFile.trim(),
      tlsOwnerUidCheck,
      hstsEnabled,
      hstsMaxAgeSeconds: Number(hstsMaxAgeSeconds) || 0,
    };

    // ---- M7 downgrade gate ----
    if (wasHTTPS && !nowHTTPS) {
      const ok1 = window.confirm(
        "⚠️ 即将关闭 HTTPS。\n\n" +
          "为防止会话 cookie 在 HTTP 上泄露，所有已登录会话（包括你自己的）都将被立即撤销。\n\n" +
          "继续前需要你输入确认短语。"
      );
      if (!ok1) return;
      const phrase = window.prompt(
        "请输入确认短语：",
        DOWNGRADE_PHRASE
      );
      if (phrase !== DOWNGRADE_PHRASE) {
        actions.setToast("确认短语不匹配，已取消", "warn");
        return;
      }
      body.confirm_downgrade = true;
      body.confirm_phrase = phrase;
    }

    // ---- HSTS enable gate ----
    if (hstsEnabled && !Boolean(data.settings.runtime?.hstsEnabled)) {
      const ok2 = window.confirm(
        "⚠️ 启用 HSTS 后，浏览器将在未来强制使用 HTTPS 访问此域名（即使手动输入 http://）。\n\n" +
          "如果后续关闭 HTTPS，浏览器可能会在 HSTS max-age 过期前拒绝通过 HTTP 访问。\n\n" +
          "确定启用？"
      );
      if (!ok2) return;
      body.confirm_hsts = true;
    }

    setApplyBusy(true);
    try {
      const resp = (await actions.api<any>("/api/settings/listener", {
        method: "POST",
        csrf: actions.csrf,
        body,
      })) as {
        addr: string;
        endpoint: ListenerEndpoint;
        runtime?: RuntimeSettings;
        downgradeRedirect?: string;
        splitStateWarning?: boolean;
        upgradeScheme?: "https";
      };

      actions.setToast(
        resp.splitStateWarning
          ? "⚠️ 状态不同步，请刷新页面确认"
          : `已切换到 ${(resp.endpoint?.scheme || "http").toUpperCase()}://${resp.addr}`,
        resp.splitStateWarning ? "warn" : "good"
      );

      const addrChanged = resp.addr !== (prevEndpoint?.addr || "");
      const schemeChanged = nowHTTPS !== wasHTTPS;
      if (addrChanged || schemeChanged) {
        await navigateToEndpoint({
          addr: resp.addr,
          tlsEnabled: nowHTTPS,
          forceScheme: resp.upgradeScheme,
        });
      }

      if (resp.downgradeRedirect) {
        setTimeout(() => {
          window.location.href = "/login";
        }, 1200);
      } else {
        await actions.reloadData();
      }
    } catch (e: any) {
      await actions.reloadData();
      const msg = friendlyError(e);
      if (e?.code === "confirm_required") {
        actions.setToast("请先完成二次确认：" + msg, "warn");
      } else if (e?.code === "confirm_hsts_required") {
        actions.setToast("启用 HSTS 需要二次确认", "warn");
      } else {
        actions.setToast(msg, "danger");
      }
    } finally {
      setApplyBusy(false);
    }
  }

  // -------- render helpers --------
  function pillClass() {
    if (prevEndpoint?.tlsEnabled) return "pill pill-good";
    return "pill pill-neutral";
  }
  const certSummaryItems: [string, any][] = [];
  if (probeResult) {
    certSummaryItems.push(["主题", probeResult.subject || "—"]);
    certSummaryItems.push(["颁发者", probeResult.issuer || "—"]);
    certSummaryItems.push([
      "DNS 名称",
      (probeResult.dnsNames || prevEndpoint?.certDnsNames || []).join(", ") || "—",
    ]);
    certSummaryItems.push(["生效", probeResult.notBefore || prevEndpoint?.certNotBefore || "—"]);
    certSummaryItems.push([
      "过期",
      <>
        <span
          style={
            typeof probeResult.daysRemaining === "number" && probeResult.daysRemaining < 30
              ? { color: "var(--danger, #c0392b)" }
              : undefined
          }
        >
          {probeResult.notAfter || prevEndpoint?.certNotAfter || "—"}
          {typeof probeResult.daysRemaining === "number"
            ? `（剩余 ${probeResult.daysRemaining} 天）`
            : ""}
        </span>
      </>,
    ]);
    certSummaryItems.push(["序列号", probeResult.serialNumber || "—"]);
    certSummaryItems.push(["文件所有者", probeResult.fileOwnerName || "—"]);
  } else if (prevEndpoint?.tlsEnabled) {
    certSummaryItems.push(["证书路径", prevEndpoint.certFile || "—"]);
    certSummaryItems.push(["DNS 名称", (prevEndpoint.certDnsNames || []).join(", ") || "—"]);
    certSummaryItems.push(["生效", prevEndpoint.certNotBefore || "—"]);
    certSummaryItems.push(["过期", prevEndpoint.certNotAfter || "—"]);
  }
  certSummaryItems.push(["最近证书重载错误", prevEndpoint?.certReloadErr || "无"]);

  return (
    <div className="grid gap-4 p-4">
      <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(300px,0.95fr)] gap-4 max-xl:grid-cols-1">
        <Panel title="运行设置" subtitle="影响允许工作目录和 Cookie 安全策略">
          <div className="grid gap-4">
            <Field label="允许根目录" help="每行一个绝对路径；后端会归一化并拒绝无效目录。">
              <textarea
                className="textarea mono min-h-36"
                onChange={(event) => setAllowedRootsText(event.target.value)}
                value={allowedRootsText}
              />
            </Field>
            <Toggle
              checked={Boolean(runtime.cookieSecure)}
              label="HTTPS 部署时启用 Secure Cookie"
              onChange={(checked) => setRuntime((current) => ({ ...current, cookieSecure: checked }))}
            />
            <div className="flex flex-wrap justify-between gap-2">
              <span className="muted text-xs">最后更新：{formatDate(runtime.updatedAt) || "-"}</span>
              <Button disabled={busy === "runtime"} onClick={() => void saveRuntime()} tone="primary">
                保存运行设置
              </Button>
            </div>
          </div>
        </Panel>

        <Panel title="服务配置" subtitle="切换监听地址、启用 HTTPS 无需重启进程">
          <div className="grid gap-4">
            {/* status pills */}
            <div className="flex items-center gap-2 flex-wrap">
              <span className={pillClass()}>{(prevEndpoint?.scheme || "HTTP").toUpperCase()}</span>
              <span className="mono text-sm muted">
                {prevEndpoint?.addr || listenAddr || "未配置"}
              </span>
              {prevEndpoint?.certReloadErr && (
                <span className="pill pill-danger" title={prevEndpoint.certReloadErr}>
                  cert err
                </span>
              )}
              {prevEndpoint?.hstsEnabled && <span className="pill pill-warn">HSTS</span>}
            </div>

            {/* listen address */}
            <Field
              label="监听地址"
              help="切换后旧连接在 2 秒内强制断开，新地址在进程重启后仍然生效。"
            >
              <input
                className="input mono w-full"
                onChange={(e) => setListenAddr(e.target.value)}
                placeholder="host:port，例如 0.0.0.0:8443"
                value={listenAddr}
              />
            </Field>

            {/* TLS panel */}
            <div className="border border-border rounded p-3 grid gap-3">
              <Toggle checked={tlsEnabled} label="启用 HTTPS (TLS)" onChange={setTlsEnabled} />
              {tlsEnabled && (
                <div className="grid gap-3" style={{ paddingLeft: "1.5rem" }}>
                  <Field
                    label="证书文件路径 (fullchain.pem)"
                    help="推荐使用绝对路径，建议放在 <data_dir>/tls/ 下。"
                  >
                    <input
                      className="input mono w-full"
                      value={tlsCertFile}
                      onChange={(e) => setTlsCertFile(e.target.value)}
                      placeholder="/var/lib/phantom-lancer/tls/fullchain.pem"
                    />
                  </Field>
                  <Field
                    label="私钥文件路径 (privkey.pem)"
                    help="文件权限必须为 600（不可组/其他可写）。"
                  >
                    <input
                      className="input mono w-full"
                      value={tlsKeyFile}
                      onChange={(e) => setTlsKeyFile(e.target.value)}
                      placeholder="/var/lib/phantom-lancer/tls/privkey.pem"
                    />
                  </Field>
                  <Toggle
                    checked={tlsOwnerUidCheck}
                    label="验证私钥文件必须归当前进程用户所有（推荐开启）"
                    onChange={setTlsOwnerUidCheck}
                  />
                  <div className="flex gap-2 flex-wrap">
                    <Button
                      tone="neutral"
                      disabled={probeBusy || !tlsCertFile.trim() || !tlsKeyFile.trim()}
                      onClick={() => void probeTLS()}
                    >
                      {probeBusy ? "校验中…" : "测试证书"}
                    </Button>
                  </div>
                  {(probeResult || (prevEndpoint?.tlsEnabled && prevEndpoint.certDnsNames)) && (
                    <ContextList items={certSummaryItems} />
                  )}
                </div>
              )}
            </div>

            {/* HSTS panel */}
            <div className="border border-border rounded p-3 grid gap-3">
              <Toggle
                checked={hstsEnabled}
                label="启用 HTTP Strict Transport Security (HSTS)"
                onChange={setHstsEnabled}
              />
              {hstsEnabled && (
                <Field
                  label="HSTS max-age（秒）"
                  help="浏览器强制 HTTPS 的缓存时长。15724800 ≈ 182 天，推荐值。"
                >
                  <div style={{ paddingLeft: "1.5rem" }}>
                    <input
                      type="number"
                      min={0}
                      max={63072000}
                      className="input mono"
                      value={hstsMaxAgeSeconds}
                      onChange={(e) => setHstsMaxAgeSeconds(Number(e.target.value) || 0)}
                    />
                  </div>
                </Field>
              )}
            </div>

            <div className="flex justify-between items-center">
              <span className="text-xs muted">
                {data.settings.file?.tlsBootStrict &&
                  "⚠️ PL_TLS_BOOT_STRICT 已启用：TLS 失败将导致启动失败"}
              </span>
              <Button
                tone="primary"
                disabled={applyBusy || !listenerDirty || !listenAddr.trim()}
                onClick={() => void applyListener()}
              >
                {applyBusy ? "应用中…" : "应用端点配置"}
              </Button>
            </div>

            <ContextList
              items={[
                ["配置文件", data.settings.file?.configPath || "-"],
                ["数据目录", data.settings.file?.dataDir || "-"],
                ["数据库", data.settings.file?.dbPath || "-"],
              ]}
            />
          </div>
        </Panel>
      </div>

      <ObjectStoragePanel actions={actions} />
      <SystemUpdatePanel actions={actions} />
    </div>
  );
}
