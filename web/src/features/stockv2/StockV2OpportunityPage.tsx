import { useState } from "react";
import type { AppActions } from "../../app/App";
import type { StockV2DiscoveryStepKey } from "../../app/types";
import { EmptyState, SubTabs } from "../../components/ui";
import { buildQueryHref, useQueryParamState } from "../../hooks/useQueryParamState";
import { StockV2EmbeddingAvailabilityNotice } from "./StockV2EmbeddingStatusSection";
import { StockV2OpportunityList } from "./StockV2OpportunityList";
import { StockV2OpportunityForm } from "./StockV2OpportunityForm";
import { StockV2OpportunityDetail } from "./StockV2OpportunityDetail";
import { StockV2OpportunityRunDrawer } from "./StockV2OpportunityRunDrawer";
import { StockV2OpportunityMarketScan } from "./StockV2OpportunityMarketScan";

// 固定 8 步顺序，对齐设计文档 §4.3。StepTimeline / RunDrawer 共享。
export const DISCOVERY_STEP_ORDER: StockV2DiscoveryStepKey[] = [
  "understand_theme",
  "internal_recall",
  "external_research",
  "theme_chain",
  "candidate_merge",
  "market_risk_check",
  "candidate_ranking",
  "final_report",
];

const OPPORTUNITY_VIEWS = ["marketScan", "themeResearch"] as const;

// 机会发现按市场扫描与手工主题研究分层，避免全市场任务状态挤占主题工作区。
export function StockV2OpportunityPage({ actions }: { actions: AppActions }) {
  const [view, setView, viewHref] = useQueryParamState("stockv2OpportunityView", OPPORTUNITY_VIEWS, "marketScan");

  return (
    <div className="grid gap-4">
      <SubTabs
        activeId={view}
        ariaLabel="机会发现视图"
        onChange={(id) => setView(id as typeof view)}
        tabs={[
          { id: "marketScan", label: "市场扫描", href: viewHref("marketScan") },
          { id: "themeResearch", label: "主题研究", href: viewHref("themeResearch") },
        ]}
      />
      {view === "marketScan" ? <StockV2OpportunityMarketScan actions={actions} /> : <StockV2ThemeOpportunityResearch actions={actions} />}
    </div>
  );
}

function StockV2ThemeOpportunityResearch({ actions }: { actions: AppActions }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [runDrawerRunId, setRunDrawerRunId] = useState<string | null>(null);
  const [detailRefreshToken, setDetailRefreshToken] = useState(0);
  const semanticRecallHref = buildQueryHref({ tab: "stockv2", stockv2: "agent", stockv2AgentView: "semanticRecall" });

  return (
    <div className="grid gap-4">
      <StockV2EmbeddingAvailabilityNotice actions={actions} manageHref={semanticRecallHref} />

      <div className="grid min-w-0 grid-cols-[minmax(260px,300px)_minmax(0,1fr)] gap-4 max-xl:grid-cols-1">
        <StockV2OpportunityList
          actions={actions}
          selectedId={selectedId}
          onSelect={(id) => {
            setSelectedId(id);
            setDetailRefreshToken(0);
          }}
          onCreate={() => setShowCreate(true)}
        />
        <div className="min-w-0">
          {selectedId ? (
            <StockV2OpportunityDetail
              actions={actions}
              opportunityId={selectedId}
              refreshToken={detailRefreshToken}
              onOpenRun={(runId) => setRunDrawerRunId(runId)}
            />
          ) : (
            <EmptyState
              title="选择一个主题机会"
              body="从左侧选择机会查看详情、候选池与运行过程，或新建一个主题机会启动发现。"
            />
          )}
        </div>
      </div>

      {showCreate ? (
        <StockV2OpportunityForm
          actions={actions}
          onClose={() => setShowCreate(false)}
          onCreated={(opp) => {
            setSelectedId(opp.id);
            setShowCreate(false);
          }}
        />
      ) : null}
      {runDrawerRunId ? (
        <StockV2OpportunityRunDrawer
          actions={actions}
          runId={runDrawerRunId}
          onClose={() => {
            setRunDrawerRunId(null);
            setDetailRefreshToken((value) => value + 1);
          }}
        />
      ) : null}
    </div>
  );
}
