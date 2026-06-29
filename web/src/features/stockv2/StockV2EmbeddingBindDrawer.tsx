import { useEffect, useState } from "react";
import { CheckCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentListResponse,
  StockV2AgentModelProfile,
  StockV2EmbeddingConfigUpdate,
  StockV2EmbeddingStatus,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, EmptyState, Field, Notice, Pill, Toggle } from "../../components/ui";

// 绑定 / 切换 StockV2 嵌入模型 Drawer。
// 列出 modelType=embedding 的 Agent 模型，单选绑定 + 启用开关，PATCH /api/stockv2/embeddings/config。
// 这是「未绑定嵌入模型」时用户解决问题的入口，因此始终可触发（不依赖当前 available）。
export function StockV2EmbeddingBindDrawer({
  actions,
  status,
  onClose,
  onDone,
}: {
  actions: AppActions;
  status: StockV2EmbeddingStatus | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const [models, setModels] = useState<StockV2AgentModelProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string>(status?.modelId || status?.config?.embeddingModelId || "");
  const [enabled, setEnabled] = useState<boolean>(status?.config?.enabled ?? true);
  const [autoMaintainEnabled, setAutoMaintainEnabled] = useState<boolean>(status?.config?.autoMaintainEnabled ?? true);
  const [maintainIntervalSeconds, setMaintainIntervalSeconds] = useState(String(status?.config?.maintainIntervalSeconds ?? 600));
  const [maintainBatchSize, setMaintainBatchSize] = useState(String(status?.config?.maintainBatchSize ?? 50));
  const [maintainRateLimitMs, setMaintainRateLimitMs] = useState(String(status?.config?.maintainRateLimitMs ?? 500));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  async function loadModels() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentModelProfile>>(
        "/api/stockv2/agent/models",
      );
      const embeddingModels = (res.items || []).filter((m) => m.modelType === "embedding");
      setModels(embeddingModels);
      // 若当前未选定，默认选第一个可绑定（enabled + available）的模型；维度可由 API 探测。
      if (!selectedId) {
        const firstUsable = embeddingModels.find((m) => isModelBindable(m));
        if (firstUsable) setSelectedId(firstUsable.id);
      }
    } catch (err) {
      setError(friendlyError(err));
      setModels([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function save() {
    if (!selectedId) return;
    setSaving(true);
    setSaveError(null);
    try {
      const update: StockV2EmbeddingConfigUpdate = {
        embeddingModelId: selectedId,
        enabled,
        autoMaintainEnabled,
        maintainIntervalSeconds: Math.max(60, Number(maintainIntervalSeconds) || 600),
        maintainBatchSize: Math.min(200, Math.max(1, Number(maintainBatchSize) || 50)),
        maintainRateLimitMs: Math.max(0, Number(maintainRateLimitMs) || 0),
      };
      await actions.api<StockV2EmbeddingStatus>("/api/stockv2/embeddings/config", {
        method: "PATCH",
        body: update,
      });
      actions.setToast("嵌入模型绑定已更新", "good");
      onDone();
      onClose();
    } catch (err) {
      setSaveError(friendlyError(err));
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSaving(false);
    }
  }

  const currentBinding = status?.modelId || status?.config?.embeddingModelId || null;
  const dirty =
    selectedId !== currentBinding ||
    enabled !== (status?.config?.enabled ?? true) ||
    autoMaintainEnabled !== (status?.config?.autoMaintainEnabled ?? true) ||
    maintainIntervalSeconds !== String(status?.config?.maintainIntervalSeconds ?? 600) ||
    maintainBatchSize !== String(status?.config?.maintainBatchSize ?? 50) ||
    maintainRateLimitMs !== String(status?.config?.maintainRateLimitMs ?? 500);
  const selectedModel = models.find((m) => m.id === selectedId) || null;
  const selectedBindable = selectedModel ? isModelBindable(selectedModel) : false;
  const anyBindable = models.some(isModelBindable);

  return (
    <Drawer title="绑定嵌入模型" subtitle="选择 StockV2 向量化使用的 embedding 模型" onClose={onClose} width={480}>
      <div className="grid gap-4">
        {error ? (
          <div className="grid gap-2">
            <Notice tone="danger">加载模型失败：{error}</Notice>
            <Button onClick={() => void loadModels()}>重试</Button>
          </div>
        ) : loading ? (
          <p className="text-sm text-[var(--muted)]">加载可用嵌入模型…</p>
        ) : models.length === 0 ? (
          <EmptyState
            title="尚无嵌入模型"
            body="请先在「Agent」页创建 modelType=embedding 的模型（含 provider 与协议），再回到这里绑定。"
          />
        ) : (
          <>
            {saveError ? <Notice tone="danger">保存失败：{saveError}</Notice> : null}

            <Notice tone="warn">
              切换嵌入模型后，旧向量维度可能不一致并标记为 stale；建议保存后立即「维护向量化」。
            </Notice>

            {!anyBindable ? (
              <Notice tone="danger">
                没有可绑定的嵌入模型：候选模型都未启用或状态不是 available。请先在「Agent」页启用 embedding 模型并置为 available。
              </Notice>
            ) : null}

            <Field label="嵌入模型" help="仅展示 modelType=embedding 的模型">
              <div className="grid gap-1.5">
                {models.map((m) => {
                  const selected = m.id === selectedId;
                  const isCurrent = m.id === currentBinding;
                  const dims = m.embeddingDimensions ?? 0;
                  const statusBad = !!m.status && m.status !== "available";
                  const noDims = dims <= 0;
                  const bindable = m.enabled && !statusBad;
                  const reason = !m.enabled ? "模型已禁用" : statusBad ? `状态：${m.status}` : null;
                  return (
                    <button
                      type="button"
                      key={m.id}
                      disabled={!bindable}
                      onClick={() => setSelectedId(m.id)}
                      className={`flex items-start gap-2 rounded-md border px-3 py-2 text-left text-xs transition ${
                        selected
                          ? "border-[var(--accent)] bg-[var(--surface-strong)]"
                          : bindable
                            ? "border-[var(--line)] bg-[var(--surface)] hover:bg-[var(--surface-soft)]"
                            : "cursor-not-allowed border-[var(--line)] bg-[var(--surface-soft)] opacity-60"
                      }`}
                    >
                      <span className={`mt-0.5 text-[var(--accent)] ${selected ? "" : "opacity-0"}`}>
                        <CheckCircle size={14} weight="fill" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex flex-wrap items-center gap-1.5">
                          <strong className="text-sm text-[var(--text)]">{m.displayName || m.modelName}</strong>
                          {isCurrent ? <Pill tone="good">当前绑定</Pill> : null}
                          {reason ? <Pill tone="danger">{reason}</Pill> : <Pill tone="good">可绑定</Pill>}
                          {bindable && noDims ? <Pill tone="neutral">维度由 API 返回</Pill> : null}
                        </span>
                        <span className="mt-0.5 block text-[var(--muted)]">
                          <span className="font-mono">{m.modelName}</span>
                          {m.embeddingProtocol ? <> · {m.embeddingProtocol}</> : null}
                          <> · {dims > 0 ? `${dims}d` : "维度待探测"}</>
                        </span>
                      </span>
                    </button>
                  );
                })}
              </div>
            </Field>

            <Toggle
              checked={enabled}
              label="启用向量化（绑定并启用后才允许生成向量与语义召回）"
              onChange={setEnabled}
            />

            <div className="grid gap-3 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <Toggle
                checked={autoMaintainEnabled}
                label="自动维护向量资产"
                onChange={setAutoMaintainEnabled}
              />
              <div className="grid grid-cols-3 gap-2">
                <Field label="周期(秒)" help="默认 600 秒。">
                  <input
                    type="number"
                    min={60}
                    step={60}
                    value={maintainIntervalSeconds}
                    onChange={(e) => setMaintainIntervalSeconds(e.target.value)}
                  />
                </Field>
                <Field label="每轮条数" help="本轮最多处理多少条需要维护的资产。">
                  <input
                    type="number"
                    min={1}
                    max={200}
                    step={1}
                    value={maintainBatchSize}
                    onChange={(e) => setMaintainBatchSize(e.target.value)}
                  />
                </Field>
                <Field label="间隔(ms)" help="每条 embedding 调用之间的等待。">
                  <input
                    type="number"
                    min={0}
                    step={100}
                    value={maintainRateLimitMs}
                    onChange={(e) => setMaintainRateLimitMs(e.target.value)}
                  />
                </Field>
              </div>
            </div>

            <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
              <Button onClick={onClose}>取消</Button>
              <Button tone="primary" disabled={!selectedId || saving || !dirty || !selectedBindable} onClick={() => void save()}>
                {saving ? "保存中…" : "保存绑定"}
              </Button>
            </div>
          </>
        )}
      </div>
    </Drawer>
  );
}

// 模型可绑定：embedding 类型 + 已启用 + 状态可用。维度可选（由 API 返回决定）。
function isModelBindable(m: StockV2AgentModelProfile): boolean {
  return m.modelType === "embedding" && m.enabled && (!m.status || m.status === "available");
}
