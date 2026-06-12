import { useRef, useState } from "react";
import type { ButtonHTMLAttributes, DragEvent, ReactNode } from "react";
import type { Tone } from "../app/types";

interface SubTabItem {
  id: string;
  label: ReactNode;
  badge?: ReactNode;
}

/**
 * SubTabs is the canonical secondary navigation bar used across all feature
 * views (Codex, Images, Docker, etc.). It unifies three previously divergent
 * styles (underline tabs, segmented controls, in-panel mini-tabs) into one
 * consistent Quiet Workbench pattern.
 *
 * Visual rules:
 *   - Horizontally scrollable on overflow
 *   - Selected: surface-strong background + 2px bottom accent bar
 *   - Inactive: muted-strong text, soft hover background
 *   - Right slot for status pills / actions (aligned right)
 */
export function SubTabs({
  tabs,
  activeId,
  onChange,
  rightSlot,
  className = "",
}: {
  tabs: SubTabItem[];
  activeId: string;
  onChange: (id: string) => void;
  rightSlot?: ReactNode;
  className?: string;
}) {
  return (
    <div className={`flex flex-wrap items-center gap-2 border-b border-[var(--line)] pb-2 ${className}`}>
      <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        {tabs.map((tab) => {
          const active = tab.id === activeId;
          return (
            <button
              aria-pressed={active}
              className={`flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm transition ${active ? "bg-[var(--surface-strong)] text-[var(--text)] shadow-[inset_0_-2px_0_var(--accent)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`}
              key={tab.id}
              onClick={() => onChange(tab.id)}
              type="button"
            >
              {tab.label}
              {tab.badge !== undefined && tab.badge !== null && tab.badge !== "" ? (
                <span className="ml-0.5 inline-flex items-center">{tab.badge}</span>
              ) : null}
            </button>
          );
        })}
      </div>
      {rightSlot ? <div className="flex flex-wrap items-center justify-end gap-2">{rightSlot}</div> : null}
    </div>
  );
}

export function Button({
  tone = "neutral",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: "neutral" | "primary" | "danger" }) {
  const toneClass = tone === "primary" ? "button-primary" : tone === "danger" ? "button-danger" : "";
  return <button {...props} className={`button ${toneClass} ${className}`} type={props.type || "button"} />;
}

export function Panel({ title, subtitle, actions, children, className = "" }: { title?: string; subtitle?: string; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`panel ${className}`}>
      {title || actions ? (
        <div className="panel-header">
          <div>
            {title ? <h2 className="m-0 text-sm font-semibold">{title}</h2> : null}
            {subtitle ? <p className="muted mt-1 mb-0 text-xs">{subtitle}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
        </div>
      ) : null}
      <div className="panel-body">{children}</div>
    </section>
  );
}

export function Pill({ children, tone = "neutral" }: { children: ReactNode; tone?: Tone }) {
  const toneClass = tone === "good" ? "pill-good" : tone === "warn" ? "pill-warn" : tone === "danger" ? "pill-danger" : "";
  return <span className={`pill ${toneClass}`}>{children}</span>;
}

export function Metric({ label, value, detail, tone = "neutral", onClick }: { label: string; value: ReactNode; detail?: ReactNode; tone?: Tone; onClick?: () => void }) {
  const toneClass =
    tone === "good"
      ? "border-[rgba(18,132,79,0.2)] bg-[var(--good-soft)]"
      : tone === "warn"
        ? "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]"
        : tone === "danger"
          ? "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)]"
          : "border-[var(--line)] bg-[var(--surface-soft)]";
  const className = `min-h-24 rounded-lg border p-3 text-left ${toneClass} ${onClick ? "w-full transition hover:border-[var(--line-strong)]" : ""}`;
  const content = (
    <>
      <span className="muted text-xs">{label}</span>
      <strong className="mt-3 block break-words text-xl leading-tight">{value}</strong>
      {detail ? <small className="muted mt-1 block break-words text-xs leading-relaxed">{detail}</small> : null}
    </>
  );
  if (onClick) {
    return (
      <button className={className} onClick={onClick} type="button">
        {content}
      </button>
    );
  }
  return (
    <div className={className}>
      {content}
    </div>
  );
}

export function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="grid min-h-36 place-items-center rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4 text-center">
      <div>
        <strong className="block text-sm">{title}</strong>
        <p className="muted mt-1 mb-0 text-sm">{body}</p>
      </div>
    </div>
  );
}

export function Field({ label, children, help }: { label: string; children: ReactNode; help?: string }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {help ? <small className="muted text-xs">{help}</small> : null}
    </label>
  );
}

export function ImageDropInput({
  accept = "image/png,image/jpeg,image/webp,image/gif",
  disabled = false,
  hint = "点击选择，或拖拽图片到这里",
  label = "上传图片",
  name,
  onFiles,
  resetAfterSelect = false,
}: {
  accept?: string;
  disabled?: boolean;
  hint?: string;
  label?: string;
  name?: string;
  onFiles?: (files: File[]) => void;
  resetAfterSelect?: boolean;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [dragging, setDragging] = useState(false);
  const [fileName, setFileName] = useState("");

  function setFiles(files: FileList | File[]) {
    const items = Array.from(files).filter((file) => file.type.startsWith("image/"));
    const file = items[0];
    if (!file) return;
    const transfer = new DataTransfer();
    transfer.items.add(file);
    if (inputRef.current) inputRef.current.files = transfer.files;
    setFileName(file.name);
    onFiles?.([file]);
    if (resetAfterSelect && inputRef.current) inputRef.current.value = "";
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    event.stopPropagation();
    setDragging(false);
    if (disabled) return;
    setFiles(event.dataTransfer.files);
  }

  return (
    <div
      aria-disabled={disabled}
      className={`grid gap-1.5 rounded-lg border border-dashed p-3 text-left transition ${disabled ? "border-[var(--line)] bg-[var(--surface-soft)] opacity-60" : dragging ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line-strong)] bg-[var(--surface-soft)] hover:bg-[var(--surface-strong)]"}`}
      onClick={() => {
        if (!disabled) inputRef.current?.click();
      }}
      onDragEnter={(event) => {
        event.preventDefault();
        if (!disabled) setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDragOver={(event) => event.preventDefault()}
      onDrop={handleDrop}
      onKeyDown={(event) => {
        if (!disabled && (event.key === "Enter" || event.key === " ")) {
          event.preventDefault();
          inputRef.current?.click();
        }
      }}
      role="button"
      tabIndex={disabled ? -1 : 0}
    >
      <input
        accept={accept}
        className="hidden"
        disabled={disabled}
        name={name}
        onChange={(event) => {
          if (event.target.files) setFiles(event.target.files);
        }}
        ref={inputRef}
        type="file"
      />
      <span className="text-xs font-semibold text-[var(--muted-strong)]">{label}</span>
      <span className="text-xs text-[var(--muted)]">{fileName || hint}</span>
    </div>
  );
}

export function Notice({ children, tone = "warn" }: { children: ReactNode; tone?: "warn" | "danger" }) {
  const toneClass = tone === "danger" ? "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] text-[var(--danger)]" : "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] text-[var(--warn)]";
  return <div className={`rounded-lg border p-3 text-sm ${toneClass}`}>{children}</div>;
}

export function ContextList({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <div className="grid gap-0">
      {items.map(([label, value]) => (
        <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-3 border-b border-[var(--line)] py-2 last:border-b-0 max-sm:grid-cols-1 max-sm:gap-1" key={label}>
          <span className="muted text-xs">{label}</span>
          <strong className="min-w-0 break-words text-sm font-medium">{value}</strong>
        </div>
      ))}
    </div>
  );
}

export function Toast({ message, tone }: { message: string; tone: Tone }) {
  const toneClass = tone === "danger" ? "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] text-[var(--danger)]" : "border-[var(--line)] bg-[var(--surface)] text-[var(--text)]";
  return (
    <div className={`fixed right-5 bottom-5 z-50 max-w-sm rounded-lg border px-3 py-2 text-sm shadow-[var(--shadow)] ${toneClass}`} role="status">
      {message}
    </div>
  );
}

/**
 * Toggle is the canonical checkbox row used by settings panels. It renders as
 * a full-width bordered row — use it for primary settings that stand on their
 * own line.
 *
 * For compact inline checkboxes (filters, secondary options, confirmations),
 * use CheckLabel instead. Never write a bare `<input type="checkbox">` in
 * business components — always go through Toggle or CheckLabel.
 *
 * Variants:
 *   - `variant="default"` — label on the left, checkbox on the right.
 *                            Standard form setting row.
 *   - `variant="row"`     — checkbox on the left, label on the right.
 *                            Checked rows take the good/soft tone. Use for
 *                            list/grid rows where the toggle IS the row
 *                            (e.g. account enablement grids).
 */
export function Toggle({
  checked,
  label,
  onChange,
  variant = "default",
  className = "",
  inputClassName = "",
}: {
  checked: boolean;
  label: ReactNode;
  onChange: (checked: boolean) => void;
  variant?: "default" | "row";
  className?: string;
  inputClassName?: string;
}) {
  if (variant === "row") {
    const tone = checked ? "border-[rgba(18,132,79,0.22)] bg-[var(--good-soft)]" : "border-[var(--line)] bg-[var(--surface)]";
    return (
      <label className={`grid min-h-9 grid-cols-[auto_minmax(0,1fr)] items-center gap-3 rounded-lg border px-3 py-2 text-sm ${tone} ${className}`}>
        <input
          checked={checked}
          className={`h-4 w-4 accent-[var(--accent)] ${inputClassName}`}
          onChange={(event) => onChange(event.target.checked)}
          type="checkbox"
        />
        <span className="min-w-0">{label}</span>
      </label>
    );
  }
  return (
    <label className={`flex min-h-10 items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-sm ${className}`}>
      <span>{label}</span>
      <input
        checked={checked}
        className={inputClassName}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
    </label>
  );
}

/**
 * CheckLabel is a compact inline checkbox + label for secondary settings,
 * filters, and confirmation checks where the full-width Toggle row is too heavy.
 *
 * Use when:
 *   - The checkbox is an inline option next to other controls
 *   - It's a confirmation before a destructive action
 *   - It's a minor filter/option in a sidebar or toolbar
 *
 * Do NOT use for primary settings in settings panels — use Toggle instead.
 */
export function CheckLabel({
  checked,
  children,
  onChange,
  className = "",
  inputClassName = "",
  size = "sm",
  align = "center",
}: {
  checked: boolean;
  children: ReactNode;
  onChange: (checked: boolean) => void;
  className?: string;
  inputClassName?: string;
  size?: "sm" | "xs";
  align?: "center" | "start";
}) {
  const sizeClass = size === "xs" ? "text-xs text-[var(--muted-strong)]" : "text-sm";
  const alignClass = align === "start" ? "items-start" : "items-center";
  return (
    <label className={`inline-flex ${alignClass} gap-2 ${sizeClass} ${className}`}>
      <input
        checked={checked}
        className={`accent-[var(--accent)] mt-0.5 ${inputClassName}`}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
      <span>{children}</span>
    </label>
  );
}

/**
 * CollapsibleSection wraps arbitrary children in a native <details> container
 * so a group of rarely-touched advanced fields can live under a clickable
 * header without the caller managing any state.
 *
 * Defaults to collapsed (do not overwhelm the user with knobs they do not
 * need).  The disclosure triangle rotates 180° on open via a CSS rule in
 * styles.css so we do not depend on arbitrary Tailwind variants.
 */
export function CollapsibleSection({
  title,
  subtitle,
  children,
  defaultOpen = false,
  className = "",
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  children: ReactNode;
  defaultOpen?: boolean;
  className?: string;
}) {
  return (
    <details
      className={`rounded-lg border border-[var(--border)] bg-[var(--surface-soft)] overflow-hidden ${className}`}
      open={defaultOpen}
    >
      <summary
        className="flex items-center justify-between gap-3 px-3 py-3 cursor-pointer select-none list-none hover:bg-[var(--surface-hover)] transition"
      >
        <div className="flex flex-col gap-0.5">
          <span className="font-medium text-sm">{title}</span>
          {subtitle ? <span className="text-xs text-[var(--muted)]">{subtitle}</span> : null}
        </div>
        <svg
          className="collapsible-caret text-[var(--muted)] shrink-0 transition-transform duration-200"
          height="16"
          viewBox="0 0 24 24"
          width="16"
          xmlns="http://www.w3.org/2000/svg"
        >
          <path
            d="M6 9l6 6 6-6"
            fill="none"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="2"
          />
        </svg>
      </summary>
      <div className="grid gap-4 border-t border-[var(--border)] px-3 py-3">
        {children}
      </div>
    </details>
  );
}
