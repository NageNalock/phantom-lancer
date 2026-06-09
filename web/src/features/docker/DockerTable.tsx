import type { ReactNode } from "react";
import { EmptyState } from "../../components/ui";

export interface DockerTableRow {
  key: string;
  cells: ReactNode[];
}

export function DockerTable({ headers, rows, empty, loading }: { headers: string[]; rows: DockerTableRow[]; empty: string; loading: boolean }) {
  if (!rows.length) {
    return <EmptyState title={empty} body={loading ? "正在加载。" : "当前没有可显示的条目。"} />;
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left">
        <thead>
          <tr className="border-b border-[var(--line)]">
            {headers.map((header) => (
              <th className="muted px-2 py-2 text-xs font-medium" key={header}>
                {header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr className="border-b border-[var(--line)] last:border-b-0" key={row.key}>
              {row.cells.map((cell, index) => (
                <td className="px-2 py-2 align-top" key={index}>
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
