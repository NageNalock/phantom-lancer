import { useEffect, useState } from "react";
import { ArrowClockwise, Plus } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { ApiError, StockV2AgentListResponse, StockV2Opportunity } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Notice, Pill } from "../../components/ui";
import {
  formatDate,
  stockV2OpportunityMarketScopeLabel,
  stockV2OpportunityStatusLabel,
  stockV2OpportunityStatusTone,
} from "../../domain/labels";
import { SimplePager } from "./StockV2EmbeddingStatusSection";

const PAGE_SIZE = 10;

// Opportunity 列表（左栏）。懒加载 + 分页。后端未实现时 404 → 空态 + 创建按钮仍可见。
export function StockV2OpportunityList({
  actions,
  selectedId,
  onSelect,
  onCreate,
}: {
  actions: AppActions;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreate: () => void;
}) {
  const [items, setItems] = useState<StockV2Opportunity[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  async function load(nextPage = page) {
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((Math.max(1, nextPage) - 1) * PAGE_SIZE),
      });
      const res = await actions.api<StockV2AgentListResponse<StockV2Opportunity>>(
        `/api/stockv2/opportunities?${params}`,
      );
      const nextItems = res.items || [];
      const nextTotal = res.total ?? nextItems.length;
      const nextTotalPages = Math.max(1, Math.ceil(nextTotal / PAGE_SIZE));
      setTotal(nextTotal);
      if (nextPage > nextTotalPages) {
        setItems([]);
        setPage(nextTotalPages);
        return;
      }
      setItems(nextItems);
    } catch (err) {
      const status = (err as ApiError).status;
      setError(status === 404 ? "主题研究后端尚未实现（404）。可在 Agent 的语义召回页面独立检查向量能力。" : friendlyError(err));
      setItems([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(page);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);

  return (
    <div className="flex flex-col rounded-lg border border-[var(--line)] bg-[var(--surface)]">
      <div className="flex items-center justify-between gap-2 border-b border-[var(--line)] px-3 py-2">
        <strong className="text-sm">主题机会</strong>
        <div className="flex items-center gap-1.5">
          <Button onClick={() => void load(page)} title="刷新列表">
            <ArrowClockwise size={14} />
          </Button>
          <Button tone="primary" onClick={onCreate} title="新建主题机会">
            <Plus size={14} className="mr-1" />
            新建
          </Button>
        </div>
      </div>

      <div className="grid min-h-32 content-start gap-1.5 p-2">
        {loading && items.length === 0 ? (
          <p className="p-3 text-sm text-[var(--muted)]">加载中…</p>
        ) : error ? (
          <div className="grid gap-2 p-2">
            <Notice tone="danger">{error}</Notice>
            <Button onClick={() => void load(page)}>重试</Button>
          </div>
        ) : items.length === 0 ? (
          <EmptyState title="暂无主题机会" body="新建一个主题机会，启动 Codex CLI 研究与候选发现。" />
        ) : (
          items.map((opp) => {
            const active = opp.id === selectedId;
            return (
              <button
                type="button"
                key={opp.id}
                onClick={() => onSelect(opp.id)}
                className={`w-full rounded-md border px-3 py-2 text-left text-xs transition ${
                  active
                    ? "border-[var(--accent)] bg-[var(--surface-strong)]"
                    : "border-[var(--line)] bg-[var(--surface)] hover:bg-[var(--surface-soft)]"
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 truncate text-sm font-medium">{opp.title}</span>
                  <Pill tone={stockV2OpportunityStatusTone(opp.status)}>{stockV2OpportunityStatusLabel(opp.status)}</Pill>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[var(--muted)]">
                  <span>{stockV2OpportunityMarketScopeLabel(opp.marketScope)}</span>
                  <span>·</span>
                  <span>{formatDate(opp.updatedAt || opp.createdAt) || "-"}</span>
                </div>
              </button>
            );
          })
        )}
      </div>

      {items.length > 0 ? (
        <div className="border-t border-[var(--line)] px-2 py-2">
          <SimplePager loading={loading} page={page} pageSize={PAGE_SIZE} total={total} totalPages={totalPages} onPage={setPage} />
        </div>
      ) : null}
    </div>
  );
}
