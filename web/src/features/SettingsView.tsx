import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, RuntimeSettings } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, Field, Panel, SubTabs, Toggle } from "../components/ui";
import { defaultRuntime, formatDate } from "../domain/labels";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { SystemUpdatePanel } from "./settings/SystemUpdatePanel";
import { ObjectStoragePanel } from "./settings/ObjectStoragePanel";

type SettingsTab = "runtime" | "storage" | "updates";

const SETTINGS_TABS: Array<{ id: SettingsTab; label: string }> = [
  { id: "runtime", label: "运行与服务" },
  { id: "storage", label: "对象存储" },
  { id: "updates", label: "系统更新" },
];
const SETTINGS_TAB_IDS: SettingsTab[] = SETTINGS_TABS.map((item) => item.id);
const SETTINGS_CLEAR_KEYS = ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker"];

export function SettingsView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab, tabHref] = useQueryParamState<SettingsTab>("settings", SETTINGS_TAB_IDS, "runtime", { clearKeys: SETTINGS_CLEAR_KEYS });
  const [runtime, setRuntime] = useState<RuntimeSettings>(data.settings.runtime || defaultRuntime());
  const [allowedRootsText, setAllowedRootsText] = useState((data.settings.runtime?.allowedRoots || []).join("\n"));
  const [busy, setBusy] = useState("");
  const [listenAddr, setListenAddr] = useState<string>(data.settings.file?.addr || "");
  const [swapBusy, setSwapBusy] = useState(false);

  const listenAddrDirty = useMemo(() => {
    const current = data.settings.file?.addr || "";
    return listenAddr.trim() !== current.trim() && listenAddr.trim() !== "";
  }, [listenAddr, data.settings.file?.addr]);

  useEffect(() => {
    const next = data.settings.runtime || defaultRuntime();
    setRuntime(next);
    setAllowedRootsText((next.allowedRoots || []).join("\n"));
  }, [data.settings.runtime]);

  useEffect(() => {
    setListenAddr(data.settings.file?.addr || "");
  }, [data.settings.file?.addr]);

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

  async function applyListenAddr() {
    const addr = listenAddr.trim();
    // Client-side validation — server's net.Listen call is the ground truth;
    // these checks just give fast feedback for obviously-wrong input.
    const m = /^([^\s:]+):(\d{1,5})$/.exec(addr);
    if (!m) {
      actions.setToast("地址格式应为 host:port（例如 127.0.0.1:8080 或 0.0.0.0:9090）", "danger");
      return;
    }
    const port = Number(m[2]);
    if (port < 1 || port > 65535) {
      actions.setToast("端口必须在 1-65535 之间", "danger");
      return;
    }
    setSwapBusy(true);
    try {
      await actions.api("/api/settings/listen-addr", {
        method: "POST",
        csrf: actions.csrf,
        body: { addr },
      });
      await actions.reloadData();
      actions.setToast(`监听地址已切换到 ${addr}`, "good");
      // If the bind address is specific enough, offer to navigate the browser
      // to the new host:port.  For wildcard binds (0.0.0.0, ::) we only
      // change the port portion of the current URL.
      try {
        const current = new URL(window.location.href);
        const [host, portStr] = [m[1], m[2]];
        let navigateTo: string | null = null;
        if (host === "0.0.0.0" || host === "::" || host === "[::]") {
          if (current.port !== portStr) {
            current.port = portStr;
            navigateTo = current.toString();
          }
        } else if (host.toLowerCase() !== "localhost" && !host.startsWith("127.")) {
          // Explicit bind to a non-localhost host/IP — attempt navigation if
          // the hostname or port actually differs from what the browser has.
          if (current.hostname !== host || current.port !== portStr) {
            current.hostname = host;
            current.port = portStr;
            navigateTo = current.toString();
          }
        } else {
          // localhost / 127.x.x.x bind: change port if it differs.
          if (current.port !== portStr) {
            current.port = portStr;
            navigateTo = current.toString();
          }
        }
        if (navigateTo) {
          const target = navigateTo;
          setTimeout(() => {
            window.location.href = target;
          }, 600);
        }
      } catch {
        /* ignore malformed location.href (e.g. inside test harness) */
      }
    } catch (error) {
      // Reset the input to the real (unchanged) effective address.
      void actions.reloadData();
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSwapBusy(false);
    }
  }

  return (
    <div className="grid gap-4 p-4">
      <SubTabs activeId={tab} onChange={(id) => setTab(id as SettingsTab)} tabs={SETTINGS_TABS.map((item) => ({ ...item, href: tabHref(item.id) }))} />

      {tab === "runtime" ? (
        <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(300px,0.95fr)] gap-4 max-xl:grid-cols-1">
          <Panel title="运行设置" subtitle="影响允许工作目录和 Cookie 安全策略">
            <div className="grid gap-4">
              <label className="field">
                <span>允许根目录</span>
                <textarea
                  autoComplete="off"
                  className="textarea mono min-h-36"
                  name="allowed_roots"
                  onChange={(event) => setAllowedRootsText(event.target.value)}
                  spellCheck={false}
                  value={allowedRootsText}
                />
                <small className="muted text-xs">每行一个绝对路径；后端会归一化并拒绝无效目录。</small>
              </label>
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

          <Panel title="服务配置" subtitle="修改监听地址无需重启；其他启动参数为只读">
            <div className="grid gap-4">
              <Field label="监听地址" help="切换后旧连接将在 2 秒内强制断开，新地址在进程重启后仍然生效。">
                <div className="flex gap-2">
                  <input
                    autoComplete="off"
                    className="input mono flex-1"
                    name="listen_addr"
                    onChange={(event) => setListenAddr(event.target.value)}
                    placeholder="host:port，例如 0.0.0.0:8080"
                    spellCheck={false}
                    value={listenAddr}
                  />
                  <Button
                    disabled={swapBusy || !listenAddrDirty}
                    onClick={() => void applyListenAddr()}
                    tone="primary"
                  >
                    应用
                  </Button>
                </div>
              </Field>
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
      ) : null}

      {tab === "storage" ? <ObjectStoragePanel actions={actions} /> : null}

      {tab === "updates" ? <SystemUpdatePanel actions={actions} /> : null}

    </div>
  );
}
