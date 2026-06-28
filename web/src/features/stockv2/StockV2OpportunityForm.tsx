import { useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2Opportunity,
  StockV2OpportunityInput,
  StockV2OpportunityInstrumentScope,
  StockV2OpportunityMarketScope,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice } from "../../components/ui";

const MARKET_OPTIONS: Array<{ value: StockV2OpportunityMarketScope; label: string }> = [
  { value: "a_share", label: "A股" },
  { value: "hk", label: "港股" },
  { value: "us", label: "美股" },
  { value: "all", label: "全部" },
];

const INSTRUMENT_OPTIONS: Array<{ value: StockV2OpportunityInstrumentScope; label: string }> = [
  { value: "stock", label: "个股" },
  { value: "exchange_fund", label: "ETF" },
  { value: "both", label: "个股+ETF" },
];

type Phase = "form" | "submitting" | "error";

// 创建主题机会 Drawer。POST /api/stockv2/opportunities，成功后选中新建项。
export function StockV2OpportunityForm({
  actions,
  onClose,
  onCreated,
}: {
  actions: AppActions;
  onClose: () => void;
  onCreated: (opp: StockV2Opportunity) => void;
}) {
  const [title, setTitle] = useState("");
  const [userThesis, setUserThesis] = useState("");
  const [marketScope, setMarketScope] = useState<StockV2OpportunityMarketScope>("a_share");
  const [instrumentScope, setInstrumentScope] = useState<StockV2OpportunityInstrumentScope>("both");
  const [phase, setPhase] = useState<Phase>("form");
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setPhase("submitting");
    setError(null);
    try {
      const input: StockV2OpportunityInput = {
        title: title.trim(),
        userThesis: userThesis.trim() || undefined,
        marketScope,
        instrumentScope,
      };
      const res = await actions.api<StockV2Opportunity>("/api/stockv2/opportunities", {
        method: "POST",
        body: input,
      });
      actions.setToast("主题机会已创建", "good");
      onCreated(res);
    } catch (err) {
      setError(friendlyError(err));
      setPhase("error");
      actions.setToast(friendlyError(err), "danger");
    }
  }

  const canSubmit = title.trim().length > 0 && phase !== "submitting";

  return (
    <Drawer title="新建主题机会" subtitle="输入主题或事件，随后启动 Agent 研究与候选发现" onClose={onClose} width={480}>
      <div className="grid gap-4">
        {phase === "error" && error ? <Notice tone="danger">创建失败：{error}</Notice> : null}

        <Field label="标题" help="短标题，例如「字节跳动 AI 模型主题」">
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="主题或事件标题" />
        </Field>

        <Field label="用户判断（Thesis）" help="保留原始判断；Agent 据此拆解产业链与候选">
          <textarea
            rows={4}
            value={userThesis}
            onChange={(e) => setUserThesis(e.target.value)}
            placeholder="例如：字节跳动新 AI 模型表现很好，找 A 股 / ETF 相关机会"
          />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="市场范围">
            <select value={marketScope} onChange={(e) => setMarketScope(e.target.value as StockV2OpportunityMarketScope)}>
              {MARKET_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </Field>
          <Field label="标的范围">
            <select value={instrumentScope} onChange={(e) => setInstrumentScope(e.target.value as StockV2OpportunityInstrumentScope)}>
              {INSTRUMENT_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </Field>
        </div>

        <Notice tone="warn">创建后可在详情页「开始发现」，由 Codex CLI 研究执行；主程序校验通过后才落库候选。</Notice>

        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={onClose}>取消</Button>
          <Button tone="primary" disabled={!canSubmit} onClick={() => void submit()}>
            {phase === "submitting" ? "创建中…" : "创建并选中"}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}
