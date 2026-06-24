import { ArrowClockwise } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2PagedResponse, StockV2Settings, StockV2StockProfileUpdateTask } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Field, Notice, Panel, Pill, Toggle } from "../../components/ui";
import { stockV2SettingsSummary } from "../../domain/labels";

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;

const PROFILE_TASK_PAGE_SIZE = 12;

export function StockV2Settings({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const settings = data.stockv2.settings;
  const [form, setForm] = useState<Partial<StockV2Settings>>({});
  const [jin10CurlInput, setJin10CurlInput] = useState("");
  const [financialJuiceCookieInput, setFinancialJuiceCookieInput] = useState("");
  const [profileTasks, setProfileTasks] = useState<StockV2StockProfileUpdateTask[]>([]);
  const [profileTaskTotal, setProfileTaskTotal] = useState(0);
  const [profileTaskPage, setProfileTaskPage] = useState(1);
  const [profileTaskLoading, setProfileTaskLoading] = useState(false);
  const [profileTaskError, setProfileTaskError] = useState("");
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
    await runAction("清除 FinancialJuice 凭据", async () => {
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

  async function loadProfileTasks(nextPage = profileTaskPage) {
    setProfileTaskLoading(true);
    setProfileTaskError("");
    try {
      const safePage = Math.max(1, nextPage);
      const params = new URLSearchParams({
        limit: String(PROFILE_TASK_PAGE_SIZE),
        offset: String((safePage - 1) * PROFILE_TASK_PAGE_SIZE),
      });
      const res = await actions.api<StockV2PagedResponse<StockV2StockProfileUpdateTask>>(
        `/api/stockv2/profiles/update-tasks?${params.toString()}`,
      );
      const total = res.total ?? 0;
      const limit = res.limit || PROFILE_TASK_PAGE_SIZE;
      const resolvedOffset = res.offset ?? (safePage - 1) * PROFILE_TASK_PAGE_SIZE;
      setProfileTasks(res.items ?? []);
      setProfileTaskTotal(total);
      setProfileTaskPage(total > 0 && resolvedOffset >= total ? Math.max(1, Math.ceil(total / limit)) : safePage);
    } catch (error) {
      setProfileTaskError(friendlyError(error));
    } finally {
      setProfileTaskLoading(false);
    }
  }

  useEffect(() => {
    void loadProfileTasks(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
        title="自动画像更新"
        subtitle="后台按队列、预算和限速更新基础输入；输入变化时才启动画像 AI"
      >
        <div className="grid gap-4">
          <Toggle
            checked={!!form.baseProfileAutoMaintainEnabled}
            label={
              <div>
                <div>启用自动画像更新</div>
                <div className="muted mt-0.5 text-xs">先修复本地索引，再小批量深度更新；基础输入变化时才尝试 AI。</div>
              </div>
            }
            onChange={(checked) => update("baseProfileAutoMaintainEnabled", checked)}
          />

          <Field label="更新周期 (秒)" help="建议 86400 秒（每天一次）；关闭时不会后台运行。">
            <input
              min={3600}
              step={3600}
              type="number"
              value={form.baseProfileMaintainIntervalSeconds ?? 86400}
              onChange={(e) => update("baseProfileMaintainIntervalSeconds", Number(e.target.value))}
            />
          </Field>

          <div className="grid grid-cols-3 gap-3">
            <Field label="每轮标的数" help="每次自动维护最多处理多少只标的。">
              <input
                min={1}
                max={50}
                step={1}
                type="number"
                value={form.baseProfileDeepUpdateBatchSize ?? 12}
                onChange={(e) => update("baseProfileDeepUpdateBatchSize", Number(e.target.value))}
              />
            </Field>
            <Field label="AI 预算" help="每轮最多启动多少次画像 AI。">
              <input
                min={1}
                max={10}
                step={1}
                type="number"
                value={form.baseProfileDeepUpdateAiBudget ?? 2}
                onChange={(e) => update("baseProfileDeepUpdateAiBudget", Number(e.target.value))}
              />
            </Field>
            <Field label="单只间隔 (ms)" help="候选之间的基础等待时间，后台会再做稳定打散。">
              <input
                min={100}
                max={60000}
                step={100}
                type="number"
                value={form.baseProfileDeepUpdateRateLimitMs ?? 1500}
                onChange={(e) => update("baseProfileDeepUpdateRateLimitMs", Number(e.target.value))}
              />
            </Field>
          </div>

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">自动更新状态</span>
              <Pill tone={form.baseProfileAutoMaintainEnabled ? "good" : "neutral"}>
                {form.baseProfileAutoMaintainEnabled ? "已开启" : "已关闭"}
              </Pill>
            </div>
            <div className="mt-2 grid gap-1 text-xs text-[var(--muted)]">
              <span>上次：{formatTime(settings.baseProfileLastMaintainAt)}</span>
              <span>下次：{formatTime(settings.baseProfileNextMaintainAt)}</span>
              <span>
                队列：每轮 {form.baseProfileDeepUpdateBatchSize ?? 12} 只，AI {form.baseProfileDeepUpdateAiBudget ?? 2} 次，间隔 {form.baseProfileDeepUpdateRateLimitMs ?? 1500}ms
              </span>
              {settings.baseProfileLastMaintainResult ? <span className="break-words">最近结果：{settings.baseProfileLastMaintainResult}</span> : null}
            </div>
          </div>

          <StockProfileUpdateTasksPanel
            items={profileTasks}
            loading={profileTaskLoading}
            error={profileTaskError}
            page={profileTaskPage}
            total={profileTaskTotal}
            onRefresh={() => void loadProfileTasks(profileTaskPage)}
            onPage={(page) => void loadProfileTasks(page)}
          />
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
        subtitle="金十市场快讯默认使用首页接口；可粘贴浏览器复制的 curl 覆盖 endpoint / Cookie / 必要 header"
        actions={
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void handleFetchJin10()} disabled={!form.jin10Enabled}>
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
                <div className="muted mt-0.5 text-xs">默认抓取 www.jin10.com 市场快讯；敏感 Cookie 不会在 API 响应中回显。</div>
              </div>
            }
            onChange={(checked) => update("jin10Enabled", checked)}
          />

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">配置状态</span>
              <div className="flex gap-1.5">
                <Pill tone={settings.jin10EndpointSet ? "good" : "neutral"}>
                  endpoint {settings.jin10EndpointSet ? "已保存" : "默认接口"}
                </Pill>
                <Pill tone={settings.jin10CookieSet ? "good" : "neutral"}>
                  Cookie {settings.jin10CookieSet ? "已保存" : "可选"}
                </Pill>
              </div>
            </div>
            <p className="muted mt-2 text-xs">默认接口可直接抓取首页市场快讯；粘贴 curl 只用于覆盖默认请求配置。</p>
          </div>

          <Field label="金十请求 curl" help="可选。支持解析请求 URL、-b/--cookie、Cookie header、x-app-id、x-version。">
            <textarea
              rows={5}
              value={jin10CurlInput}
              onChange={(e) => {
                setJin10CurlInput(e.target.value);
                setDirty(true);
              }}
              placeholder="curl 'https://flash-api.jin10.com/get_flash_list?channel=-8200&vip=1' -H 'x-app-id: bVBF4FyRTn5NJF5n' -H 'x-version: 1.0.0'"
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
              清除凭据
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
                <div className="muted mt-0.5 text-xs">使用用户本机复制的请求凭据拉取英文快讯</div>
              </div>
            }
            onChange={(checked) => update("financialJuiceEnabled", checked)}
          />

          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">凭据状态</span>
              <Pill tone={settings.financialJuiceCookieSet ? "good" : "neutral"}>
                {settings.financialJuiceCookieSet ? "已配置" : "未配置"}
              </Pill>
            </div>
            <p className="muted mt-2 text-xs">保存后仅显示配置状态，不在 API 响应中回显 URL token 或 Cookie。</p>
          </div>

          <Field label="FinancialJuice 请求片段" help="粘贴浏览器复制的 Startup curl、含 info 的请求 URL 或 Cookie header。">
            <textarea
              rows={5}
              value={financialJuiceCookieInput}
              onChange={(e) => {
                setFinancialJuiceCookieInput(e.target.value);
                setDirty(true);
              }}
              placeholder="curl 'https://live.financialjuice.com/FJService.asmx/Startup?info=...' 或 Cookie: ..."
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

function StockProfileUpdateTasksPanel({
  items,
  loading,
  error,
  page,
  total,
  onRefresh,
  onPage,
}: {
  items: StockV2StockProfileUpdateTask[];
  loading: boolean;
  error: string;
  page: number;
  total: number;
  onRefresh: () => void;
  onPage: (page: number) => void;
}) {
  const totalPages = Math.max(1, Math.ceil(total / PROFILE_TASK_PAGE_SIZE));
  const start = total === 0 ? 0 : (page - 1) * PROFILE_TASK_PAGE_SIZE + 1;
  const end = Math.min(total, page * PROFILE_TASK_PAGE_SIZE);

  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <strong className="block text-sm">画像更新记录</strong>
          <p className="muted mt-1 mb-0 text-xs">记录自动/手动触发、基础输入变化、AI 调用决策和数据源状态。</p>
        </div>
        <Button className="inline-flex items-center gap-1.5" onClick={onRefresh} disabled={loading}>
          <ArrowClockwise size={15} />
          刷新
        </Button>
      </div>

      {error ? <Notice tone="danger">加载画像更新记录失败：{error}</Notice> : null}

      {items.length === 0 && !loading ? (
        <div className="mt-3">
          <EmptyState title="暂无画像更新记录" body="自动画像更新或单只股票手动更新执行后，会在这里留下任务记录。" />
        </div>
      ) : (
        <div className="mt-3 overflow-x-auto rounded-md border border-[var(--line)] bg-[var(--surface)]">
          <table className="min-w-full text-left text-sm">
            <thead className="border-b border-[var(--line)] text-xs text-[var(--muted)]">
              <tr>
                <th className="px-3 py-2 font-medium">标的</th>
                <th className="px-3 py-2 font-medium">触发</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">基础输入</th>
                <th className="px-3 py-2 font-medium">AI</th>
                <th className="px-3 py-2 font-medium">数据源</th>
                <th className="px-3 py-2 font-medium">时间</th>
              </tr>
            </thead>
            <tbody>
              {items.map((task) => (
                <tr key={task.id} className="border-b border-[var(--line)] last:border-b-0">
                  <td className="px-3 py-2 align-top">
                    <div className="font-mono text-xs font-semibold">{task.symbol}</div>
                    <div className="muted mt-0.5 text-xs">{task.market || "-"}</div>
                  </td>
                  <td className="px-3 py-2 align-top">
                    <Pill tone={task.triggerSource === "auto" ? "good" : "neutral"}>{triggerSourceLabel(task.triggerSource)}</Pill>
                    {task.triggerReason ? <div className="muted mt-1 max-w-[220px] truncate text-xs">{task.triggerReason}</div> : null}
                  </td>
                  <td className="px-3 py-2 align-top">
                    <Pill tone={taskStatusTone(task.status)}>{taskStatusLabel(task.status)}</Pill>
                    {task.errorMessage ? <div className="mt-1 max-w-[240px] truncate text-xs text-[var(--danger)]">{task.errorMessage}</div> : null}
                  </td>
                  <td className="px-3 py-2 align-top">
                    <Pill tone={task.baseInputChanged ? "warn" : "neutral"}>{task.baseInputChanged ? "有变化" : "无变化"}</Pill>
                  </td>
                  <td className="px-3 py-2 align-top">
                    <Pill tone={aiDecisionTone(task.aiDecision)}>{aiDecisionLabel(task.aiDecision)}</Pill>
                    {task.agentRunId ? <div className="muted mt-1 font-mono text-xs">run {shortID(task.agentRunId)}</div> : null}
                  </td>
                  <td className="px-3 py-2 align-top">
                    <StockProfileSourceStatusSummary task={task} />
                  </td>
                  <td className="px-3 py-2 align-top">
                    <div className="text-xs">{formatTime(task.startedAt)}</div>
                    {hasMeaningfulTime(task.finishedAt) ? <div className="muted mt-0.5 text-xs">完成 {formatTime(task.finishedAt)}</div> : null}
                  </td>
                </tr>
              ))}
              {loading ? (
                <tr>
                  <td className="muted px-3 py-4 text-center text-sm" colSpan={7}>加载中...</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      )}

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--muted)]">
        <span>
          第 {page} / {totalPages} 页 · {start}-{end} / {total}
        </span>
        <div className="flex items-center gap-1.5">
          <Button disabled={loading || page <= 1} onClick={() => onPage(page - 1)}>上一页</Button>
          <Button disabled={loading || page >= totalPages} onClick={() => onPage(page + 1)}>下一页</Button>
        </div>
      </div>
    </div>
  );
}

function StockProfileSourceStatusSummary({ task }: { task: StockV2StockProfileUpdateTask }) {
  const statuses = task.sourceStatuses ?? [];
  if (statuses.length === 0) return <span className="text-xs text-[var(--muted)]">-</span>;
  return (
    <div className="flex max-w-[260px] flex-wrap gap-1">
      {statuses.slice(0, 3).map((item) => (
        <Pill className="max-w-[180px] truncate" key={`${task.id}-${item.source}`} tone={sourceStatusTone(item.status)}>
          {item.source} {sourceStatusLabel(item.status)}
        </Pill>
      ))}
      {statuses.length > 3 ? <span className="text-xs text-[var(--muted)]">+{statuses.length - 3}</span> : null}
    </div>
  );
}

function triggerSourceLabel(value: string): string {
  if (value === "auto") return "自动";
  if (value === "manual") return "手动";
  return value || "-";
}

function taskStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    completed: "完成",
    partial: "部分完成",
    failed: "失败",
  };
  return labels[status] ?? status;
}

function taskStatusTone(status: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "completed") return "good";
  if (status === "partial") return "warn";
  if (status === "failed") return "danger";
  return "neutral";
}

function aiDecisionLabel(decision: string): string {
  const labels: Record<string, string> = {
    called: "已调用",
    skipped_unchanged: "输入未变",
    skipped_not_configured: "未配置",
    skipped_unavailable: "不可用",
    failed: "失败",
  };
  return labels[decision] ?? (decision || "-");
}

function aiDecisionTone(decision: string): "neutral" | "good" | "warn" | "danger" {
  if (decision === "called") return "good";
  if (decision === "failed") return "danger";
  if (decision === "skipped_unavailable") return "warn";
  return "neutral";
}

function sourceStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    success: "成功",
    failed: "失败",
    skipped: "跳过",
  };
  return labels[status] ?? status;
}

function sourceStatusTone(status: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "success") return "good";
  if (status === "failed") return "danger";
  if (status === "skipped") return "neutral";
  return "neutral";
}

function shortID(id: string): string {
  if (!id) return "-";
  return id.length <= 10 ? id : id.slice(0, 8);
}
