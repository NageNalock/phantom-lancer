import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentCreateModelRequest,
  StockV2AgentModelProfile,
  StockV2AgentModelTestResult,
  StockV2AgentProviderModelCatalog,
  StockV2AgentProviderModelCatalogItem,
  StockV2AgentProviderProfile,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice, Toggle } from "../../components/ui";

export function StockV2AgentModelDrawer({
  model,
  providers,
  onClose,
  onSaved,
  actions,
}: {
  model: StockV2AgentModelProfile | null; // null = 新建
  providers: StockV2AgentProviderProfile[];
  onClose: () => void;
  onSaved?: () => void;
  actions: AppActions;
}) {
  const isEdit = model != null;
  const [form, setForm] = useState<StockV2AgentCreateModelRequest>({
    providerId: model?.providerId || providers[0]?.id || "",
    modelName: model?.modelName || "",
    displayName: model?.displayName || "",
    enabled: model?.enabled ?? true,
    contextLimit: model?.contextLimit,
  });
  const [catalog, setCatalog] = useState<StockV2AgentProviderModelCatalogItem[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [testLoading, setTestLoading] = useState(false);
  const [testResult, setTestResult] = useState<StockV2AgentModelTestResult | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (model) {
      setForm({
        providerId: model.providerId,
        modelName: model.modelName,
        displayName: model.displayName || "",
        enabled: model.enabled,
        contextLimit: model.contextLimit,
      });
    }
  }, [model]);

  useEffect(() => {
    if (!isEdit && !form.providerId && providers[0]?.id) {
      setForm((prev) => ({ ...prev, providerId: providers[0].id }));
    }
  }, [form.providerId, isEdit, providers]);

  const canSubmit = !!form.providerId && !!form.modelName.trim();

  async function handleSubmit() {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const body: StockV2AgentCreateModelRequest = {
        providerId: form.providerId,
        modelName: form.modelName.trim(),
        displayName: form.displayName?.trim(),
        enabled: form.enabled,
        contextLimit: form.contextLimit,
      };
      if (isEdit) {
        await actions.api(`/api/stockv2/agent/models/${model.id}`, { method: "PUT", body });
      } else {
        await actions.api("/api/stockv2/agent/models", { method: "POST", body });
      }
      actions.setToast(isEdit ? "Model 已更新" : "Model 已创建", "good");
      onSaved?.();
      onClose();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function fetchProviderModels() {
    if (!form.providerId) return;
    setCatalogLoading(true);
    setCatalogError(null);
    setTestResult(null);
    try {
      const res = await actions.api<StockV2AgentProviderModelCatalog>(
        `/api/stockv2/agent/providers/${form.providerId}/models`,
      );
      setCatalog(res.items || []);
      actions.setToast(`已获取 ${res.items?.length || 0} 个模型`, "good");
    } catch (err) {
      setCatalog([]);
      setCatalogError(friendlyError(err));
    } finally {
      setCatalogLoading(false);
    }
  }

  async function testModel() {
    if (!canSubmit) return;
    setTestLoading(true);
    setTestResult(null);
    try {
      const res = await actions.api<StockV2AgentModelTestResult>("/api/stockv2/agent/models/test", {
        method: "POST",
        body: { providerId: form.providerId, modelName: form.modelName.trim() },
      });
      setTestResult(res);
      actions.setToast(res.ok ? "模型可用" : "模型测试未通过", res.ok ? "good" : "warn");
    } catch (err) {
      setTestResult({ ok: false, message: friendlyError(err) });
    } finally {
      setTestLoading(false);
    }
  }

  function selectCatalogModel(modelItem: StockV2AgentProviderModelCatalogItem) {
    setForm({
      ...form,
      modelName: modelItem.id,
      displayName: form.displayName || modelItem.displayName || modelItem.id,
    });
    setTestResult(null);
  }

  return (
    <Drawer
      title={isEdit ? "配置模型" : "新建模型"}
      subtitle={isEdit ? `ID: ${model?.id}` : "具体模型实例，绑定到 Provider"}
      onClose={onClose}
      width={480}
      footer={
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>取消</Button>
          <Button tone="primary" disabled={submitting || !canSubmit} onClick={() => void handleSubmit()}>
            {submitting ? "保存中…" : "保存"}
          </Button>
        </div>
      }
    >
      <div className="grid gap-3 text-sm">
        {error ? <Notice tone="danger">{error}</Notice> : null}

        <Field label="Provider">
          <select
            value={form.providerId}
            disabled={isEdit}
            onChange={(e) => {
              setForm({ ...form, providerId: e.target.value, modelName: "" });
              setCatalog([]);
              setCatalogError(null);
              setTestResult(null);
            }}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            {providers.length === 0 ? (
              <option value="">(无 provider)</option>
            ) : (
              providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.displayName || p.name} ({p.providerType})
                </option>
              ))
            )}
          </select>
        </Field>

        <Field label="模型名 (modelName)">
          <div className="grid gap-2">
            <div className="flex gap-2">
              <input
                value={form.modelName}
                onChange={(e) => {
                  setForm({ ...form, modelName: e.target.value });
                  setTestResult(null);
                }}
                placeholder="例如：gpt-5.5"
                className="min-w-0 flex-1 rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 font-mono text-sm"
              />
              <Button disabled={!form.providerId || catalogLoading} onClick={() => void fetchProviderModels()}>
                {catalogLoading ? "获取中…" : "获取模型列表"}
              </Button>
            </div>
            {catalogError ? <Notice tone="danger">{catalogError}</Notice> : null}
            {catalog.length > 0 ? (
              <select
                value={form.modelName}
                onChange={(e) => {
                  const selected = catalog.find((item) => item.id === e.target.value);
                  if (selected) selectCatalogModel(selected);
                }}
                className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
              >
                <option value="">从服务商模型列表选择</option>
                {catalog.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.displayName || item.id}
                    {item.source === "codex_cli_bundled" ? " (bundled)" : ""}
                  </option>
                ))}
              </select>
            ) : null}
          </div>
        </Field>

        <Field label="显示名">
          <input
            value={form.displayName || ""}
            onChange={(e) => setForm({ ...form, displayName: e.target.value })}
            placeholder="例如：主审查模型"
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          />
        </Field>

        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <Field label="上下文长度">
            <input
              type="number"
              value={form.contextLimit ?? ""}
              onChange={(e) =>
                setForm({
                  ...form,
                  contextLimit: e.target.value ? Number(e.target.value) : undefined,
                })
              }
              placeholder="如 200000"
              className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
            />
          </Field>
          <div className="flex items-end">
            <Button disabled={!canSubmit || testLoading} onClick={() => void testModel()}>
              {testLoading ? "测试中…" : "测试模型"}
            </Button>
          </div>
        </div>

        {testResult ? (
          testResult.ok ? (
            <div className="rounded-lg border border-[rgba(18,132,79,0.22)] bg-[var(--good-soft)] p-3 text-sm text-[var(--good)]">
              模型可用
              {testResult.latencyMs ? ` · ${testResult.latencyMs} ms` : ""}
              {testResult.message ? ` · ${testResult.message}` : ""}
            </div>
          ) : (
            <Notice tone="warn">
              模型测试未通过
              {testResult.latencyMs ? ` · ${testResult.latencyMs} ms` : ""}
              {testResult.message ? ` · ${testResult.message}` : ""}
            </Notice>
          )
        ) : null}

        <div className="grid grid-cols-1 gap-2">
          <Toggle
            checked={form.enabled ?? true}
            label="启用"
            onChange={(v) => setForm({ ...form, enabled: v })}
          />
        </div>
      </div>
    </Drawer>
  );
}
