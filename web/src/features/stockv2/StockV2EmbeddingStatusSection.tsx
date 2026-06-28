import { useEffect, useState } from "react";
import { ArrowClockwise, Database, Hammer } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { StockV2EmbeddingAsset, StockV2EmbeddingAssetStatus, StockV2EmbeddingStatus } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, EmptyState, Notice, Pill } from "../../components/ui";
import {
  stockV2EmbeddingAssetStatusLabel,
  stockV2EmbeddingAssetStatusTone,
  stockV2EmbeddingErrorCodeLabel,
} from "../../domain/labels";
import { StockV2EmbeddingRebuildDrawer } from "./StockV2EmbeddingRebuildDrawer";

// Embedding 状态区（顶部常驻 Panel，独立加载 /embeddings/status）。
// 它是「主题机会」页语义召回能力的前提状态，与 opportunity 列表解耦：
// 即便 opportunity 相关后端 404，本区仍展示真实的模型绑定与向量资产状态。
// 拦截逻辑只读 status.available，不在前端做 embedding 计算。
export function StockV2EmbeddingStatusSection({ actions }: { actions: AppActions }) {
  const [status, setStatus] = useState<StockV2EmbeddingStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showRebuild, setShowRebuild] = useState(false);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2EmbeddingStatus>("/api/stockv2/embeddings/status");
      setStatus(res);
    } catch (err) {
      setError(friendlyError(err));
      setStatus(null);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const embeddingReady = !!status?.available;
  const disabledReason = !embeddingReady
    ? status?.errorMessage || stockV2EmbeddingErrorCodeLabel(status?.errorCode)
    : null;

  return (
    <>
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <Database size={16} className="text-[var(--muted-strong)]" />
            <strong className="text-sm">向量化与语义召回</strong>
            {loading ? (
              <Pill tone="neutral">加载中…</Pill>
            ) : status ? (
              <Pill tone={embeddingReady ? "good" : "danger"}>{embeddingReady ? "可用" : "不可用"}</Pill>
            ) : (
              <Pill tone="warn">状态未知</Pill>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={() => void load()} title="重新拉取状态">
              <ArrowClockwise size={14} className="mr-1.5" />
              刷新
            </Button>
            <Button
              tone="primary"
              disabled={!embeddingReady}
              title={disabledReason || "重建向量化资产"}
              onClick={() => setShowRebuild(true)}
            >
              <Hammer size={14} className="mr-1.5" />
              重建向量化
            </Button>
          </div>
        </div>

        {error ? (
          <div className="mt-3">
            <Notice tone="danger">加载 embedding 状态失败：{error}</Notice>
          </div>
        ) : null}

        {!loading && status ? (
          <>
            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
              <span>
                绑定模型：<strong className="text-[var(--text)]">{status.modelName || status.modelId || "未绑定"}</strong>
              </span>
              {status.embeddingProtocol ? (
                <span>
                  协议：<span className="font-mono">{status.embeddingProtocol}</span>
                </span>
              ) : null}
              {status.embeddingDimensions ? (
                <span>
                  维度：<span className="font-mono">{status.embeddingDimensions}</span>
                </span>
              ) : null}
              <span>
                启用：<strong className="text-[var(--text)]">{status.config?.enabled ? "是" : "否"}</strong>
              </span>
            </div>

            <div className="mt-3 grid grid-cols-3 gap-2 max-sm:grid-cols-1">
              <CountCell label="就绪向量" value={status.readyAssetCount} tone="good" />
              <CountCell label="过期向量" value={status.staleAssetCount} tone={status.staleAssetCount > 0 ? "warn" : "neutral"} />
              <CountCell label="失败向量" value={status.failedAssetCount} tone={status.failedAssetCount > 0 ? "danger" : "neutral"} />
            </div>

            {!embeddingReady && disabledReason ? (
              <div className="mt-3">
                <Notice tone="danger">
                  语义向量召回与重建已禁用：{disabledReason}。关键词搜索、项目内资料查询与外部搜索仍可独立使用，但不会标记为向量召回。
                </Notice>
              </div>
            ) : null}
            {embeddingReady && status.staleAssetCount > 0 ? (
              <div className="mt-3">
                <Notice tone="warn">
                  检测到 {status.staleAssetCount} 个过期向量（嵌入模型可能已切换），建议重建后再进行语义召回。
                </Notice>
              </div>
            ) : null}
          </>
        ) : null}

        {embeddingReady ? (
          <div className="mt-4">
            <CollapsibleSection title="向量资产明细" subtitle="按对象类型 / 状态筛选，分页查看">
              <StockV2EmbeddingAssetList actions={actions} />
            </CollapsibleSection>
          </div>
        ) : null}
      </div>

      {showRebuild ? (
        <StockV2EmbeddingRebuildDrawer actions={actions} onClose={() => setShowRebuild(false)} onDone={() => void load()} />
      ) : null}
    </>
  );
}

function CountCell({ label, value, tone }: { label: string; value: number; tone: "good" | "warn" | "danger" | "neutral" }) {
  const toneClass =
    tone === "good"
      ? "border-[rgba(18,132,79,0.2)] bg-[var(--good-soft)]"
      : tone === "warn"
        ? "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]"
        : tone === "danger"
          ? "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)]"
          : "border-[var(--line)] bg-[var(--surface-soft)]";
  const valueColor =
    tone === "good" ? "text-[var(--good)]" : tone === "warn" ? "text-[var(--warn)]" : tone === "danger" ? "text-[var(--danger)]" : "text-[var(--text)]";
  return (
    <div className={`rounded-lg border p-3 ${toneClass}`}>
      <span className="block text-xs text-[var(--muted)]">{label}</span>
      <strong className={`mt-1 block text-xl leading-tight ${valueColor}`}>{value}</strong>
    </div>
  );
}

// 向量资产明细：内联分页 + objectType/status 筛选。仅在 embeddingReady 时展示。
const ASSET_PAGE_SIZE = 10;
const STATUS_OPTIONS: Array<{ value: "" | StockV2EmbeddingAssetStatus; label: string }> = [
  { value: "", label: "全部状态" },
  { value: "ready", label: "就绪" },
  { value: "stale", label: "过期" },
  { value: "failed", label: "失败" },
];
const TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "", label: "全部类型" },
  { value: "stock_profile", label: "股票画像" },
  { value: "news_event", label: "新闻事件" },
  { value: "opportunity", label: "机会主题" },
  { value: "theme", label: "主题" },
  { value: "external_source", label: "外部来源" },
];

function StockV2EmbeddingAssetList({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2EmbeddingAsset[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [objectType, setObjectType] = useState("");
  const [assetStatus, setAssetStatus] = useState<"" | StockV2EmbeddingAssetStatus>("");

  const totalPages = Math.max(1, Math.ceil(total / ASSET_PAGE_SIZE));

  async function load(nextPage = page, nextType = objectType, nextStatus = assetStatus) {
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams({
        limit: String(ASSET_PAGE_SIZE),
        offset: String((Math.max(1, nextPage) - 1) * ASSET_PAGE_SIZE),
      });
      if (nextType) params.set("objectType", nextType);
      if (nextStatus) params.set("status", nextStatus);
      const res = await actions.api<{ items: StockV2EmbeddingAsset[]; total?: number }>(
        `/api/stockv2/embeddings/assets?${params}`,
      );
      setItems(res.items || []);
      setTotal(res.total ?? res.items?.length ?? 0);
    } catch (err) {
      setError(friendlyError(err));
      setItems([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function changeType(value: string) {
    setObjectType(value);
    setPage(1);
    void load(1, value, assetStatus);
  }
  function changeStatus(value: "" | StockV2EmbeddingAssetStatus) {
    setAssetStatus(value);
    setPage(1);
    void load(1, objectType, value);
  }
  function changePage(next: number) {
    const p = Math.min(Math.max(1, next), totalPages);
    setPage(p);
    void load(p);
  }

  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={() => void load(page)}>重试</Button>
      </div>
    );
  }

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="按对象类型筛选" className="select h-9 w-32 text-xs" value={objectType} onChange={(e) => changeType(e.target.value)}>
          {TYPE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
        <select aria-label="按状态筛选" className="select h-9 w-28 text-xs" value={assetStatus} onChange={(e) => changeStatus(e.target.value as "" | StockV2EmbeddingAssetStatus)}>
          {STATUS_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
        {loading ? <span className="text-xs text-[var(--muted)]">加载中…</span> : null}
      </div>

      {items.length === 0 ? (
        <EmptyState title="暂无向量资产" body="尚未生成向量，或筛选条件下无结果。可点击上方「重建向量化」。" />
      ) : (
        <div className="grid gap-2">
          {items.map((asset) => (
            <div key={asset.id} className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                  <span className="font-mono text-[var(--muted-strong)]">{asset.objectType}</span>
                  <span className="ml-2 break-words text-[var(--text)]">{asset.textSummary || asset.objectId}</span>
                </div>
                <Pill tone={stockV2EmbeddingAssetStatusTone(asset.status)}>{stockV2EmbeddingAssetStatusLabel(asset.status)}</Pill>
              </div>
              <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[var(--muted)]">
                <span>model {asset.modelId?.slice(0, 8) || "-"}</span>
                <span>{asset.embeddingDimensions}d</span>
                <span className="font-mono">{asset.embeddingProtocol}</span>
                {asset.errorMessage ? <span className="text-[var(--danger)]">{asset.errorMessage}</span> : null}
              </div>
            </div>
          ))}
          <SimplePager loading={loading} page={page} pageSize={ASSET_PAGE_SIZE} total={total} totalPages={totalPages} onPage={changePage} />
        </div>
      )}
    </div>
  );
}

// 轻量分页：上一页 / 下一页 / 页码 / 跳页 select。供本页资产列表使用。
export function SimplePager({
  loading,
  page,
  pageSize,
  total,
  totalPages,
  onPage,
}: {
  loading: boolean;
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  onPage: (page: number) => void;
}) {
  if (total <= pageSize) {
    return (
      <div className="mt-2 text-xs text-[var(--muted)]">共 {total} 条</div>
    );
  }
  const start = (page - 1) * pageSize + 1;
  const end = Math.min(total, page * pageSize);
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-3 text-xs">
      <span className="text-[var(--muted)]">
        第 {page} / {totalPages} 页 · {start}-{end} / {total}
      </span>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button disabled={loading || page <= 1} onClick={() => onPage(Math.max(1, page - 1))}>上一页</Button>
        <Button disabled={loading || page >= totalPages} onClick={() => onPage(Math.min(totalPages, page + 1))}>下一页</Button>
        <select
          aria-label="跳转到页码"
          className="select h-9 w-24 text-xs"
          disabled={loading}
          onChange={(event) => onPage(Number(event.target.value))}
          value={page}
        >
          {Array.from({ length: totalPages }, (_, idx) => idx + 1).map((item) => (
            <option key={item} value={item}>第 {item} 页</option>
          ))}
        </select>
      </div>
    </div>
  );
}
