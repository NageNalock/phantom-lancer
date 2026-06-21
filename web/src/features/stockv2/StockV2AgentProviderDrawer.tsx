import { useEffect, useState } from "react";
import { WarningCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { StockV2AgentCreateProviderRequest, StockV2AgentProviderProfile } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice } from "../../components/ui";

const providerTypeTip = [
  "codex_cli: 内置 default 使用本机 Codex CLI 登录态；手动新建项仍按 OpenAI-compatible endpoint 配置。",
  "openai: OpenAI 或 OpenAI-compatible 云服务。",
  "local: 本机或内网 OpenAI-compatible 服务。",
].join("\n");

// Provider 新建 / 编辑 Drawer。遵循 Quiet 风格：只暴露当前可用的 OpenAI-compatible 配置。
// 新建模式: provider 为 null；编辑模式: provider 为已有对象。
export function StockV2AgentProviderDrawer({
  provider,
  onClose,
  onSaved,
  actions,
}: {
  provider: StockV2AgentProviderProfile | null; // null = 新建
  onClose: () => void;
  onSaved?: () => void;
  actions: AppActions;
}) {
  const isEdit = provider != null;
  const [form, setForm] = useState<StockV2AgentCreateProviderRequest>({
    providerType: provider?.providerType || "codex_cli",
    displayName: provider?.displayName || "",
    baseUrl: provider?.baseUrl || "https://api.openai.com/v1",
  });
  const [apiKey, setApiKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (provider) {
      setForm({
        providerType: provider.providerType,
        displayName: provider.displayName || "",
        baseUrl: provider.baseUrl || "https://api.openai.com/v1",
      });
      setApiKey("");
    }
  }, [provider]);

  const hasUsableKey = isEdit ? provider?.apiKeySet || !!apiKey.trim() : !!apiKey.trim();
  const canSubmit = !!form.baseUrl?.trim() && hasUsableKey;

  async function handleSubmit() {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const body: StockV2AgentCreateProviderRequest = {
        providerType: form.providerType || "codex_cli",
        displayName: form.displayName?.trim(),
        baseUrl: form.baseUrl?.trim(),
      };
      if (apiKey.trim()) {
        body.apiKey = apiKey.trim();
      }
      if (isEdit) {
        await actions.api(`/api/stockv2/agent/providers/${provider.id}`, { method: "PUT", body });
      } else {
        await actions.api("/api/stockv2/agent/providers", { method: "POST", body });
      }
      actions.setToast(isEdit ? "Provider 已更新" : "Provider 已创建", "good");
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
      title={isEdit ? "编辑 Provider" : "新建 Provider"}
      subtitle={isEdit ? `ID: ${provider?.id}` : "Agent 调用的供应商配置"}
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

        <div className="grid gap-1">
          <div className="flex items-center gap-1.5 text-xs font-medium text-[var(--muted-strong)]">
            <span>Provider 类型</span>
            <span
              title={providerTypeTip}
              className="inline-flex h-4 w-4 items-center justify-center rounded-full border border-[var(--line)] text-[var(--muted)]"
            >
              <WarningCircle size={12} />
            </span>
          </div>
          <select
            value={form.providerType || "codex_cli"}
            disabled
            className="w-full rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1.5 text-sm text-[var(--muted-strong)]"
          >
            <option value="codex_cli">codex_cli</option>
            <option value="openai">openai</option>
            <option value="local">local</option>
          </select>
        </div>

        <Field label="显示名">
          <input
            value={form.displayName || ""}
            onChange={(e) => setForm({ ...form, displayName: e.target.value })}
            placeholder="例如：OpenAI 主账号"
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          />
        </Field>

        <Field label="OpenAI Base URL">
          <input
            value={form.baseUrl || ""}
            onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
            placeholder="https://api.openai.com/v1"
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 font-mono text-sm"
          />
        </Field>

        <Field label={provider?.apiKeySet ? "API Key / Token（留空保持原值）" : "API Key / Token"}>
          <input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={provider?.apiKeySet ? "已设置，输入新 token 可替换" : "sk-..."}
            autoComplete="off"
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5 font-mono text-sm"
          />
        </Field>

        <p className="text-xs leading-5 text-[var(--muted)]">
          内置 default Provider 不在这里编辑。手动新建 Provider 按 OpenAI-compatible 协议调用；Token 只写入本地服务数据库，响应不会回显。
        </p>
      </div>
    </Drawer>
  );
}
