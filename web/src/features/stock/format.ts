export function snippet(value?: string, max = 1000): string {
  const text = String(value || "").trim();
  if (text.length <= max) return text || "-";
  return `${text.slice(0, max)}...`;
}

export function text(form: FormData, key: string): string {
  return String(form.get(key) || "").trim();
}

export function number(form: FormData, key: string): number {
  const parsed = Number(text(form, key));
  return Number.isFinite(parsed) ? parsed : 0;
}

export function optionalNumber(form: FormData, key: string): number | null {
  const raw = text(form, key);
  if (raw === "") return 0;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

export function percentInput(form: FormData, key: string, fallback: number): number {
  const raw = number(form, key);
  return (raw || fallback) / 100;
}

export function money(value?: number): string {
  return new Intl.NumberFormat("zh-CN", { style: "currency", currency: "CNY", maximumFractionDigits: 2 }).format(Number(value || 0));
}

export function price(value?: number): string {
  if (!value) return "-";
  return Number(value).toFixed(3);
}

export function percent(value?: number): string {
  if (!value) return "0%";
  return `${(Number(value) * 100).toFixed(1)}%`;
}

export function numberText(value?: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(Number(value || 0));
}

export function durationText(seconds?: number): string {
  const value = Number(seconds || 0);
  if (value <= 0) return "-";
  if (value < 60) return `${value}s`;
  if (value < 3600) return `${Math.round(value / 60)}m`;
  return `${Math.round(value / 3600)}h`;
}

export function directionLabel(value?: string): string {
  return ({ buy: "买入", add: "加仓", hold: "持有", reduce: "减仓", sell: "卖出", watch: "观察" }[value || ""] || value || "观察");
}

export function operationLabel(value?: string): string {
  return directionLabel(value);
}

export function marketSessionLabel(value?: string): string {
  return ({ call_auction: "集合竞价", continuous_morning: "上午交易", lunch_break: "午休", continuous_afternoon: "下午交易", post_close: "盘后", holiday_or_weekend: "非交易日", closed: "闭市" }[value || ""] || value || "未知");
}
