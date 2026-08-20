import { useState } from "react";
import type { AppActions } from "../../app/App";
import type { StockV2OpportunityDiscoveryConfig } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Notice, Toggle } from "../../components/ui";

export function StockV2OpportunityScopeControl({
  actions,
  config,
  loading,
  error,
  onChange,
  onRetry,
}: {
  actions: AppActions;
  config: StockV2OpportunityDiscoveryConfig | null;
  loading: boolean;
  error: string | null;
  onChange: (config: StockV2OpportunityDiscoveryConfig) => void;
  onRetry: () => void;
}) {
  const [saving, setSaving] = useState(false);

  async function update(next: boolean) {
    if (!config) return;
    setSaving(true);
    try {
      const saved = await actions.api<StockV2OpportunityDiscoveryConfig>(
        "/api/stockv2/opportunity-discovery/config",
        { method: "PATCH", body: { excludeChiNextAndStarMarket: next } },
      );
      onChange(saved);
      actions.setToast(next ? "新机会研究将排除创业板和科创板个股" : "新机会研究已允许创业板和科创板个股", "good");
    } catch (cause) {
      actions.setToast(`机会发现范围保存失败：${friendlyError(cause)}`, "danger");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="grid gap-2">
      <Toggle
        checked={config?.excludeChiNextAndStarMarket ?? false}
        disabled={loading || saving || !config || Boolean(error)}
        label={(
          <span>
            <strong className="block">{saving ? "正在保存标的范围…" : "排除创业板和科创板个股"}</strong>
            <span className="text-xs text-[var(--muted)]">
              新主题研究与新策略生成不接受 300 / 301 / 688 / 689 个股；ETF、组合持仓和历史结果不受影响。市场扫描始终只扫描主板。
            </span>
          </span>
        )}
        name="opportunity_exclude_chinext_star"
        onChange={(checked) => void update(checked)}
      />
      {error ? (
        <Notice tone="warn">
          机会发现范围加载失败：{error}。<button type="button" className="ml-1 text-[var(--accent)] hover:underline" onClick={onRetry}>重试</button>
        </Notice>
      ) : null}
    </div>
  );
}
