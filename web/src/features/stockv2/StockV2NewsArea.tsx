import type { AppActions } from "../../app/App";
import { SubTabs } from "../../components/ui";
import { useQueryParamState } from "../../hooks/useQueryParamState";
import { StockV2NewsWorkbench } from "./StockV2NewsWorkbench";
import { StockV2NewsContext } from "./news-context";

type NewsView = "context" | "assets";

const NEWS_VIEWS: readonly NewsView[] = ["context", "assets"];

export function StockV2NewsArea({ actions }: { actions: AppActions }) {
  const [view, setView, viewHref] = useQueryParamState<NewsView>(
    "stockv2News",
    NEWS_VIEWS,
    "context",
    { clearKeys: ["stockv2NewsContext", "stockv2NewsTheme"] },
  );

  return (
    <div className="grid gap-4">
      <SubTabs
        activeId={view}
        ariaLabel="消息面视图"
        onChange={(id) => setView(id as NewsView)}
        tabs={[
          { id: "context", label: "消息脉络", href: viewHref("context") },
          { id: "assets", label: "近期资产", href: viewHref("assets") },
        ]}
      />

      {view === "context" ? <StockV2NewsContext actions={actions} /> : <StockV2NewsWorkbench actions={actions} />}
    </div>
  );
}
