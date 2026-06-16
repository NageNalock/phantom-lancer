import { ArrowsDownUp, PencilSimple, Plus, Trash } from "@phosphor-icons/react";
import { useState, type FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockPortfolio } from "../../app/types";
import { Button, CheckLabel, ContextList, EmptyState, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
import { money, number, numberText, percent, percentInput, price, text } from "./format";
import { StockSymbolCombobox } from "./StockSymbolCombobox";

type PortfolioPermissions = {
  allowBuy: boolean;
  allowAdd: boolean;
  allowReduce: boolean;
  allowSell: boolean;
};

export function StockPortfolios({
  actions,
  data,
  runAction,
}: {
  actions: AppActions;
  data: AppData;
  runAction: (label: string, fn: () => Promise<void>) => Promise<void>;
}) {
  const portfolios = data.stock.portfolios || [];
  const defaultPortfolio = portfolios[0];
  const recentInstruments = data.stock.instruments || [];
  const [editingId, setEditingId] = useState("");
  const [createPermissions, setCreatePermissions] = useState<PortfolioPermissions>({
    allowBuy: true,
    allowAdd: true,
    allowReduce: true,
    allowSell: true,
  });
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  async function createPortfolio(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    await runAction("已创建股票账户", async () => {
      await actions.api("/api/stock/portfolios", {
        method: "POST",
        body: {
          name: text(form, "name"),
          cash: number(form, "cash"),
          riskLevel: text(form, "riskLevel") || "balanced",
          maxSinglePositionPct: percentInput(form, "maxSinglePositionPct", 20),
          maxDrawdownPct: percentInput(form, "maxDrawdownPct", 15),
          ...permissionsFromForm(form),
          description: text(form, "description"),
        },
      });
      formElement.reset();
      setCreatePermissions({ allowBuy: true, allowAdd: true, allowReduce: true, allowSell: true });
    });
  }

  async function saveHolding(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const portfolioId = text(form, "portfolioId");
    await runAction("已保存持仓", async () => {
      await actions.api(`/api/stock/portfolios/${portfolioId}/holdings`, {
        method: "POST",
        body: {
          symbol: text(form, "symbol"),
          market: text(form, "market"),
          name: text(form, "name"),
          quantity: number(form, "quantity"),
          availableQuantity: number(form, "availableQuantity"),
          costPrice: number(form, "costPrice"),
          lastPrice: number(form, "lastPrice"),
          tradableStatus: text(form, "tradableStatus") || "tradable",
        },
      });
      formElement.reset();
    });
  }

  async function updatePortfolio(portfolio: StockPortfolio, form: FormData) {
    await runAction("已更新股票账户", async () => {
      await actions.api(`/api/stock/portfolios/${portfolio.id}`, {
        method: "PATCH",
        body: {
          name: text(form, "name"),
          cash: number(form, "cash"),
          riskLevel: text(form, "riskLevel") || "balanced",
          maxSinglePositionPct: percentInput(form, "maxSinglePositionPct", 20),
          maxDrawdownPct: percentInput(form, "maxDrawdownPct", 15),
          ...permissionsFromForm(form),
          description: text(form, "description"),
          notes: text(form, "notes"),
        },
      });
      setEditingId("");
    });
  }

  async function adjustCash(portfolio: StockPortfolio, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const amount = number(form, "amount");
    if (amount <= 0) {
      actions.setToast("资金调整金额必须大于 0", "danger");
      return;
    }
    const direction = text(form, "direction") || "deposit";
    const cashDelta = direction === "withdraw" ? -amount : amount;
    await runAction(direction === "withdraw" ? "已划出资金" : "已划入资金", async () => {
      await actions.api(`/api/stock/portfolios/${portfolio.id}`, {
        method: "PATCH",
        body: { cashDelta },
      });
      formElement.reset();
    });
  }

  async function deletePortfolio(portfolio: StockPortfolio) {
    const holdingCount = portfolio.holdings?.length || 0;
    const confirmed = await confirmDanger({
      title: "删除股票账户",
      objectName: portfolio.name || portfolio.id,
      body: "该操作会删除账户和当前持仓。如果账户已被策略、盯盘或历史操作引用，后端会阻止删除。",
      confirmLabel: "删除账户",
      impact: [
        holdingCount ? `将同时删除 ${holdingCount} 条当前持仓。` : "当前没有持仓会被删除。",
        "策略、盯盘、操作确认、复盘记忆等历史引用不会被静默改写。",
      ],
      recovery: "删除不可恢复；如后端检测到引用关系，删除会失败并保持原状态。",
    });
    if (!confirmed) return;
    await runAction("已删除股票账户", async () => {
      await actions.api(`/api/stock/portfolios/${portfolio.id}`, { method: "DELETE" });
    });
  }

  return (
    <>
      <div className="grid gap-4">
        <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
          <Panel title="新建账户/仓位组合">
            <form className="grid gap-3" onSubmit={(event) => void createPortfolio(event)}>
              <Field label="名称"><input className="input" name="name" required /></Field>
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(240px,1.25fr)] gap-3 max-lg:grid-cols-1">
                <Field label="可用现金"><input className="input" min="0" name="cash" step="0.01" type="number" /></Field>
                <Field label="单票上限(%)"><input className="input" defaultValue="20" max="100" min="1" name="maxSinglePositionPct" step="1" type="number" /></Field>
                <Field label="最大回撤(%)"><input className="input" defaultValue="15" max="100" min="1" name="maxDrawdownPct" step="1" type="number" /></Field>
              </div>
              <Field label="风险偏好">
                <select className="select" name="riskLevel" defaultValue="balanced">
                  <option value="conservative">保守</option>
                  <option value="balanced">均衡</option>
                  <option value="aggressive">进取</option>
                </select>
              </Field>
              <PermissionChecks permissions={createPermissions} setPermissions={setCreatePermissions} />
              <Field label="说明"><textarea className="textarea" name="description" /></Field>
              <div><Button tone="primary" type="submit"><Plus size={15} />创建账户</Button></div>
            </form>
          </Panel>
          <Panel title="录入/更新持仓">
            {portfolios.length ? (
              <form className="grid gap-3" onSubmit={(event) => void saveHolding(event)}>
                <Field label="账户">
                  <select className="select" name="portfolioId" defaultValue={defaultPortfolio?.id}>
                    {portfolios.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </Field>
                <StockSymbolCombobox actions={actions} label="股票" recent={recentInstruments} required />
                <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
                  <Field label="数量"><input className="input" min="0" name="quantity" step="1" type="number" /></Field>
                  <Field label="可卖数量"><input className="input" min="0" name="availableQuantity" step="1" type="number" /></Field>
                  <Field label="成本价"><input className="input" min="0" name="costPrice" step="0.001" type="number" /></Field>
                  <Field label="最新价"><input className="input" min="0" name="lastPrice" step="0.001" type="number" /></Field>
                </div>
                <Field label="可交易状态">
                  <select className="select" name="tradableStatus" defaultValue="tradable">
                    <option value="tradable">正常可交易</option>
                    <option value="halted">停牌</option>
                    <option value="limit_up">涨停</option>
                    <option value="limit_down">跌停</option>
                    <option value="unknown">未知</option>
                  </select>
                </Field>
                <div><Button tone="primary" type="submit"><Plus size={15} />保存持仓</Button></div>
              </form>
            ) : <EmptyState body="先创建账户，再录入持仓。" title="还没有账户" />}
          </Panel>
        </div>

        <div className="grid gap-3">
          {portfolios.map((portfolio) => (
            <PortfolioPanel
              editing={editingId === portfolio.id}
              key={portfolio.id}
              onAdjustCash={(event) => void adjustCash(portfolio, event)}
              onCancelEdit={() => setEditingId("")}
              onDelete={() => void deletePortfolio(portfolio)}
              onEdit={() => setEditingId(editingId === portfolio.id ? "" : portfolio.id)}
              onSave={(form) => void updatePortfolio(portfolio, form)}
              portfolio={portfolio}
            />
          ))}
          {!portfolios.length ? <EmptyState body="账户创建后会在这里展示风险约束、资金和持仓。" title="暂无账户" /> : null}
        </div>
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function PortfolioPanel({
  portfolio,
  editing,
  onEdit,
  onCancelEdit,
  onSave,
  onAdjustCash,
  onDelete,
}: {
  portfolio: StockPortfolio;
  editing: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onSave: (form: FormData) => void;
  onAdjustCash: (event: FormEvent<HTMLFormElement>) => void;
  onDelete: () => void;
}) {
  return (
    <Panel
      actions={(
        <>
          <Button aria-expanded={editing} onClick={onEdit}>
            <PencilSimple size={15} />{editing ? "收起编辑" : "编辑配置"}
          </Button>
          <Button aria-label={`删除账户 ${portfolio.name || "未命名账户"}`} onClick={onDelete} tone="danger">
            <Trash size={15} />删除
          </Button>
        </>
      )}
      title={portfolio.name || "未命名账户"}
      subtitle={`现金 ${money(portfolio.cash)} / 总资产 ${money(portfolio.totalAssetValue)}`}
    >
      <div className="grid grid-cols-2 gap-x-6 gap-y-2 max-lg:grid-cols-1">
        <ContextList
          items={[
            ["风险偏好", riskLevelLabel(portfolio.riskLevel)],
            ["单票上限", percent(portfolio.maxSinglePositionPct)],
            ["最大回撤", percent(portfolio.maxDrawdownPct)],
          ]}
        />
        <ContextList
          items={[
            ["现金占比", percent(portfolio.cashPct)],
            ["约束状态", <Pill tone={portfolio.constraintStatus === "ok" ? "good" : "warn"}>{constraintLabel(portfolio.constraintStatus)}</Pill>],
            ["更新时间", portfolio.updatedAt || "-"],
          ]}
        />
      </div>

      <div className="mt-3 flex flex-wrap gap-2">
        <Pill tone={portfolio.allowBuy ? "good" : "neutral"}>买入 {portfolio.allowBuy ? "on" : "off"}</Pill>
        <Pill tone={portfolio.allowAdd ? "good" : "neutral"}>加仓 {portfolio.allowAdd ? "on" : "off"}</Pill>
        <Pill tone={portfolio.allowReduce ? "good" : "neutral"}>减仓 {portfolio.allowReduce ? "on" : "off"}</Pill>
        <Pill tone={portfolio.allowSell ? "good" : "neutral"}>卖出 {portfolio.allowSell ? "on" : "off"}</Pill>
      </div>
      {portfolio.description ? <p className="muted mt-3 mb-0 text-sm leading-relaxed">{portfolio.description}</p> : null}
      {portfolio.notes ? <p className="muted mono mt-2 mb-0 text-xs leading-relaxed">notes: {portfolio.notes}</p> : null}

      {editing ? <PortfolioEditForm onCancel={onCancelEdit} onSave={onSave} portfolio={portfolio} /> : null}

      <form className="mt-4 grid grid-cols-[140px_minmax(0,1fr)_auto] items-end gap-3 border-t border-[var(--line)] pt-3 max-lg:grid-cols-1" onSubmit={onAdjustCash}>
        <Field label="资金方向">
          <select className="select" name="direction" defaultValue="deposit">
            <option value="deposit">划入</option>
            <option value="withdraw">划出</option>
          </select>
        </Field>
        <Field label="金额">
          <input className="input" min="0.01" name="amount" step="0.01" type="number" />
        </Field>
        <Button className="max-lg:w-fit" type="submit">
          <ArrowsDownUp size={15} />调整现金
        </Button>
      </form>

      {portfolio.holdings?.length ? (
        <div className="mt-4 overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead className="text-left text-xs text-[var(--muted)]">
              <tr><th className="py-2">股票</th><th>数量</th><th>成本</th><th>现价</th><th>市值</th><th>仓位</th><th>盈亏</th></tr>
            </thead>
            <tbody>
              {portfolio.holdings.map((holding) => (
                <tr className="border-t border-[var(--line)]" key={holding.id}>
                  <td className="py-2"><span className="mono">{holding.symbol}</span> {holding.name}</td>
                  <td>{numberText(holding.quantity)}</td>
                  <td>{price(holding.costPrice)}</td>
                  <td>{price(holding.lastPrice)}</td>
                  <td>{money(holding.marketValue)}</td>
                  <td>{percent(holding.positionPct)}</td>
                  <td className={Number(holding.pnl || 0) >= 0 ? "text-[var(--good)]" : "text-[var(--danger)]"}>{money(holding.pnl)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : <div className="mt-4"><EmptyState body="保存持仓后，资产和仓位会自动重算。" title="暂无持仓" /></div>}
    </Panel>
  );
}

function PortfolioEditForm({ portfolio, onCancel, onSave }: { portfolio: StockPortfolio; onCancel: () => void; onSave: (form: FormData) => void }) {
  const [permissions, setPermissions] = useState<PortfolioPermissions>({
    allowBuy: Boolean(portfolio.allowBuy),
    allowAdd: Boolean(portfolio.allowAdd),
    allowReduce: Boolean(portfolio.allowReduce),
    allowSell: Boolean(portfolio.allowSell),
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(new FormData(event.currentTarget));
  }

  return (
    <form className="mt-4 grid gap-3 border-t border-[var(--line)] pt-4" onSubmit={submit}>
      <div className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] gap-3 max-lg:grid-cols-1">
        <Field label="名称"><input className="input" defaultValue={portfolio.name || ""} name="name" required /></Field>
        <Field label="可用现金"><input className="input" defaultValue={Number(portfolio.cash || 0).toFixed(2)} min="0" name="cash" required step="0.01" type="number" /></Field>
      </div>
      <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
        <Field label="风险偏好">
          <select className="select" name="riskLevel" defaultValue={portfolio.riskLevel || "balanced"}>
            <option value="conservative">保守</option>
            <option value="balanced">均衡</option>
            <option value="aggressive">进取</option>
          </select>
        </Field>
        <Field label="单票上限(%)"><input className="input" defaultValue={Math.round(Number(portfolio.maxSinglePositionPct || 0.2) * 100)} max="100" min="1" name="maxSinglePositionPct" step="1" type="number" /></Field>
        <Field label="最大回撤(%)"><input className="input" defaultValue={Math.round(Number(portfolio.maxDrawdownPct || 0.15) * 100)} max="100" min="1" name="maxDrawdownPct" step="1" type="number" /></Field>
      </div>
      <PermissionChecks permissions={permissions} setPermissions={setPermissions} />
      {!permissions.allowBuy && !permissions.allowAdd && !permissions.allowReduce && !permissions.allowSell ? (
        <Notice>当前所有交易权限均关闭。该账户会保留持仓和资金，但自动操作会被风控阻止。</Notice>
      ) : null}
      <Field label="说明"><textarea className="textarea" defaultValue={portfolio.description || ""} name="description" /></Field>
      <Field label="备注"><textarea className="textarea" defaultValue={portfolio.notes || ""} name="notes" /></Field>
      <div className="flex flex-wrap gap-2">
        <Button tone="primary" type="submit">保存配置</Button>
        <Button onClick={onCancel}>取消</Button>
      </div>
    </form>
  );
}

function PermissionChecks({
  permissions,
  setPermissions,
}: {
  permissions: PortfolioPermissions;
  setPermissions: (permissions: PortfolioPermissions) => void;
}) {
  return (
    <div className="grid grid-cols-4 gap-2 max-md:grid-cols-2">
      <CheckLabel checked={permissions.allowBuy} name="allowBuy" onChange={(checked) => setPermissions({ ...permissions, allowBuy: checked })} size="xs">
        允许买入
      </CheckLabel>
      <CheckLabel checked={permissions.allowAdd} name="allowAdd" onChange={(checked) => setPermissions({ ...permissions, allowAdd: checked })} size="xs">
        允许加仓
      </CheckLabel>
      <CheckLabel checked={permissions.allowReduce} name="allowReduce" onChange={(checked) => setPermissions({ ...permissions, allowReduce: checked })} size="xs">
        允许减仓
      </CheckLabel>
      <CheckLabel checked={permissions.allowSell} name="allowSell" onChange={(checked) => setPermissions({ ...permissions, allowSell: checked })} size="xs">
        允许卖出
      </CheckLabel>
    </div>
  );
}

function permissionsFromForm(form: FormData): PortfolioPermissions {
  return {
    allowBuy: form.get("allowBuy") === "on",
    allowAdd: form.get("allowAdd") === "on",
    allowReduce: form.get("allowReduce") === "on",
    allowSell: form.get("allowSell") === "on",
  };
}

function riskLevelLabel(value?: string): string {
  return ({ conservative: "保守", balanced: "均衡", aggressive: "进取" }[value || ""] || value || "均衡");
}

function constraintLabel(value?: string): string {
  return ({ ok: "正常", single_position_exceeded: "单票超限" }[value || ""] || value || "未知");
}
