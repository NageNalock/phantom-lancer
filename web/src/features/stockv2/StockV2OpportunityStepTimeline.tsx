import type { StockV2OpportunityDiscoveryStep } from "../../app/types";
import { stockV2DiscoveryStepLabel, stockV2DiscoveryStepStatusLabel } from "../../domain/labels";
import { DISCOVERY_STEP_ORDER } from "./StockV2OpportunityPage";

// 固定 8 步竖向 Timeline。按 DISCOVERY_STEP_ORDER 渲染，未回填的步骤显示 pending 占位。
// 图标：✓ completed / ● running / ✗ failed / ○ pending。
export function StockV2OpportunityStepTimeline({
  steps,
  selectedStepId,
  onSelect,
}: {
  steps: StockV2OpportunityDiscoveryStep[];
  selectedStepId: string | null;
  onSelect: (id: string) => void;
}) {
  const stepMap = new Map<string, StockV2OpportunityDiscoveryStep>();
  for (const s of steps) {
    if (s.stepKey) stepMap.set(s.stepKey, s);
  }

  return (
    <nav aria-label="发现步骤" className="grid gap-0.5">
      {DISCOVERY_STEP_ORDER.map((key, idx) => {
        const step = stepMap.get(key);
        const status = step?.status || "pending";
        const selected = !!step?.id && step.id === selectedStepId;
        const icon = status === "completed" ? "✓" : status === "running" ? "●" : status === "failed" ? "✗" : "○";
        const color =
          status === "completed"
            ? "text-[var(--good)]"
            : status === "running"
              ? "text-[var(--accent)]"
              : status === "failed"
                ? "text-[var(--danger)]"
                : "text-[var(--muted)]";
        return (
          <button
            type="button"
            key={key}
            disabled={!step}
            onClick={() => step && onSelect(step.id)}
            className={`flex items-start gap-2 rounded-md px-2 py-2 text-left text-xs transition ${
              selected ? "bg-[var(--surface-strong)]" : step ? "hover:bg-[var(--surface-soft)]" : "cursor-default"
            }`}
          >
            <span className={`mt-px w-4 text-center font-mono text-sm ${color}`}>{icon}</span>
            <span className="min-w-0 flex-1">
              <span className={`block ${step ? "text-[var(--text)]" : "text-[var(--muted)]"}`}>
                <span className="text-[var(--muted)]">{idx + 1}.</span> {stockV2DiscoveryStepLabel(key)}
              </span>
              <span className="text-[var(--muted)]">
                {step ? stockV2DiscoveryStepStatusLabel(status) : "待回填"}
              </span>
            </span>
          </button>
        );
      })}
    </nav>
  );
}
