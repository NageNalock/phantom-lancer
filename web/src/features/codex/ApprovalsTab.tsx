import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexApproval } from "../../app/types";
import { Button, EmptyState, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { formatDate } from "../../domain/labels";

export function ApprovalsTab({ actions, onChange }: { actions: AppActions; onChange: () => void }) {
  const [items, setItems] = useState<CodexApproval[]>([]);
  const [status, setStatus] = useState("pending");
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await actions.api<{ items?: CodexApproval[] }>(`/api/codex/approvals?status=${status}`);
      setItems(response.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions, status]);

  useEffect(() => {
    void load();
  }, [load]);

  async function resolve(approval: CodexApproval, action: "approve" | "deny" | "cancel") {
    try {
      await actions.api(`/api/codex/approvals/${approval.id}/${action}`, { method: "POST", csrf: actions.csrf });
      await load();
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <Panel
      actions={
        <>
          <select className="select" onChange={(event) => setStatus(event.target.value)} value={status}>
            <option value="pending">待审批</option>
            <option value="approved">已允许</option>
            <option value="denied">已拒绝</option>
            <option value="all">全部</option>
          </select>
          <Button onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>
        </>
      }
      subtitle="审批请求刷新后仍可处理；超时或重启后默认失败关闭。"
      title="Approvals"
    >
      {items.length ? (
        <div className="grid gap-2">
          {items.map((approval) => (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={approval.id}>
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <strong className="block text-sm">{approval.actionKind || "command"}</strong>
                  {approval.commandPreview ? <span className="mono mt-1 block break-all text-xs text-[var(--muted-strong)]">{approval.commandPreview}</span> : null}
                </div>
                <Pill tone={approval.riskLevel === "high" ? "danger" : approval.riskLevel === "low" ? "good" : "warn"}>{approval.riskLevel || "medium"}</Pill>
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
                {approval.cwdSummary ? <span className="mono">{approval.cwdSummary}</span> : null}
                <span>{statusLabel(approval.status)}</span>
                <span>{formatDate(approval.createdAt)}</span>
              </div>
              {approval.status === "pending" ? (
                <div className="mt-2 flex flex-wrap gap-2">
	                  <Button tone="primary" onClick={() => void resolve(approval, "approve")}>
	                    允许一次
	                  </Button>
	                  <Button onClick={() => void resolve(approval, "cancel")}>
	                    取消操作
                  </Button>
                  <Button tone="danger" onClick={() => void resolve(approval, "deny")}>
                    拒绝
                  </Button>
                </div>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <EmptyState body={loading ? "正在加载审批。" : "当前没有匹配的审批请求。"} title="暂无审批" />
      )}
    </Panel>
  );
}

function statusLabel(value?: string): string {
  return (
    {
      pending: "待审批",
      approved: "已允许",
      denied: "已拒绝",
      cancelled: "已取消",
      failed: "已失败",
      expired: "已过期",
    }[value || ""] ||
    value ||
    "未知"
  );
}
