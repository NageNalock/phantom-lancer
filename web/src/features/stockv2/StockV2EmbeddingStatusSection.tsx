import { useEffect, useState } from "react";
import { ArrowClockwise, Database, GearSix, Hammer, WarningCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { StockV2EmbeddingAsset, StockV2EmbeddingAssetBreakdown, StockV2EmbeddingAssetStatus, StockV2EmbeddingStatus } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, EmptyState, Notice, Pill } from "../../components/ui";
import {
  stockV2EmbeddingAssetStatusLabel,
  stockV2EmbeddingAssetStatusTone,
  stockV2EmbeddingErrorCodeLabel,
} from "../../domain/labels";
import { StockV2EmbeddingRebuildDrawer } from "./StockV2EmbeddingRebuildDrawer";
import { StockV2EmbeddingBindDrawer } from "./StockV2EmbeddingBindDrawer";

// Embedding 状态区（顶部常驻 Panel，独立加载 /embeddings/status）。
// 它是「主题机会」页语义召回能力的前提状态，与 opportunity 列表解耦：
// 即便 opportunity 相关后端 404，本区仍展示真实的模型绑定与向量资产状态。
// 拦截逻辑只读 status.available，不在前端做 embedding 计算。
export function StockV2EmbeddingStatusSection({ actions }: { actions: AppActions }) {
  const [status, setStatus] = useState<StockV2EmbeddingStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showRebuild, setShowRebuild] = useState(false);
  const [showBind, setShowBind] = useState(false);

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

  // 三态切分：重建只需要「模型已绑定且可用」；语义召回才需要「资产也就绪」。
  // 否则「资产未就绪 → 禁用重建 → 无法生成资产」会变成死循环。
  const errorCode = status?.errorCode;
  const modelNotConfigured = errorCode === "embedding_model_not_configured";
  const modelNotReady =
    modelNotConfigured || errorCode === "embedding_model_unavailable";
  const modelReady = !!status && !modelNotReady; // 绑定且可用（含 asset_not_ready）
  const assetsReady = !!status?.available; // 完整可用（语义召回所需）
  const modelDisabledReason = modelNotReady
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
              <Pill tone={assetsReady ? "good" : modelReady ? "warn" : "danger"}>
                {assetsReady ? "可用" : modelReady ? "待重建" : "不可用"}
              </Pill>
            ) : (
              <Pill tone="warn">状态未知</Pill>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={() => void load()} title="重新拉取状态">
              <ArrowClockwise size={14} className="mr-1.5" />
              刷新
            </Button>
            <Button onClick={() => setShowBind(true)} title="绑定 / 切换嵌入模型">
              <GearSix size={14} className="mr-1.5" />
              绑定模型
            </Button>
            <Button
              tone="primary"
              disabled={!modelReady}
              title={modelReady ? "运行一轮向量资产维护" : modelDisabledReason || ""}
              onClick={() => setShowRebuild(true)}
            >
              <Hammer size={14} className="mr-1.5" />
              维护向量化
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
              <span>
                维护：<strong className="text-[var(--text)]">{status.maintenance?.enabled ? (status.maintenance.running ? "运行中" : "已开启") : "关闭"}</strong>
              </span>
              {status.maintenance?.lastRunAt ? <span>上次：{formatEmbeddingTime(status.maintenance.lastRunAt)}</span> : null}
              {status.maintenance?.nextRunAt ? <span>下次：{formatEmbeddingTime(status.maintenance.nextRunAt)}</span> : null}
            </div>

            {modelNotConfigured ? (
              <div className="mt-3 rounded-lg border border-[rgba(207,31,50,0.28)] bg-[var(--danger-soft)] p-3 text-sm text-[var(--danger)]">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex min-w-0 gap-2">
                    <WarningCircle size={18} weight="fill" className="mt-0.5 shrink-0" />
                    <div className="min-w-0">
                      <strong className="block text-sm">未接入 Embedding 模型，很多信息面能力不可用</strong>
                      <p className="mt-1 mb-0 leading-relaxed">
                        语义召回、新闻语义关联、机会发现和策略生成里的语义补召回、向量资产自动维护都会降级或停止；关键词搜索和普通项目数据查询仍可使用。
                      </p>
                    </div>
                  </div>
                  <Button className="shrink-0" onClick={() => setShowBind(true)}>
                    接入模型
                  </Button>
                </div>
              </div>
            ) : null}

            <div className="mt-3 grid grid-cols-4 gap-2 max-sm:grid-cols-1">
              <CountCell label="就绪向量" value={status.readyAssetCount ?? 0} tone="good" />
              <CountCell label="待生成" value={status.missingAssetCount ?? 0} tone={(status.missingAssetCount ?? 0) > 0 ? "warn" : "neutral"} />
              <CountCell label="过期向量" value={status.staleAssetCount ?? 0} tone={(status.staleAssetCount ?? 0) > 0 ? "warn" : "neutral"} />
              <CountCell label="失败向量" value={status.failedAssetCount ?? 0} tone={(status.failedAssetCount ?? 0) > 0 ? "danger" : "neutral"} />
            </div>

            {status.assetBreakdown?.length ? (
              <EmbeddingAssetBreakdownTable items={status.assetBreakdown} />
            ) : null}

            {status.maintenance?.lastResult ? (
              <div className="mt-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs text-[var(--muted-strong)]">
                最近维护：<span className="font-mono text-[var(--text)]">{status.maintenance.lastResult}</span>
              </div>
            ) : null}

            {modelNotReady && modelDisabledReason ? (
              <div className="mt-3">
                <Notice tone="danger">
                  语义向量召回与重建已禁用：{modelDisabledReason}。请先「绑定模型」并启用。关键词搜索、项目内资料查询与外部搜索仍可独立使用，但不会标记为向量召回。
                </Notice>
              </div>
            ) : null}
            {modelReady && !assetsReady ? (
              <div className="mt-3">
                <Notice tone="warn">
                  已绑定嵌入模型，但尚无就绪向量资产（{stockV2EmbeddingErrorCodeLabel(errorCode)}）。语义召回暂不可用，请点击「维护向量化」处理待维护资产。关键词搜索不受影响。
                </Notice>
              </div>
            ) : null}
            {assetsReady && ((status.missingAssetCount ?? 0) > 0 || (status.staleAssetCount ?? 0) > 0) ? (
              <div className="mt-3">
                <Notice tone="warn">
                  检测到 {status.missingAssetCount ?? 0} 个待生成、{status.staleAssetCount ?? 0} 个过期向量。维护批次会优先处理 missing、stale、failed 和 hash 变化的资产，不会卡在 unchanged ready 资产上。
                </Notice>
              </div>
            ) : null}
          </>
        ) : null}

        {modelReady ? (
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
      {showBind ? (
        <StockV2EmbeddingBindDrawer
          actions={actions}
          status={status}
          onClose={() => setShowBind(false)}
          onDone={() => void load()}
        />
      ) : null}
    </>
  );
}

function formatEmbeddingTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function EmbeddingAssetBreakdownTable({ items }: { items: StockV2EmbeddingAssetBreakdown[] }) {
  return (
    <div className="mt-3 overflow-hidden rounded-md border border-[var(--line)]">
      <table className="w-full text-sm">
        <thead className="bg-[var(--surface-soft)] text-xs text-[var(--muted)]">
          <tr>
            <th className="px-3 py-2 text-left font-medium">分类</th>
            <th className="px-3 py-2 text-right font-medium">就绪</th>
            <th className="px-3 py-2 text-right font-medium">待生成</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-[var(--line)]">
          {items.map((item) => (
            <tr key={item.category}>
              <td className="px-3 py-2 text-[var(--text)]">{embeddingBreakdownLabel(item.category)}</td>
              <td className="px-3 py-2 text-right font-mono text-[var(--good)]">{item.readyAssetCount ?? 0}</td>
              <td className="px-3 py-2 text-right font-mono text-[var(--warn)]">{item.missingAssetCount ?? 0}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function embeddingBreakdownLabel(category: string): string {
  switch (category) {
    case "stock_profile":
      return "股票画像";
    case "news_event":
      return "新闻事件";
    case "other":
      return "其他";
    default:
      return category;
  }
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

// 向量资产明细：内联分页 + objectType/status 筛选。模型可用时展示，资产未就绪也可查看空态。
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
      const nextItems = res.items || [];
      const nextTotal = res.total ?? nextItems.length;
      const nextTotalPages = Math.max(1, Math.ceil(nextTotal / ASSET_PAGE_SIZE));
      setTotal(nextTotal);
      if (nextPage > nextTotalPages) {
        setItems([]);
        setPage(nextTotalPages);
        return;
      }
      setItems(nextItems);
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
        <EmptyState title="暂无向量资产" body="尚未生成向量，或筛选条件下无结果。可点击上方「维护向量化」。" />
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
