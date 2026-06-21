import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentModelProfile,
  StockV2AgentTaskProfile,
  StockV2AgentUpdateTaskProfileRequest,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice } from "../../components/ui";

export function StockV2AgentTaskProfileDrawer({
  profile,
  models,
  taskType,
  taskLabel,
  onClose,
  onSaved,
  actions,
}: {
  profile: StockV2AgentTaskProfile | null;
  models: StockV2AgentModelProfile[];
  taskType: string;
  taskLabel: string;
  onClose: () => void;
  onSaved?: () => void;
  actions: AppActions;
}) {
  const [form, setForm] = useState<StockV2AgentUpdateTaskProfileRequest>({
    primaryModelId: profile?.primaryModelId || "",
    fallbackModelId: profile?.fallbackModelId || "",
    maxBudget: profile?.maxBudget,
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (profile) {
      setForm({
        primaryModelId: profile.primaryModelId || "",
        fallbackModelId: profile.fallbackModelId || "",
        maxBudget: profile.maxBudget,
      });
    }
  }, [profile]);

  const enabledModels = models.filter((m) => m.enabled && m.status === "available");

  async function handleSubmit() {
    setSubmitting(true);
    setError(null);
    try {
      const usableModelIds = new Set(enabledModels.map((model) => model.id));
      const body: StockV2AgentUpdateTaskProfileRequest = {
        primaryModelId: form.primaryModelId && usableModelIds.has(form.primaryModelId) ? form.primaryModelId : "",
        fallbackModelId: form.fallbackModelId && usableModelIds.has(form.fallbackModelId) ? form.fallbackModelId : "",
        maxBudget: form.maxBudget,
      };
      await actions.api(`/api/stockv2/agent/task-profiles/${taskType}`, { method: "PUT", body });
      actions.setToast("任务配置已更新", "good");
      onSaved?.();
      onClose();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Drawer
      title="编辑任务配置"
      subtitle={taskLabel}
      onClose={onClose}
      width={440}
      footer={
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>取消</Button>
          <Button tone="primary" disabled={submitting || enabledModels.length === 0} onClick={() => void handleSubmit()}>
            {submitting ? "保存中…" : "保存"}
          </Button>
        </div>
      }
    >
      <div className="grid gap-3 text-sm">
        {error ? <Notice tone="danger">{error}</Notice> : null}
        {enabledModels.length === 0 ? (
          <Notice tone="warn">暂无可用于任务绑定的模型。请先创建模型,并确保它已启用且状态为可用。</Notice>
        ) : null}

        <Field label="主模型" help="优先调用的模型，必须已启用">
          <select
            value={form.primaryModelId || ""}
            onChange={(e) => setForm({ ...form, primaryModelId: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="">(未绑定)</option>
            {enabledModels.map((m) => (
              <option key={m.id} value={m.id}>
                {m.displayName || m.modelName}
              </option>
            ))}
          </select>
        </Field>

        <Field label="备模型" help="主模型失败时降级调用">
          <select
            value={form.fallbackModelId || ""}
            onChange={(e) => setForm({ ...form, fallbackModelId: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="">(无)</option>
            {enabledModels.map((m) => (
              <option key={m.id} value={m.id}>
                {m.displayName || m.modelName}
              </option>
            ))}
          </select>
        </Field>

        <Field label="预算上限" help="每次运行的成本上限 (token 或金额，依 provider 定义)">
          <input
            type="number"
            value={form.maxBudget ?? ""}
            onChange={(e) =>
              setForm({
                ...form,
                maxBudget: e.target.value ? Number(e.target.value) : undefined,
              })
            }
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          />
        </Field>
      </div>
    </Drawer>
  );
}
