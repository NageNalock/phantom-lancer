import { useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, RuntimeSettings } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, Panel } from "../components/ui";
import { defaultRuntime, formatDate } from "../domain/labels";
import { SystemUpdatePanel } from "./settings/SystemUpdatePanel";
import { ObjectStoragePanel } from "./settings/ObjectStoragePanel";

export function SettingsView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [runtime, setRuntime] = useState<RuntimeSettings>(data.settings.runtime || defaultRuntime());
  const [allowedRootsText, setAllowedRootsText] = useState((data.settings.runtime?.allowedRoots || []).join("\n"));
  const [busy, setBusy] = useState("");

  useEffect(() => {
    const next = data.settings.runtime || defaultRuntime();
    setRuntime(next);
    setAllowedRootsText((next.allowedRoots || []).join("\n"));
  }, [data.settings.runtime]);

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

  return (
    <div className="grid gap-4 p-4">
      <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(300px,0.95fr)] gap-4 max-xl:grid-cols-1">
        <Panel title="运行设置" subtitle="影响允许工作目录和 Cookie 安全策略">
          <div className="grid gap-4">
            <label className="field">
              <span>允许根目录</span>
              <textarea className="textarea mono min-h-36" onChange={(event) => setAllowedRootsText(event.target.value)} value={allowedRootsText} />
              <small className="muted text-xs">每行一个绝对路径；后端会归一化并拒绝无效目录。</small>
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input checked={Boolean(runtime.cookieSecure)} onChange={(event) => setRuntime((current) => ({ ...current, cookieSecure: event.target.checked }))} type="checkbox" />
              HTTPS 部署时启用 Secure Cookie
            </label>
            <div className="flex flex-wrap justify-between gap-2">
              <span className="muted text-xs">最后更新：{formatDate(runtime.updatedAt) || "-"}</span>
              <Button disabled={busy === "runtime"} onClick={() => void saveRuntime()} tone="primary">
                保存运行设置
              </Button>
            </div>
          </div>
        </Panel>

        <Panel title="配置文件" subtitle="只读展示当前服务启动参数">
          <ContextList
            items={[
              ["监听", data.settings.file?.addr || "-"],
              ["配置文件", data.settings.file?.configPath || "-"],
              ["数据目录", data.settings.file?.dataDir || "-"],
              ["数据库", data.settings.file?.dbPath || "-"],
            ]}
          />
        </Panel>
      </div>

      <ObjectStoragePanel actions={actions} />

      <SystemUpdatePanel actions={actions} />

    </div>
  );
}
