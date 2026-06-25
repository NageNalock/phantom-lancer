import { useEffect, useMemo, useRef, useState } from "react";
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  type CrosshairMode,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from "lightweight-charts";
import type { StockV2DailyBar, StockV2MinuteBar } from "../../app/types";
import { EmptyState, Notice } from "../../components/ui";

/**
 * StockV2KLineChart 使用 lightweight-charts v5 绘制 K 线 + 成交量。
 *
 * 核心原则（Ponytail / 安全）：
 *   - 有 error 或 bars 为空时，不画任何假 K 线。
 *   - 阳线 red / 阴线 green（A 股惯例），颜色取 CSS 变量 --good/--danger/--line/--text/--muted/--accent。
 */
export function StockV2KLineChart({
  bars,
  error,
  loading,
  mode = "daily",
}: {
  bars?: Array<StockV2DailyBar | StockV2MinuteBar> | null;
  error?: string | null;
  loading?: boolean;
  mode?: "daily" | "minute";
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const histRef = useRef<ISeriesApi<"Histogram"> | null>(null);
  const [hover, setHover] = useState<{
    time: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    pct: number;
  } | null>(null);

  // lightweight-charts 需要 time 为 UTCTimestamp（秒级）。
  const candleData = useMemo(() => {
    if (!bars?.length) return [];
    return bars.map((b) => {
      const label = barLabel(b, mode);
      const t = chartTimestamp(b, mode);
      return {
        time: t,
        open: round2(b.open),
        high: round2(b.high),
        low: round2(b.low),
        close: round2(b.close),
        _volume: round0(b.volume ?? 0),
        _pct: b.pctChange ?? NaN,
        _date: label,
      };
    });
  }, [bars, mode]);
  const candleDataRef = useRef(candleData);
  useEffect(() => {
    candleDataRef.current = candleData;
  }, [candleData]);

  const volumeData = useMemo(() => {
    if (!bars?.length) return [];
    return bars.map((b) => {
      const t = chartTimestamp(b, mode);
      const up = b.close >= b.open;
      return {
        time: t,
        value: round0(b.volume ?? 0),
        color: up ? "rgba(207,31,50,0.55)" : "rgba(18,132,79,0.55)",
      };
    });
  }, [bars, mode]);
  const shouldRenderChart = !error && !loading && !!bars?.length;

  // 初始化图表。组件第一次渲染常常是 loading/empty 分支，此时 chart 容器还不存在；
  // 因此不能用空依赖只跑一次，要等真实容器出现后再创建 lightweight-charts 实例。
  useEffect(() => {
    if (!shouldRenderChart || chartRef.current) return;
    const host = containerRef.current;
    if (!host) return;

    const css = getComputedStyle(host);
    const surface = css.getPropertyValue("--surface").trim() || "#0f1115";
    const line = css.getPropertyValue("--line").trim() || "#2a2f36";
    const text = css.getPropertyValue("--text").trim() || "#e6e8eb";
    const muted = css.getPropertyValue("--muted").trim() || "#8a8f98";

    const rect = host.getBoundingClientRect();
    const chart = createChart(host, {
      width: Math.max(1, Math.floor(rect.width || host.clientWidth || 640)),
      height: Math.max(1, Math.floor(rect.height || host.clientHeight || 420)),
      layout: {
        attributionLogo: false,
        background: { color: surface },
        textColor: text,
        fontFamily:
          "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', sans-serif",
      },
      grid: {
        vertLines: { color: line, style: 3 },
        horzLines: { color: line, style: 3 },
      },
      crosshair: { mode: 1 as CrosshairMode.Normal, vertLine: { color: line }, horzLine: { color: line } },
      rightPriceScale: { borderColor: line, scaleMargins: { top: 0.08, bottom: 0.28 } },
      timeScale: {
        borderColor: line,
        timeVisible: mode === "minute",
        secondsVisible: false,
        minBarSpacing: 1,
      },
      handleScroll: { mouseWheel: true, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: true },
      handleScale: { axisPressedMouseMove: true, mouseWheel: true, pinch: true },
    });

    const candle = chart.addSeries(CandlestickSeries, {
      upColor: "#cf1f32",      // danger 红
      downColor: "#12844f",    // good 绿
      borderUpColor: "#cf1f32",
      borderDownColor: "#12844f",
      wickUpColor: "#cf1f32",
      wickDownColor: "#12844f",
    });

    const hist = chart.addSeries(HistogramSeries, {
      priceFormat: { type: "volume" },
      priceScaleId: "",
    });
    hist.priceScale().applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
      borderColor: line,
    });

    // 十字光标 -> OHLC 抬头
    chart.subscribeCrosshairMove((param) => {
      if (!param.time || !param.seriesData.size) {
        setHover(null);
        return;
      }
      const cb = param.seriesData.get(candle) as any;
      const vb = param.seriesData.get(hist) as any;
      if (!cb) {
        setHover(null);
        return;
      }
      // 回查 bars 拿 tradeDate 和 pctChange（避免在 data 里塞 custom field 被库警告）
      const currentData = candleDataRef.current;
      const idx = currentData.findIndex((d) => String(d.time) === String(param.time));
      const date = idx >= 0 ? currentData[idx]._date : String(param.time);
      const pct = idx >= 0 ? currentData[idx]._pct : NaN;
      setHover({
        time: date,
        open: cb.open,
        high: cb.high,
        low: cb.low,
        close: cb.close,
        volume: vb?.value ?? 0,
        pct,
      });
    });

    chartRef.current = chart;
    candleRef.current = candle;
    histRef.current = hist;

    return () => {
      chart.remove();
      chartRef.current = null;
      candleRef.current = null;
      histRef.current = null;
    };
  }, [shouldRenderChart]);

  useEffect(() => {
    chartRef.current?.applyOptions({
      timeScale: {
        timeVisible: mode === "minute",
        secondsVisible: false,
      },
    });
  }, [mode]);

  // Drawer / 侧栏布局下，图表容器尺寸可能在实例创建后的下一帧才稳定。
  // 这里由组件自己观察容器尺寸，避免首次宽度为 0 时出现空白图。
  useEffect(() => {
    if (!shouldRenderChart || !chartRef.current || !containerRef.current) return;
    const chart = chartRef.current;
    const host = containerRef.current;
    const resize = () => {
      const rect = host.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) {
        chart.resize(Math.floor(rect.width), Math.floor(rect.height), true);
      }
    };
    resize();
    const frame = window.requestAnimationFrame(resize);
    const observer = new ResizeObserver(resize);
    observer.observe(host);
    return () => {
      window.cancelAnimationFrame(frame);
      observer.disconnect();
    };
  }, [shouldRenderChart]);

  // 数据变更时 setData
  useEffect(() => {
    if (!candleRef.current || !histRef.current) return;
    if (!candleData.length) {
      candleRef.current.setData([]);
      histRef.current.setData([]);
      return;
    }
    // lightweight-charts 不接受 extra fields。
    const pureCandle = candleData.map(({ time, open, high, low, close }) => ({ time, open, high, low, close }));
    candleRef.current.setData(pureCandle);
    histRef.current.setData(volumeData as any);
    // 首次填充时自适应
    try {
      chartRef.current?.timeScale().fitContent();
    } catch {
      /* noop */
    }
  }, [candleData, volumeData]);

  // 错误 / 空 / 加载态：不画假 K 线
  if (error) {
    return (
      <div className="flex h-[420px] w-full flex-col">
        <Notice tone="danger">
          <strong className="block text-xs">获取{mode === "minute" ? "分钟行情" : "日 K"}失败</strong>
          <span className="mt-1 block break-words text-[11px] leading-relaxed opacity-90">{error}</span>
        </Notice>
        <div className="mt-2 flex-1">
          <EmptyState title="暂无 K 线" body="拉取失败时不会伪造或展示旧数据，请稍后重试。" />
        </div>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex h-[420px] w-full flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)]">
        <div className="h-3 w-3 animate-spin rounded-full border-2 border-[var(--line)] border-t-[var(--accent)]" />
        <span className="text-xs text-[var(--muted-strong)]">正在加载{mode === "minute" ? "分钟行情" : "日 K"}…</span>
      </div>
    );
  }

  if (!bars?.length) {
    return (
      <div className="h-[420px] w-full">
        <EmptyState
          title={mode === "minute" ? "本地尚无分钟行情" : "本地尚无日 K"}
          body={mode === "minute" ? "后台盘中分钟行情任务会自动采集持仓和监控标的；有快照后会显示最近 5 天分钟 K。" : "点击右上角『刷新』可触发补拉；系统会在后台异步抓取并落盘。"}
        />
      </div>
    );
  }

  if (bars.length === 1) {
    const only = bars[0];
    const label = barLabel(only, mode);
    return (
      <div className="flex h-[420px] w-full flex-col rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-b border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
          <span className="font-mono text-[var(--muted-strong)]">{label}</span>
          <span>
            <span className="text-[var(--muted)]">开 </span>
            <span className="font-mono">{only.open.toFixed(2)}</span>
          </span>
          <span>
            <span className="text-[var(--muted)]">高 </span>
            <span className="font-mono text-[var(--danger)]">{only.high.toFixed(2)}</span>
          </span>
          <span>
            <span className="text-[var(--muted)]">低 </span>
            <span className="font-mono text-[var(--good)]">{only.low.toFixed(2)}</span>
          </span>
          <span>
            <span className="text-[var(--muted)]">收 </span>
            <span className="font-mono font-semibold">{only.close.toFixed(2)}</span>
          </span>
          <span className="ml-auto text-[var(--muted)]">
            <span>量 </span>
            <span className="font-mono">{formatVol(only.volume ?? 0)}</span>
          </span>
        </div>
        <div className="flex flex-1 items-center justify-center p-4">
          <EmptyState
            title={mode === "minute" ? "本地只有 1 根分钟 K" : "本地只有 1 根日 K"}
            body="少于 2 根时不绘制 K 线，避免单根蜡烛被拉伸成异常图形。"
          />
        </div>
      </div>
    );
  }

  const latest = bars[bars.length - 1];
  const display = hover ?? {
    time: barLabel(latest, mode),
    open: latest.open,
    high: latest.high,
    low: latest.low,
    close: latest.close,
    volume: latest.volume ?? 0,
    pct: latest.pctChange ?? NaN,
  };
  const up = display.close >= display.open;

  return (
    <div className="flex w-full flex-col">
      {/* 抬头：当前/悬停 OHLC */}
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 rounded-t-lg border border-b-0 border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
        <span className="font-mono text-[var(--muted-strong)]">{display.time}</span>
        <span>
          <span className="text-[var(--muted)]">开 </span>
          <span className="font-mono">{display.open.toFixed(2)}</span>
        </span>
        <span>
          <span className="text-[var(--muted)]">高 </span>
          <span className="font-mono text-[var(--danger)]">{display.high.toFixed(2)}</span>
        </span>
        <span>
          <span className="text-[var(--muted)]">低 </span>
          <span className="font-mono text-[var(--good)]">{display.low.toFixed(2)}</span>
        </span>
        <span>
          <span className="text-[var(--muted)]">收 </span>
          <span className={`font-mono font-semibold ${up ? "text-[var(--danger)]" : "text-[var(--good)]"}`}>
            {display.close.toFixed(2)}
          </span>
        </span>
        {!Number.isNaN(display.pct) && (
          <span
            className={`font-mono ${display.pct > 0 ? "text-[var(--danger)]" : display.pct < 0 ? "text-[var(--good)]" : "text-[var(--muted-strong)]"}`}
          >
            {display.pct > 0 ? "+" : ""}
            {display.pct.toFixed(2)}%
          </span>
        )}
        <span className="ml-auto text-[var(--muted)]">
          <span className="text-[var(--muted)]">量 </span>
          <span className="font-mono">{formatVol(display.volume)}</span>
        </span>
      </div>
      <div
        ref={containerRef}
        className="h-[420px] w-full rounded-b-lg border border-[var(--line)] bg-[var(--surface)]"
      />
    </div>
  );
}

function round2(n: number): number {
  return Math.round((n + Number.EPSILON) * 100) / 100;
}
function round0(n: number): number {
  return Math.round(n);
}
function formatVol(n: number): string {
  if (!n || !isFinite(n)) return "0";
  if (n >= 1e8) return (n / 1e8).toFixed(2) + "亿";
  if (n >= 1e4) return (n / 1e4).toFixed(2) + "万";
  return String(Math.round(n));
}

function barTimeValue(b: StockV2DailyBar | StockV2MinuteBar, mode: "daily" | "minute"): string {
  if (mode === "minute") {
    return (b as StockV2MinuteBar).minuteAt;
  }
  return (b as StockV2DailyBar).tradeDate + "T00:00:00Z";
}

function chartTimestamp(b: StockV2DailyBar | StockV2MinuteBar, mode: "daily" | "minute"): UTCTimestamp {
  const seconds = Math.floor(new Date(barTimeValue(b, mode)).getTime() / 1000);
  // lightweight-charts has no timezone option and renders intraday labels as UTC.
  // A-share minute bars are China local wall-clock time, so shift only the chart coordinate.
  return (mode === "minute" ? seconds + 8 * 60 * 60 : seconds) as UTCTimestamp;
}

function barLabel(b: StockV2DailyBar | StockV2MinuteBar, mode: "daily" | "minute"): string {
  if (mode === "minute") {
    const raw = (b as StockV2MinuteBar).minuteAt;
    const d = new Date(raw);
    if (Number.isNaN(d.getTime())) return raw || "-";
    return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", timeZone: "Asia/Shanghai" });
  }
  return (b as StockV2DailyBar).tradeDate;
}
