import { useState } from "react";
import type { AppActions } from "../../app/App";
import type { StockV2DiscoveryStepKey } from "../../app/types";
import { EmptyState } from "../../components/ui";
import { StockV2EmbeddingStatusSection } from "./StockV2EmbeddingStatusSection";
import { StockV2OpportunityList } from "./StockV2OpportunityList";
import { StockV2OpportunityForm } from "./StockV2OpportunityForm";
import { StockV2OpportunityDetail } from "./StockV2OpportunityDetail";
import { StockV2OpportunityRunDrawer } from "./StockV2OpportunityRunDrawer";

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

// 「主题机会」Tab 顶层容器：顶部常驻 Embedding 状态区 + 左列表 / 右详情。
// 管理 selectedId / 创建 / Run Drawer 状态；候选 Drawer 由 Detail 自管。
export function StockV2OpportunityPage({ actions }: { actions: AppActions }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [runDrawerRunId, setRunDrawerRunId] = useState<string | null>(null);
  const [refreshNonce, setRefreshNonce] = useState(0);

  return (
    <div className="grid gap-4">
      <StockV2EmbeddingStatusSection actions={actions} />

      <div className="grid grid-cols-[320px_minmax(0,1fr)] gap-4 max-xl:grid-cols-1">
        <StockV2OpportunityList
          actions={actions}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onCreate={() => setShowCreate(true)}
        />
        <div className="min-w-0">
          {selectedId ? (
            <StockV2OpportunityDetail
              key={`${selectedId}-${refreshNonce}`}
              actions={actions}
              opportunityId={selectedId}
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
            setRefreshNonce((n) => n + 1);
          }}
        />
      ) : null}
    </div>
  );
}
