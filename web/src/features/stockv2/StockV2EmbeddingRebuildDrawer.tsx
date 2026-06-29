import { useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2EmbeddingObjectType,
  StockV2EmbeddingRebuildRequest,
  StockV2EmbeddingRebuildResult,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice } from "../../components/ui";
import { CheckLabel } from "../../components/ui";

// 向量资产维护 Drawer：勾选对象类型 + 数量上限，POST /embeddings/rebuild。
// 仅在嵌入模型已绑定且可用时由父组件触发；本组件不做 embedding 计算，只调后端。
const OBJECT_TYPES: Array<{ value: StockV2EmbeddingObjectType; label: string }> = [
  { value: "stock_profile", label: "股票画像" },
  { value: "news_event", label: "新闻事件" },
  { value: "opportunity", label: "机会主题" },
];

type Phase = "form" | "submitting" | "done" | "error";

export function StockV2EmbeddingRebuildDrawer({
  actions,
  onClose,
  onDone,
}: {
  actions: AppActions;
  onClose: () => void;
  onDone: () => void;
}) {
  const [selected, setSelected] = useState<StockV2EmbeddingObjectType[]>(["stock_profile", "news_event"]);
  const [limit, setLimit] = useState(50);
  const [force, setForce] = useState(false);
  const [phase, setPhase] = useState<Phase>("form");
  const [result, setResult] = useState<StockV2EmbeddingRebuildResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  function toggle(type: StockV2EmbeddingObjectType) {
    setSelected((prev) => (prev.includes(type) ? prev.filter((t) => t !== type) : [...prev, type]));
  }

  async function submit() {
    setPhase("submitting");
    setError(null);
    try {
      const req: StockV2EmbeddingRebuildRequest = {
        objectTypes: selected.length ? selected : undefined,
        limit: limit > 0 ? limit : undefined,
        force,
      };
      const res = await actions.api<StockV2EmbeddingRebuildResult>("/api/stockv2/embeddings/rebuild", {
        method: "POST",
        body: req,
      });
      setResult(res);
      setPhase("done");
      actions.setToast(`维护完成：成功 ${res.success}/${res.total}`, res.failed > 0 ? "warn" : "good");
      onDone();
    } catch (err) {
      setError(friendlyError(err));
      setPhase("error");
      actions.setToast(friendlyError(err), "danger");
    }
  }

  return (
    <Drawer title="维护向量化资产" subtitle="处理待生成、过期、失败和内容变化的 embedding" onClose={onClose} width={480}>
      <div className="grid gap-4">
        {phase === "error" && error ? <Notice tone="danger">维护失败：{error}</Notice> : null}

        {phase === "done" && result ? (
          <div className="grid gap-3">
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
              <strong>维护完成</strong>
              <div className="mt-2 grid grid-cols-3 gap-2 text-center text-xs">
                <ResultCell label="总数" value={result.total} />
                <ResultCell label="成功" value={result.success} tone="good" />
                <ResultCell label="失败" value={result.failed} tone="danger" />
              </div>
              {result.skipped ? <p className="mt-2 text-xs text-[var(--muted)]">跳过 {result.skipped} 条空文本或无需处理的资产。</p> : null}
            </div>
            {result.failedItems?.length ? (
              <details className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]">
                <summary className="cursor-pointer px-3 py-2 text-sm font-medium">失败明细（{result.failedItems.length}）</summary>
                <pre className="max-h-48 overflow-auto border-t border-[var(--line)] px-3 py-2 text-xs text-[var(--muted-strong)]">
                  {JSON.stringify(result.failedItems, null, 2)}
                </pre>
              </details>
            ) : null}
            <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
              <Button tone="primary" onClick={onClose}>关闭</Button>
            </div>
          </div>
        ) : (
          <>
            <Notice tone="warn">维护会调用已绑定的嵌入模型并占用额度；force 关闭时只处理 missing、stale、failed 和 hash 变化的资产，不重算 unchanged ready。</Notice>
            <Field label="对象类型" help="不勾选则处理全部可维护类型">
              <div className="flex flex-wrap gap-3">
                {OBJECT_TYPES.map((t) => (
                  <CheckLabel key={t.value} checked={selected.includes(t.value)} onChange={() => toggle(t.value)}>
                    {t.label}
                  </CheckLabel>
                ))}
              </div>
            </Field>
            <Field label="数量上限" help="本轮最多处理多少条需要工作的资产；后端上限为 200，0 或留空使用默认值">
              <input
                type="number"
                min={0}
                max={200}
                value={limit}
                onChange={(e) => setLimit(Number(e.target.value) || 0)}
              />
            </Field>
            <CheckLabel checked={force} onChange={() => setForce((value) => !value)}>
              force：重算 unchanged ready 资产
            </CheckLabel>
            <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
              <Button onClick={onClose}>取消</Button>
              <Button tone="primary" disabled={phase === "submitting"} onClick={() => void submit()}>
                {phase === "submitting" ? "维护中…" : "开始维护"}
              </Button>
            </div>
          </>
        )}
      </div>
    </Drawer>
  );
}

function ResultCell({ label, value, tone }: { label: string; value: number; tone?: "good" | "danger" }) {
  const color = tone === "good" ? "text-[var(--good)]" : tone === "danger" ? "text-[var(--danger)]" : "text-[var(--text)]";
  return (
    <div className="rounded border border-[var(--line)] bg-[var(--surface)] p-2">
      <div className="text-[var(--muted)]">{label}</div>
      <div className={`text-base font-semibold ${color}`}>{value}</div>
    </div>
  );
}
