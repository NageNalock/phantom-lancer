import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Settings } from "../../app/types";
import { Button, Field, Notice, Panel, Pill, Toggle } from "../../components/ui";
import { formatMeaningfulDateTime as formatTime } from "./time";

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;

export function StockV2Settings({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const settings = data.stockv2.settings;
  const [form, setForm] = useState<Partial<StockV2Settings>>({});
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (settings) {
      setForm(settings);
      setDirty(false);
    }
  }, [settings?.id]);

  function update<K extends keyof StockV2Settings>(key: K, value: StockV2Settings[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
    setDirty(true);
  }

  async function handleSave() {
    await runAction("保存设置", async () => {
      const body: Record<string, unknown> = {
        autoUpdateEnabled: !!form.autoUpdateEnabled,
        updateIntervalSec: Number(form.updateIntervalSec ?? 3600),
        proxyEnabled: !!form.proxyEnabled,
        proxyType: form.proxyType || "http",
        proxyHost: form.proxyHost || "",
        proxyPort: Number(form.proxyPort ?? 0),
      };
      await actions.api("/api/stockv2/settings", {
        method: "PUT",
        body,
      });
      setDirty(false);
    });
  }

  if (!settings) {
    return (
      <Panel title="设置">
        <p className="text-sm text-[var(--muted)]">加载中...</p>
      </Panel>
    );
  }

  return (
    <div className="grid gap-4">
      {dirty ? (
        <Notice tone="warn">当前有未保存的修改，点击下方「保存设置」按钮生效。</Notice>
      ) : null}

      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm text-[var(--muted-strong)]">
        这里配置统一的数据资产维护任务。每次任务会刷新标的与最新价，并逐只检查日 K；本地缺失、不足 250 根或已陈旧时才补拉。
      </div>

      {/* 数据资产自动维护 */}
      <Panel
        title="数据资产自动维护"
        subtitle="同一任务维护标的、最新价和本地日 K 覆盖"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.autoUpdateEnabled}
            label={
              <div>
                <div>启用后台维护</div>
                <div className="muted mt-0.5 text-xs">
                  按下面周期刷新标的列表、名称、市场、类型、状态、最新价，并按需补日 K。
                </div>
              </div>
            }
            onChange={(checked) => update("autoUpdateEnabled", checked)}
          />

          <Field label="维护周期 (秒)" help="影响统一数据资产维护。最小 300 秒，建议 1800-3600 秒。">
            <input
              type="number"
              min={300}
              step={60}
              value={form.updateIntervalSec ?? 3600}
              onChange={(e) => update("updateIntervalSec", Number(e.target.value))}
            />
          </Field>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">当前状态</span>
              <Pill tone={form.autoUpdateEnabled ? "good" : "neutral"}>
                {form.autoUpdateEnabled ? `每 ${formatInterval(Number(form.updateIntervalSec ?? 3600))}` : "已关闭"}
              </Pill>
            </div>
            {settings.lastScheduledUpdate ? (
              <p className="muted mt-2 text-xs">
                上次维护：{formatTime(settings.lastScheduledUpdate)}
              </p>
            ) : null}
            <ul className="muted mt-2 list-inside list-disc text-xs">
              <li>标的不存在或信息需要更新时，会刷新该标的主数据与最新价</li>
              <li>日 K 不存在、不足 250 根或超过新鲜度窗口时，会单独补拉该标的</li>
              <li>已满足覆盖的标的只做检查并跳过，批次内仍带随机打散</li>
            </ul>
          </div>
        </div>
      </Panel>

      {/* 代理配置 */}
      <Panel
        title="代理配置"
        subtitle="海外部署优化网络连接"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.proxyEnabled}
            label={
              <div>
                <div>启用代理</div>
                <div className="muted mt-0.5 text-xs">所有股票数据请求通过代理发送</div>
              </div>
            }
            onChange={(checked) => update("proxyEnabled", checked)}
          />

          <div className={`grid grid-cols-3 gap-3 ${!form.proxyEnabled ? "opacity-50" : ""}`}>
            <Field label="代理类型">
              <select
                value={form.proxyType || "http"}
                onChange={(e) => update("proxyType", e.target.value)}
                disabled={!form.proxyEnabled}
              >
                <option value="http">HTTP</option>
                <option value="https">HTTPS</option>
                <option value="socks5">SOCKS5</option>
              </select>
            </Field>
            <Field label="代理地址">
              <input
                type="text"
                placeholder="127.0.0.1"
                value={form.proxyHost || ""}
                onChange={(e) => update("proxyHost", e.target.value)}
                disabled={!form.proxyEnabled}
              />
            </Field>
            <Field label="端口">
              <input
                type="number"
                placeholder="7890"
                value={form.proxyPort ?? 0}
                onChange={(e) => update("proxyPort", Number(e.target.value))}
                disabled={!form.proxyEnabled}
              />
            </Field>
          </div>
        </div>
      </Panel>

      {/* 数据源信息 */}
      <Panel
        title="数据源说明"
        subtitle="这里只展示外部接口和限频策略，不是独立维护任务"
      >
        <div className="grid gap-3">
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">腾讯行情接口</strong>
                <span className="ml-2 text-xs text-[var(--muted)]">qt.gtimg.cn</span>
              </div>
              <Pill tone="good">标的/Quote</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              用于刷新标的基础信息、最新价和日 K。批量请求会自动打散，避免短时间打满数据源。
            </p>
          </div>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">批量打散策略</strong>
              </div>
              <Pill tone="good">已启用</Pill>
            </div>
            <ul className="muted mt-2 list-inside list-disc text-xs">
              <li>自动维护：按上面的周期触发，统一处理标的、最新价和日 K 覆盖</li>
              <li>手动补拉：在“维护任务”里立即创建日 K 抓取任务</li>
              <li>失败会记录在维护历史里，不会删除已有本地数据</li>
            </ul>
          </div>
        </div>
      </Panel>

      {/* 保存按钮 */}
      <div className="flex justify-end gap-2">
        <Button
          onClick={() => {
            setForm(settings);
            setDirty(false);
          }}
          disabled={!dirty}
        >
          重置
        </Button>
        <Button tone="primary" onClick={() => void handleSave()} disabled={!dirty}>
          保存设置
        </Button>
      </div>
    </div>
  );
}

function formatInterval(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`;
  return `${seconds} 秒`;
}
