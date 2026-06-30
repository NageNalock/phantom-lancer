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
        这里配置统一的数据资产维护任务。自动维护固定在每日 23:00 后的低峰窗口运行；每次任务会刷新标的与最新价，并逐只检查日 K。
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
                <div>启用全市场自动维护</div>
                <div className="muted mt-0.5 text-xs">
                  每天 23:00 开始运行；23:00-06:00 低峰窗口内允许补跑，白天不追补。
                </div>
              </div>
            }
            onChange={(checked) => update("autoUpdateEnabled", checked)}
          />

          <Field label="数据新鲜度窗口 (秒)" help="不控制自动维护触发时间；只影响统一维护中“标的是否足够新鲜可跳过”的判断，最大按 24 小时生效。">
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
                {form.autoUpdateEnabled ? "每日 23:00" : "已关闭"}
              </Pill>
            </div>
            <p className="muted mt-2 text-xs">
              调度窗口：Asia/Shanghai 23:00-06:00；新鲜度窗口：{formatInterval(Number(form.updateIntervalSec ?? 3600))}
            </p>
            {settings.lastScheduledUpdate ? (
              <p className="muted mt-2 text-xs">
                上次自动维护调度确认：{formatTime(settings.lastScheduledUpdate)}
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
                <span className="ml-2 text-xs text-[var(--muted)]">qt.gtimg.cn / web.ifzq.gtimg.cn</span>
              </div>
              <Pill tone="good">标的 / Quote / 日 K</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              标的主数据与批量 Quote 走 qt.gtimg.cn，日 K 走腾讯 fqkline；批量请求会自动打散，避免短时间打满数据源。
            </p>
          </div>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">东方财富 fallback</strong>
                <span className="ml-2 text-xs text-[var(--muted)]">push2his / fund NAV</span>
              </div>
              <Pill tone="neutral">分钟线 / 基金净值</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              最新价优先用分钟线投影，腾讯分钟线不可用时回退东方财富分钟线；场内基金日 K 缺口会回退基金净值接口。
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
              <li>自动维护：每日 23:00 后低峰窗口触发，统一处理标的、最新价和日 K 覆盖</li>
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
