import type { ReactNode } from "react";
import { EmptyState } from "../../components/ui";

export interface DockerTableColumn {
  header: ReactNode;
  width?: string;
  className?: string;
  cellClassName?: string;
}

export interface DockerTableRow {
  key: string;
  cells: ReactNode[];
}

export function DockerTable({
  columns,
  headers,
  rows,
  empty,
  loading,
  onSelectRow,
  selectedKey,
}: {
  columns?: DockerTableColumn[];
  headers?: ReactNode[];
  rows: DockerTableRow[];
  empty: string;
  loading: boolean;
  onSelectRow?: (row: DockerTableRow) => void;
  selectedKey?: string;
}) {
  if (!rows.length) {
    if (!empty) {
      return null;
    }
    return <EmptyState title={empty} body={loading ? "正在加载。" : "当前没有可显示的条目。"} />;
  }
  const resolvedColumns: DockerTableColumn[] = columns || (headers || []).map((header) => ({ header }));
  return (
    <div className="overflow-x-auto rounded-lg border border-[var(--line)]">
      <table className="min-w-full table-fixed border-collapse text-left">
        <thead>
          <tr className="border-b border-[var(--line)]">
            {resolvedColumns.map((column, index) => (
              <th className={`muted px-2 py-2 text-xs font-medium ${column.className || ""}`} key={index} style={column.width ? { width: column.width } : undefined}>
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              className={`border-b border-[var(--line)] last:border-b-0 ${onSelectRow ? "cursor-pointer transition hover:bg-[var(--surface-soft)]" : ""} ${selectedKey === row.key ? "bg-[rgba(59,130,246,0.06)]" : ""}`}
              key={row.key}
              onClick={onSelectRow ? () => onSelectRow(row) : undefined}
            >
              {row.cells.map((cell, index) => (
                <td className={`min-w-0 px-2 py-2 align-top ${resolvedColumns[index]?.cellClassName || ""}`} key={index}>
                  <div className="min-w-0">{cell}</div>
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function DockerValue({
  value,
  copyValue,
  mono = true,
  clamp = true,
  className = "",
}: {
  value: string;
  copyValue?: string;
  mono?: boolean;
  clamp?: boolean;
  className?: string;
}) {
  const display = value || "-";
  async function copy() {
    if (!copyValue && !value) return;
    try {
      await navigator.clipboard?.writeText(copyValue || value);
    } catch {
      // Clipboard access may be unavailable on non-secure origins.
    }
  }
  return (
    <span className={`group/value flex min-w-0 items-center gap-1.5 ${className}`}>
      <span className={`${mono ? "mono" : ""} min-w-0 text-xs ${clamp ? "truncate" : "break-all"}`} title={display}>
        {display}
      </span>
      {copyValue || value ? (
        <button
          aria-label="复制"
          className="shrink-0 rounded border border-[var(--line)] px-1.5 py-0.5 text-[10px] text-[var(--muted-strong)] opacity-0 transition hover:bg-[var(--surface-strong)] group-hover/value:opacity-100 focus:opacity-100"
          onClick={() => void copy()}
          type="button"
        >
          复制
        </button>
      ) : null}
    </span>
  );
}
