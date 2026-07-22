import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentModelProfile,
  StockV2AgentProviderProfile,
  StockV2AgentTaskProfile,
  StockV2AgentUpdateTaskProfileRequest,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice } from "../../components/ui";

export function StockV2AgentTaskProfileDrawer({
  profile,
  models,
  providers,
  taskType,
  taskLabel,
  onClose,
  onSaved,
  actions,
}: {
  profile: StockV2AgentTaskProfile | null;
  models: StockV2AgentModelProfile[];
  providers: StockV2AgentProviderProfile[];
  taskType: string;
  taskLabel: string;
  onClose: () => void;
  onSaved?: () => void;
  actions: AppActions;
}) {
  const [form, setForm] = useState<StockV2AgentUpdateTaskProfileRequest>({
    executionMode: profile?.executionMode || "cli",
    primaryModelId: profile?.primaryModelId || "",
    fallbackModelId: profile?.fallbackModelId || "",
    reasoningEffort: profile?.reasoningEffort || "",
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (profile) {
      setForm({
        executionMode: profile.executionMode || "cli",
        primaryModelId: profile.primaryModelId || "",
        fallbackModelId: profile.fallbackModelId || "",
        reasoningEffort: profile.reasoningEffort || "",
      });
    }
  }, [profile]);

  const providerByID = new Map(providers.map((provider) => [provider.id, provider]));
  const executionMode = form.executionMode === "api" ? "api" : "cli";
  const enabledModels = models.filter((m) => {
    if (!m.enabled || m.status !== "available" || (m.modelType || "chat") !== "chat") return false;
    const provider = providerByID.get(m.providerId);
    return executionMode === "cli" ? provider?.providerType === "codex_cli" : provider?.providerType !== "codex_cli";
  });

  async function handleSubmit() {
    setSubmitting(true);
    setError(null);
    try {
      const usableModelIds = new Set(enabledModels.map((model) => model.id));
      const body: StockV2AgentUpdateTaskProfileRequest = {
        executionMode,
        primaryModelId: form.primaryModelId && usableModelIds.has(form.primaryModelId) ? form.primaryModelId : "",
        fallbackModelId: form.fallbackModelId && usableModelIds.has(form.fallbackModelId) ? form.fallbackModelId : "",
        reasoningEffort: form.reasoningEffort || "",
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
          <Notice tone="warn">当前执行模式下暂无可用对话模型。CLI 仅使用内置 Codex Provider；API 使用 OpenAI-compatible Provider。</Notice>
        ) : null}

        <Field label="执行模式" help="CLI 使用本机 Codex 会话与 MCP；API 直接请求所选 Provider，并在服务内完成函数调用循环">
          <select
            value={executionMode}
            onChange={(e) => setForm({ ...form, executionMode: e.target.value, primaryModelId: "", fallbackModelId: "" })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="cli">CLI · Codex 登录态</option>
            <option value="api">API · OpenAI-compatible</option>
          </select>
        </Field>

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

        <Field
          label="模型推理强度"
          help={
            executionMode === "api"
              ? "留空时不传参数，沿用模型默认值；DeepSeek 中 low 关闭思考，medium/high 使用 high，xhigh/max 使用 max"
              : "留空时不传参数，沿用模型默认值；Codex CLI 决定支持范围"
          }
        >
          <select
            value={form.reasoningEffort || ""}
            onChange={(e) => setForm({ ...form, reasoningEffort: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="">模型默认（不传）</option>
            <option value="low">low · 较低</option>
            <option value="medium">medium · 中等</option>
            <option value="high">high · 较高</option>
            <option value="xhigh">xhigh · 超高</option>
            <option value="max">max · 最大</option>
            {executionMode === "cli" ? <option value="ultra">ultra · 最大并自动任务委派</option> : null}
          </select>
        </Field>
      </div>
    </Drawer>
  );
}
