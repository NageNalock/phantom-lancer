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
import { stockV2AgentModelStatusLabel } from "../../domain/labels";

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
  const cliOnly = taskType === "portfolio_sentinel";
  const [form, setForm] = useState<StockV2AgentUpdateTaskProfileRequest>({
    executionMode: cliOnly ? "cli" : profile?.executionMode || "cli",
    primaryModelId: profile?.primaryModelId || "",
    fallbackModelId: profile?.fallbackModelId || "",
    reasoningEffort: profile?.reasoningEffort || "",
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (profile) {
      setForm({
        executionMode: cliOnly ? "cli" : profile.executionMode || "cli",
        primaryModelId: profile.primaryModelId || "",
        fallbackModelId: cliOnly ? "" : profile.fallbackModelId || "",
        reasoningEffort: profile.reasoningEffort || "",
      });
    }
  }, [cliOnly, profile]);

  const providerByID = new Map(providers.map((provider) => [provider.id, provider]));
  const executionMode = cliOnly ? "cli" : form.executionMode === "api" ? "api" : "cli";
  const compatibleModels = models.filter((m) => {
    if ((m.modelType || "chat") !== "chat") return false;
    const provider = providerByID.get(m.providerId);
    if (!provider) return false;
    return executionMode === "cli" ? provider.providerType === "codex_cli" : provider.providerType !== "codex_cli";
  });
  const enabledModels = compatibleModels.filter((model) => model.enabled && model.status === "available");
  const modelOptionLabel = (model: StockV2AgentModelProfile) => {
    const name = model.displayName || model.modelName;
    if (!model.enabled) return `${name} (未启用)`;
    if (model.status !== "available") return `${name} (${stockV2AgentModelStatusLabel(model.status)})`;
    return name;
  };

  async function handleSubmit() {
    setSubmitting(true);
    setError(null);
    try {
      const usableModelIds = new Set(enabledModels.map((model) => model.id));
      const body: StockV2AgentUpdateTaskProfileRequest = {
        executionMode,
        primaryModelId: form.primaryModelId && usableModelIds.has(form.primaryModelId) ? form.primaryModelId : "",
        fallbackModelId: !cliOnly && form.fallbackModelId && usableModelIds.has(form.fallbackModelId) ? form.fallbackModelId : "",
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
        {cliOnly ? (
          <Notice>
            组合哨兵持仓必须使用 Codex CLI。任务会启用实时 web search，并可使用 CLI/MCP/Agent 侧检索能力；API Provider 不具备这条能力，因此不参与绑定或降级。
          </Notice>
        ) : null}
        {enabledModels.length === 0 ? (
          <Notice tone="warn">当前执行模式下暂无可用对话模型。CLI 仅使用内置 Codex Provider；API 使用 OpenAI-compatible Provider。</Notice>
        ) : null}
        {compatibleModels.length > enabledModels.length ? (
          <p className="m-0 text-xs text-[var(--muted)]">不可用模型保留显示并注明状态，但不能保存为任务绑定。</p>
        ) : null}

        <Field
          label="执行模式"
          help={cliOnly ? "组合哨兵持仓固定使用本机 Codex CLI 与实时搜索，不能切换为 API" : "CLI 使用本机 Codex 会话与 MCP；API 直接请求所选 Provider，并在服务内完成函数调用循环"}
        >
          <select
            value={executionMode}
            disabled={cliOnly}
            onChange={(e) => setForm({ ...form, executionMode: e.target.value, primaryModelId: "", fallbackModelId: "" })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="cli">CLI · Codex 登录态</option>
            {!cliOnly ? <option value="api">API · OpenAI-compatible</option> : null}
          </select>
        </Field>

        <Field label="主模型" help="优先调用的模型，必须已启用且测试可用">
          <select
            value={form.primaryModelId || ""}
            onChange={(e) => setForm({ ...form, primaryModelId: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="">(未绑定)</option>
            {compatibleModels.map((m) => (
              <option disabled={!m.enabled || m.status !== "available"} key={m.id} value={m.id}>
                {modelOptionLabel(m)}
              </option>
            ))}
          </select>
        </Field>

        {!cliOnly ? (
          <Field label="备模型" help="主模型失败时降级调用">
            <select
              value={form.fallbackModelId || ""}
              onChange={(e) => setForm({ ...form, fallbackModelId: e.target.value })}
              className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
            >
              <option value="">(无)</option>
              {compatibleModels.map((m) => (
                <option disabled={!m.enabled || m.status !== "available"} key={m.id} value={m.id}>
                  {modelOptionLabel(m)}
                </option>
              ))}
            </select>
          </Field>
        ) : null}

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
