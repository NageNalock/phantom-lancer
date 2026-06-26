import { useEffect, useRef, useState } from "react";
import { MagnifyingGlass } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { StockV2Instrument } from "../../app/types";
import { Pill } from "../../components/ui";
import { stockV2InstrumentTypeLabel } from "../../domain/labels";

// 标的引用：表单里选中的标的只取展示与提交所需的最小字段。
export interface SymbolRef {
  symbol: string;
  market?: string;
  name?: string;
}

// SymbolPicker：基于 /api/stockv2/instruments/search 的标的搜索选择器，
// debounce 200ms，输出 {symbol, market, name}。策略表单与策略生成表单复用。
export function SymbolPicker({
  actions,
  value,
  onChange,
}: {
  actions: AppActions;
  value: SymbolRef;
  onChange: (ref: SymbolRef) => void;
}) {
  const [query, setQuery] = useState(value.symbol && value.name ? `${value.symbol} · ${value.name}` : value.symbol || "");
  const [results, setResults] = useState<StockV2Instrument[]>([]);
  const [open, setOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    if (!query.trim()) {
      setResults([]);
      return;
    }
    timerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const res = await actions.api<{ items: StockV2Instrument[] }>(
          `/api/stockv2/instruments/search?q=${encodeURIComponent(query)}&limit=20`,
        );
        setResults(res.items || []);
        setOpen(true);
      } catch {
        setResults([]);
      } finally {
        setSearching(false);
      }
    }, 200);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [query, actions]);

  function pick(inst: StockV2Instrument) {
    onChange({ symbol: inst.symbol, market: inst.market, name: inst.name });
    setQuery(`${inst.symbol} · ${inst.name || ""}`);
    setOpen(false);
  }

  return (
    <div className="relative" ref={wrapRef}>
      <div className="relative">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
        <input
          type="text"
          className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
          placeholder="输入代码或名称搜索"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => {
            if (results.length) setOpen(true);
          }}
        />
      </div>
      {open ? (
        <div className="absolute left-0 right-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]">
          {searching ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">搜索中…</div>
          ) : results.length === 0 ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">{query ? "未找到匹配的标的" : "输入关键词开始搜索"}</div>
          ) : (
            results.map((inst) => (
              <button
                key={inst.id}
                type="button"
                onClick={() => pick(inst)}
                className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-[var(--surface-soft)]"
              >
                <span className="font-mono">{inst.symbol}</span>
                <span className="mx-2 min-w-0 truncate text-[var(--muted)]">{inst.name}</span>
                <span className="flex shrink-0 items-center gap-1">
                  <Pill tone="neutral" className="text-xs">
                    {inst.market === "SH" ? "沪" : inst.market === "SZ" ? "深" : inst.market === "BJ" ? "北" : inst.market}
                  </Pill>
                  <Pill tone="neutral" className="text-xs">
                    {stockV2InstrumentTypeLabel(inst.instrumentType)}
                  </Pill>
                </span>
              </button>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}
