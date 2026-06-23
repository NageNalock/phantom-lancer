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
  const [jin10CurlInput, setJin10CurlInput] = useState("");
  const [financialJuiceCookieInput, setFinancialJuiceCookieInput] = useState("");
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (settings) {
      setForm(settings);
      setJin10CurlInput("");
      setFinancialJuiceCookieInput("");
      setDirty(false);
    }
  }, [settings?.id]);

  function update<K extends keyof StockV2Settings>(key: K, value: StockV2Settings[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
    setDirty(true);
  }

  async function handleSave() {
    await runAction("保存设置", async () => {
      const body: Record<string, unknown> = { ...form };
      if (jin10CurlInput.trim()) {
        body.jin10CurlInput = jin10CurlInput;
      }
      if (financialJuiceCookieInput.trim()) {
        body.financialJuiceCookieInput = financialJuiceCookieInput;
      }
      await actions.api("/api/stockv2/settings", {
        method: "PUT",
        body,
      });
      setJin10CurlInput("");
      setFinancialJuiceCookieInput("");
      setDirty(false);
    });
  }

  async function handleClearJin10Config() {
    await runAction("清除金十配置", async () => {
      await actions.api("/api/stockv2/settings", {
        method: "PUT",
        body: { jin10ClearConfig: true },
      });
      setJin10CurlInput("");
      setDirty(false);
    });
  }

  async function handleFetchJin10() {
    await runAction("运行金十消息处理", async () => {
      await actions.api("/api/stockv2/news/sources/jin10/run-once", { method: "POST" });
    });
  }

  async function handleClearFinancialJuiceCookie() {
    await runAction("清除 FinancialJuice Cookie", async () => {
      await actions.api("/api/stockv2/settings", {
        method: "PUT",
        body: { financialJuiceClearCookie: true },
      });
      setFinancialJuiceCookieInput("");
      setDirty(false);
    });
  }

  async function handleFetchFinancialJuice() {
    await runAction("运行 FinancialJuice 消息处理", async () => {
      await actions.api("/api/stockv2/news/sources/financialjuice/run-once", { method: "POST" });
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
                <div className="muted mt-0.5 text-xs">按设定周期自动更新标的主数据和行情</div>
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

      <Panel
        title="基础画像维护"
        subtitle="定期为主数据生成确定性股票画像，不触发全市场 AI"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.baseProfileAutoMaintainEnabled}
            label={
              <div>
                <div>启用基础画像自动维护</div>
                <div className="muted mt-0.5 text-xs">只运行本地确定性 RebuildStockProfiles；AI 画像仍需单只手动触发。</div>
              </div>
            }
            onChange={(checked) => update("baseProfileAutoMaintainEnabled", checked)}
          />

          <Field label="维护周期 (秒)" help="建议 86400 秒（每天一次）；关闭时不会后台运行。">
            <input
              min={3600}
              step={3600}
              type="number"
              value={form.baseProfileMaintainIntervalSeconds ?? 86400}
              onChange={(e) => update("baseProfileMaintainIntervalSeconds", Number(e.target.value))}
            />
          </Field>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">维护状态</span>
              <Pill tone={form.baseProfileAutoMaintainEnabled ? "good" : "neutral"}>
                {form.baseProfileAutoMaintainEnabled ? "已开启" : "手动维护"}
              </Pill>
            </div>
            <div className="mt-2 grid gap-1 text-xs text-[var(--muted)]">
              <span>上次：{formatTime(settings.baseProfileLastMaintainAt)}</span>
              <span>下次：{formatTime(settings.baseProfileNextMaintainAt)}</span>
              {settings.baseProfileLastMaintainResult ? <span className="break-words">结果：{settings.baseProfileLastMaintainResult}</span> : null}
            </div>
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

      <Panel
        title="中文消息源"
        subtitle="金十市场快讯，粘贴浏览器复制的 curl 后由系统解析 endpoint / Cookie / 必要 header"
        actions={
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void handleFetchJin10()} disabled={!form.jin10Enabled || !settings.jin10EndpointSet || !settings.jin10CookieSet}>
              处理一次
            </Button>
            <Button onClick={() => void handleClearJin10Config()} disabled={!settings.jin10EndpointSet && !settings.jin10CookieSet}>
              清除配置
            </Button>
          </div>
        }
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.jin10Enabled}
            label={
              <div>
                <div>启用金十</div>
                <div className="muted mt-0.5 text-xs">使用 www.jin10.com 市场快讯请求配置；敏感 Cookie 不会在 API 响应中回显。</div>
              </div>
            }
            onChange={(checked) => update("jin10Enabled", checked)}
          />

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">配置状态</span>
              <div className="flex gap-1.5">
                <Pill tone={settings.jin10EndpointSet ? "good" : "neutral"}>
                  endpoint {settings.jin10EndpointSet ? "已保存" : "未保存"}
                </Pill>
                <Pill tone={settings.jin10CookieSet ? "good" : "neutral"}>
                  Cookie {settings.jin10CookieSet ? "已保存" : "未保存"}
                </Pill>
              </div>
            </div>
            <p className="muted mt-2 text-xs">从浏览器 Network 里复制金十市场快讯请求的 curl，保存后只展示配置状态。</p>
          </div>

          <Field label="金十请求 curl" help="支持解析请求 URL、-b/--cookie、Cookie header、x-app-id、x-version。">
            <textarea
              rows={5}
              value={jin10CurlInput}
              onChange={(e) => {
                setJin10CurlInput(e.target.value);
                setDirty(true);
              }}
              placeholder="curl 'https://...jin10.com/tv/index/list?app=jin10' -b '...' -H 'x-app-id: ...' -H 'x-version: ...'"
            />
          </Field>
        </div>
      </Panel>

      <Panel
        title="英文消息源"
        subtitle="FinancialJuice 走统一抓取、归一化与关联链路"
        actions={
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void handleFetchFinancialJuice()} disabled={!form.financialJuiceEnabled || !settings.financialJuiceCookieSet}>
              处理一次
            </Button>
            <Button onClick={() => void handleClearFinancialJuiceCookie()} disabled={!settings.financialJuiceCookieSet}>
              清除 Cookie
            </Button>
          </div>
        }
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.financialJuiceEnabled}
            label={
              <div>
                <div>启用 FinancialJuice</div>
                <div className="muted mt-0.5 text-xs">使用用户本机保存的 Cookie 拉取英文快讯</div>
              </div>
            }
            onChange={(checked) => update("financialJuiceEnabled", checked)}
          />

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">Cookie 状态</span>
              <Pill tone={settings.financialJuiceCookieSet ? "good" : "neutral"}>
                {settings.financialJuiceCookieSet ? "已配置" : "未配置"}
              </Pill>
            </div>
            <p className="muted mt-2 text-xs">保存后仅显示配置状态，不在 API 响应中回显 Cookie。</p>
          </div>

          <Field label="FinancialJuice 请求片段" help="粘贴浏览器复制的 curl 或 Cookie header，系统只提取 Cookie。">
            <textarea
              rows={5}
              value={financialJuiceCookieInput}
              onChange={(e) => {
                setFinancialJuiceCookieInput(e.target.value);
                setDirty(true);
              }}
              placeholder="curl 'https://live.financialjuice.com/FJService.asmx/Startup?...' -H 'Cookie: ...'"
            />
          </Field>
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
            setJin10CurlInput("");
            setFinancialJuiceCookieInput("");
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
