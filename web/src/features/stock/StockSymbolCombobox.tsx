import { CaretDown, MagnifyingGlass } from "@phosphor-icons/react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { AppActions } from "../../app/App";
import type { StockInstrument, StockInstrumentSearchResponse } from "../../app/types";
import { Field, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";

export function StockSymbolCombobox({
  actions,
  label = "股票",
  placeholder = "输入代码 / 名称 / 拼音首字母",
  allowFreeInput = true,
  initialValue,
  recent = [],
  required = false,
  onSelect,
}: {
  actions: AppActions;
  label?: string;
  placeholder?: string;
  allowFreeInput?: boolean;
  initialValue?: Pick<StockInstrument, "symbol" | "market" | "name">;
  recent?: StockInstrument[];
  required?: boolean;
  onSelect?: (instrument: StockInstrument) => void;
}) {
  const [query, setQuery] = useState(displayInstrument(initialValue));
  const [selected, setSelected] = useState<StockInstrument | undefined>(initialValue);
  const [items, setItems] = useState<StockInstrument[]>([]);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [loading, setLoading] = useState(false);
  const [composing, setComposing] = useState(false);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);

  const free = useMemo(() => parseFreeStockInput(query, initialValue?.market || "SH"), [query, initialValue?.market]);
  const visibleItems = query.trim() ? items : recent.slice(0, 6);
  const hiddenSymbol = selected?.symbol || (allowFreeInput ? free.symbol : "");
  const hiddenMarket = selected?.market || (allowFreeInput ? free.market : "");
  const hiddenName = selected?.name || (allowFreeInput ? free.name : "");

  useEffect(() => {
    if (composing) return;
    const raw = query.trim();
    if (!raw) {
      setItems([]);
      setError("");
      return;
    }
    const timer = window.setTimeout(() => {
      void (async () => {
        setLoading(true);
        setError("");
        try {
          const params = new URLSearchParams({ q: raw, pageSize: "8", sort: "relevance" });
          const result = await actions.api<StockInstrumentSearchResponse>(`/api/stock/instruments/search?${params.toString()}`);
          setItems(result.items || []);
          setActiveIndex(0);
          setOpen(true);
        } catch (err) {
          setError(friendlyError(err));
        } finally {
          setLoading(false);
        }
      })();
    }, 200);
    return () => window.clearTimeout(timer);
  }, [actions, composing, query]);

  function choose(item: StockInstrument) {
    setSelected(item);
    setQuery(displayInstrument(item));
    setOpen(false);
    onSelect?.(item);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (!open && (event.key === "ArrowDown" || event.key === "Enter")) {
      setOpen(true);
      return;
    }
    if (!visibleItems.length) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveIndex((idx) => Math.min(idx + 1, visibleItems.length - 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex((idx) => Math.max(idx - 1, 0));
    } else if (event.key === "Enter" && open) {
      event.preventDefault();
      choose(visibleItems[activeIndex] || visibleItems[0]);
    } else if (event.key === "Escape") {
      setOpen(false);
    }
  }

  return (
    <div className="relative">
      <Field label={label} help={error || (allowFreeInput ? "未命中时可保留手填代码，市场可用 600519.SH 这类后缀指定。" : undefined)}>
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center rounded-md border border-[var(--line)] bg-[var(--surface)] focus-within:border-[var(--line-strong)]">
          <MagnifyingGlass className="ml-2 text-[var(--muted-strong)]" size={15} />
          <input
            aria-autocomplete="list"
            aria-expanded={open}
            autoComplete="off"
            className="min-h-9 min-w-0 border-0 bg-transparent px-2 text-sm outline-none"
            onBlur={() => window.setTimeout(() => setOpen(false), 120)}
            onChange={(event) => {
              setSelected(undefined);
              setQuery(event.target.value);
              setOpen(true);
            }}
            onCompositionEnd={() => setComposing(false)}
            onCompositionStart={() => setComposing(true)}
            onFocus={() => setOpen(true)}
            onKeyDown={handleKeyDown}
            placeholder={placeholder}
            ref={inputRef}
            required={required && !hiddenSymbol}
            value={query}
          />
          <button className="grid min-h-9 w-9 place-items-center text-[var(--muted-strong)]" onMouseDown={(event) => event.preventDefault()} onClick={() => setOpen((v) => !v)} type="button">
            <CaretDown size={14} />
          </button>
        </div>
      </Field>
      <input name="symbol" type="hidden" value={hiddenSymbol} />
      <input name="market" type="hidden" value={hiddenMarket} />
      <input name="name" type="hidden" value={hiddenName} />
      {open ? (
        <div className="absolute z-20 mt-1 max-h-72 w-full overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-1 shadow-lg">
          {loading ? <div className="muted px-3 py-2 text-xs">搜索中</div> : null}
          {!loading && visibleItems.length === 0 ? (
            <div className="muted px-3 py-2 text-xs">{allowFreeInput ? "无匹配结果，将按手填代码提交。" : "无匹配结果"}</div>
          ) : null}
          {visibleItems.map((item, index) => (
            <button
              className={`grid w-full grid-cols-[88px_minmax(0,1fr)_auto] items-center gap-2 rounded px-2 py-2 text-left text-sm ${index === activeIndex ? "bg-[var(--surface-strong)]" : "hover:bg-[var(--surface-soft)]"}`}
              key={`${item.symbol || ""}-${item.market || ""}`}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => choose(item)}
              type="button"
            >
              <span className="mono">{item.symbol || "-"}.{item.market || "-"}</span>
              <span className="min-w-0 truncate">{item.name || item.symbol}</span>
              <span className="flex items-center gap-1">
                {item.industry ? <Pill>{item.industry}</Pill> : null}
                <Pill tone={item.status === "delisted" ? "warn" : item.quality === "fresh" ? "good" : "neutral"}>{item.quality || item.status || "unknown"}</Pill>
              </span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function displayInstrument(item?: Pick<StockInstrument, "symbol" | "market" | "name">): string {
  if (!item?.symbol) return "";
  const suffix = item.market ? `.${item.market}` : "";
  return `${item.symbol}${suffix}${item.name ? ` ${item.name}` : ""}`;
}

function parseFreeStockInput(value: string, fallbackMarket: string): { symbol: string; market: string; name: string } {
  const first = value.trim().split(/\s+/)[0] || "";
  const match = first.match(/^([A-Za-z0-9]+)(?:[.:-](SH|SZ|BJ))?$/i);
  const symbol = (match?.[1] || first).toUpperCase();
  const market = (match?.[2] || marketFromSymbol(symbol) || fallbackMarket || "SH").toUpperCase();
  const name = value.trim().slice(first.length).trim();
  return { symbol, market, name };
}

function marketFromSymbol(symbol: string): string {
  if (/^920/.test(symbol)) return "BJ";
  if (/^[69]/.test(symbol)) return "SH";
  if (/^[03]/.test(symbol)) return "SZ";
  if (/^[48]/.test(symbol)) return "BJ";
  return "";
}
