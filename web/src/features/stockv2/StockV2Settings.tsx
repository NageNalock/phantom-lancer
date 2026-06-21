import { Clock, Globe, Shield } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Settings } from "../../app/types";
import { Button, Field, Notice, Panel, Pill, Toggle } from "../../components/ui";
import { stockV2SettingsSummary } from "../../domain/labels";

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
      await actions.api("/api/stockv2/settings", {
        method: "PUT",
        body: form,
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

      {/* 自动更新 */}
      <Panel
        title="自动更新"
        subtitle="定时后台拉取最新股票数据"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.autoUpdateEnabled}
            label={
              <div>
                <div>启用自动更新</div>
                <div className="muted mt-0.5 text-xs">按设定周期自动更新股票主数据和行情</div>
              </div>
            }
            onChange={(checked) => update("autoUpdateEnabled", checked)}
          />

          <Field label="更新周期 (秒)" help="最小 300 秒（5 分钟），建议 1800-3600 秒">
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
                {stockV2SettingsSummary(form as StockV2Settings)}
              </Pill>
            </div>
            {settings.lastScheduledUpdate ? (
              <p className="muted mt-2 text-xs">
                上次定时更新：{formatTime(settings.lastScheduledUpdate)}
              </p>
            ) : null}
          </div>
        </div>
      </Panel>

      {/* 日级 K 线自动增量 */}
      <Panel
        title="日级 K 线（Daily Bars）"
        subtitle="收盘后自动为全市场最近交易日窗口补拉日级行情（周末跳过）"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.dailyBarsAutoEnabled}
            label={
              <div>
                <div>启用自动增量</div>
                <div className="muted mt-0.5 text-xs">
                  工作日 16:30 (Asia/Shanghai) 之后触发一次全市场最近交易日增量；当日去重，周末不跑。
                </div>
              </div>
            }
            onChange={(checked) => update("dailyBarsAutoEnabled", checked)}
          />

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">调度状态</span>
              <Pill tone={form.dailyBarsAutoEnabled ? "good" : "neutral"}>
                {form.dailyBarsAutoEnabled ? "已开启" : "手动触发"}
              </Pill>
            </div>
            {hasMeaningfulTime(settings.dailyBarsLastRun) ? (
              <p className="muted mt-2 text-xs">
                上次定时增量：{formatTime(settings.dailyBarsLastRun)}
              </p>
            ) : (
              <p className="muted mt-2 text-xs">尚未执行定时增量。</p>
            )}
            <ul className="muted mt-2 list-inside list-disc text-xs">
              <li>自动任务 = 全市场 active 主数据最近约 10 个自然日窗口</li>
              <li>热集合 = 手动任务，当前全部持仓（去重 symbol）</li>
              <li>交易日历简化：仅跳过周六日，当日不重复执行</li>
              <li>单只间随机抖动 80±60ms，避免被数据源风控</li>
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
        title="数据源"
        subtitle="当前使用的行情接口"
      >
        <div className="grid gap-3">
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">腾讯行情接口</strong>
                <span className="ml-2 text-xs text-[var(--muted)]">qt.gtimg.cn</span>
              </div>
              <Pill tone="good">主数据源</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              海外可用，支持 A 股 / 港股 / 美股。批量 80 只，批间抖动 30-50ms 避免风控。
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
              <li>每批 80 只股票</li>
              <li>批间随机抖动 30-50ms</li>
              <li>失败自动重试，指数退避</li>
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

function formatTime(iso?: string): string {
  if (!hasMeaningfulTime(iso)) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function hasMeaningfulTime(iso?: string): iso is string {
  return !!iso && !iso.startsWith("0001-01-01");
}
