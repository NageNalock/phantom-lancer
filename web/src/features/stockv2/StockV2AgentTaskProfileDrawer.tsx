import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  ObjectStorageProfile,
  StockV2AgentModelProfile,
  StockV2AgentProviderProfile,
  StockV2AgentTaskProfile,
  StockV2AgentUpdateTaskProfileRequest,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Toggle } from "../../components/ui";
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
  const archiveSupported = [
    "operation_review",
    "strategy_generation",
    "opportunity_discovery",
    "portfolio_sentinel",
  ].includes(taskType);
  const [form, setForm] = useState<StockV2AgentUpdateTaskProfileRequest>({
    executionMode: cliOnly ? "cli" : profile?.executionMode || "cli",
    primaryModelId: profile?.primaryModelId || "",
    fallbackModelId: profile?.fallbackModelId || "",
    reasoningEffort: profile?.reasoningEffort || "",
    archiveEnabled: profile?.archiveEnabled || false,
    archiveObjectStorageProfileId: profile?.archiveObjectStorageProfileId || "",
  });
  const [storageProfiles, setStorageProfiles] = useState<ObjectStorageProfile[]>([]);
  const [storageLoading, setStorageLoading] = useState(false);
  const [storageError, setStorageError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (profile) {
      setForm({
        executionMode: cliOnly ? "cli" : profile.executionMode || "cli",
        primaryModelId: profile.primaryModelId || "",
        fallbackModelId: profile.fallbackModelId || "",
        reasoningEffort: profile.reasoningEffort || "",
        archiveEnabled: profile.archiveEnabled || false,
        archiveObjectStorageProfileId: profile.archiveObjectStorageProfileId || "",
      });
    }
  }, [cliOnly, profile]);

  useEffect(() => {
    if (!archiveSupported) return;
    let active = true;
    setStorageLoading(true);
    setStorageError(null);
    void actions
      .api<{ items?: ObjectStorageProfile[] }>("/api/object-storage/profiles")
      .then((response) => {
        if (active) setStorageProfiles(response.items || []);
      })
      .catch((err) => {
        if (active) setStorageError(friendlyError(err));
      })
      .finally(() => {
        if (active) setStorageLoading(false);
      });
    return () => {
      active = false;
    };
  }, [actions, archiveSupported]);

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
    if (archiveSupported && form.archiveEnabled && !form.archiveObjectStorageProfileId) {
      setError("启用完整上下文归档前，请选择对象存储 Profile。");
      setSubmitting(false);
      return;
    }
    try {
      const usableModelIds = new Set(enabledModels.map((model) => model.id));
      const body: StockV2AgentUpdateTaskProfileRequest = {
        executionMode,
        primaryModelId: form.primaryModelId && usableModelIds.has(form.primaryModelId) ? form.primaryModelId : "",
        fallbackModelId: form.fallbackModelId && usableModelIds.has(form.fallbackModelId) ? form.fallbackModelId : "",
        reasoningEffort: form.reasoningEffort || "",
        archiveEnabled: archiveSupported ? Boolean(form.archiveEnabled) : false,
        archiveObjectStorageProfileId: archiveSupported ? form.archiveObjectStorageProfileId || "" : "",
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
            组合哨兵持仓必须使用 Codex CLI。内置 Provider 使用原生 web search；自定义 codex_cli Provider 使用任务级搜索
            MCP。主备模型都只能选择 codex_cli Provider，API Provider 不参与绑定或降级。
          </Notice>
        ) : null}
        {enabledModels.length === 0 ? (
          <Notice tone="warn">
            当前执行模式下暂无可用对话模型。CLI 可使用内置或自定义 codex_cli Provider；API 使用 OpenAI-compatible Provider。
          </Notice>
        ) : null}
        {compatibleModels.length > enabledModels.length ? (
          <p className="m-0 text-xs text-[var(--muted)]">不可用模型保留显示并注明状态，但不能保存为任务绑定。</p>
        ) : null}

        <Field
          label="执行模式"
          help={
            cliOnly
              ? "组合哨兵持仓固定使用本机 Codex CLI 与实时搜索，不能切换为 API"
              : "CLI 使用本机 Codex 与任务绑定的 Provider/MCP；API 直接请求所选 Provider，并在服务内完成函数调用循环"
          }
        >
          <select
            value={executionMode}
            disabled={cliOnly}
            onChange={(e) => setForm({ ...form, executionMode: e.target.value, primaryModelId: "", fallbackModelId: "" })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="cli">CLI · Codex 或自定义 Provider</option>
            {!cliOnly ? <option value="api">API · OpenAI-compatible</option> : null}
          </select>
        </Field>

        <Field label="主模型" help="优先调用的模型，必须已启用且测试可用">
          <select
            value={form.primaryModelId || ""}
            onChange={(e) =>
              setForm({
                ...form,
                primaryModelId: e.target.value,
                fallbackModelId: form.fallbackModelId === e.target.value ? "" : form.fallbackModelId,
              })
            }
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

        <Field
          label="备模型"
          help={
            cliOnly
              ? "主模型运行失败时改用另一个 Codex CLI 模型重试一次；两次执行及 Token、搜索审计都会保留"
              : "主模型失败时降级调用"
          }
        >
          <select
            value={form.fallbackModelId || ""}
            onChange={(e) => setForm({ ...form, fallbackModelId: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          >
            <option value="">(无)</option>
            {compatibleModels.map((m) => (
              <option
                disabled={!m.enabled || m.status !== "available" || m.id === form.primaryModelId}
                key={m.id}
                value={m.id}
              >
                {modelOptionLabel(m)}
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

        {archiveSupported ? (
          <CollapsibleSection
            title="高级设置"
            subtitle={form.archiveEnabled ? "完整上下文将流式归档到对象存储" : "完整上下文归档默认关闭"}
          >
            <div className="grid gap-3">
              <Toggle
                checked={Boolean(form.archiveEnabled)}
                label={
                  <span>
                    <strong className="block">归档完整 Agent 上下文</strong>
                    <span className="mt-0.5 block text-xs text-[var(--muted)]">
                      保存模型输入、输出、工具调用、校验与最终应用结果。上传失败不影响任务结果。
                    </span>
                  </span>
                }
                onChange={(checked) => setForm({ ...form, archiveEnabled: checked })}
              />
              {storageError ? <Notice tone="warn">对象存储 Profile 加载失败：{storageError}</Notice> : null}
              <Field
                label="归档对象存储 Profile"
                help="请使用私有 Bucket，并在 Bucket 侧启用默认加密和未完成 multipart 生命周期清理。系统不在本地保留副本，也不提供归档浏览入口。"
              >
                <select
                  value={form.archiveObjectStorageProfileId || ""}
                  disabled={!form.archiveEnabled || storageLoading}
                  onChange={(e) => setForm({ ...form, archiveObjectStorageProfileId: e.target.value })}
                  className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm disabled:opacity-60"
                >
                  <option value="">{storageLoading ? "加载中…" : "请选择对象存储 Profile"}</option>
                  {storageProfiles.map((item) => (
                    <option key={item.id} value={item.id} disabled={!item.hasCredentials}>
                      {item.name || item.providerLabel} · {item.bucket}
                      {item.hasCredentials ? "" : " (缺少凭据)"}
                    </option>
                  ))}
                </select>
              </Field>
              <Notice tone="warn">
                归档包含完整业务上下文，可能体积较大。队列溢出、网络中断、服务重启或对象存储异常时，本次归档会被整体放弃，不会阻塞 Agent。
              </Notice>
            </div>
          </CollapsibleSection>
        ) : null}
      </div>
    </Drawer>
  );
}
