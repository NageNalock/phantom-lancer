/**
 * Human-readable size formatting shared across the UI. Canonicalise on
 * base-1024 (IEC-style) rounding: four copies of this function existed
 * across the codebase with different unit names, zero-value semantics,
 * and upper bounds.
 *
 *   - `formatBytes`      → decimal unit names (B / KB / MB / GB / TB),
 *                          empty / non-positive → `emptyLabel` ("-" default)
 *   - `formatBytesIEC`   → IEC unit names (B / KiB / MiB / GiB / TiB),
 *                          empty / zero → `"0 B"` (system-update convention)
 *   - `formatBytesZero`  → decimal unit names, empty / non-positive → `"0 B"`
 *                          (used by Docker / Logs views that want to show 0
 *                          rather than a dash for empty containers/volumes)
 */

type FormatBytesOptions = {
  /** Use IEC (KiB/MiB) naming instead of decimal (KB/MB). */
  iec?: boolean;
  /** Value returned for `null`, `undefined`, zero or negative input. */
  emptyLabel?: string;
  /** Largest unit index to use (0=B, 1=K, 2=M, 3=G, 4=T). Defaults to 4 (TB). */
  maxUnit?: 0 | 1 | 2 | 3 | 4;
};

const DECIMAL_UNITS = ["B", "KB", "MB", "GB", "TB"] as const;
const IEC_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"] as const;

function formatBytesImpl(raw: number | null | undefined, opts: FormatBytesOptions = {}): string {
  const { iec = false, emptyLabel = "-", maxUnit = 4 } = opts;
  if (raw == null || !Number.isFinite(raw) || raw <= 0) {
    return emptyLabel;
  }
  const units = iec ? IEC_UNITS : DECIMAL_UNITS;
  let value = raw;
  let unit = 0;
  while (value >= 1024 && unit < maxUnit && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = value >= 10 || unit === 0 ? 0 : 1;
  return `${value.toFixed(digits)} ${units[unit]}`;
}

export function formatBytes(value?: number | null): string {
  return formatBytesImpl(value, { iec: false, emptyLabel: "-", maxUnit: 4 });
}

export function formatBytesIEC(value?: number | null): string {
  return formatBytesImpl(value, { iec: true, emptyLabel: "0 B", maxUnit: 2 });
}

export function formatBytesZero(value?: number | null): string {
  return formatBytesImpl(value, { iec: false, emptyLabel: "0 B", maxUnit: 4 });
}
