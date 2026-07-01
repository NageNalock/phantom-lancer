import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexCapabilitySummary } from "../../app/types";
import { Button, EmptyState, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { formatDate } from "../../domain/labels";

const KINDS = ["skills", "mcp", "plugins"];

export function CapabilitiesTab({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<CodexCapabilitySummary[]>([]);

  const load = useCallback(async () => {
    const next = await Promise.all(KINDS.map(async (kind) => {
      const response = await actions.api<{ capability?: CodexCapabilitySummary }>(`/api/codex/capabilities/${kind}`);
      return response.capability || { kind, status: "unknown" };
    }));
    setItems(next);
  }, [actions]);

  useEffect(() => {
    void load().catch((error) => actions.setToast(friendlyError(error), "danger"));
  }, [actions, load]);

  async function probe() {
    try {
      await actions.api("/api/codex/capabilities/probe", { method: "POST", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <Panel actions={<Button onClick={() => void probe()}>重新探测</Button>} subtitle="只展示 Codex 可安全探测到的扩展摘要；不读取或展示 secret/env/header。" title="能力">
      <div className="grid gap-2">
        {items.map((item) => (
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={item.kind}>
            <div className="flex items-center justify-between gap-2">
              <strong className="text-sm">{item.kind}</strong>
              <Pill tone={item.status === "available" ? "good" : item.status === "unavailable" ? "danger" : "warn"}>{item.status || "unknown"}</Pill>
            </div>
            {item.lastError ? <p className="mt-2 mb-0 text-xs text-[var(--danger)]">{item.lastError}</p> : null}
            <p className="muted mt-1 mb-0 text-xs">probed {formatDate(item.probedAt) || "-"}</p>
            {item.items?.length ? (
              <pre className="mono mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--line)] p-2 text-xs">{JSON.stringify(item.items, null, 2)}</pre>
            ) : (
              <EmptyState title="不可探测" body="当前 CLI 未提供稳定安全摘要接口，Phantom Lancer 不解析含 secret 的完整配置。" />
            )}
          </div>
        ))}
      </div>
    </Panel>
  );
}
