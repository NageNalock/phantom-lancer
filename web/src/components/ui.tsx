import { Children, cloneElement, forwardRef, isValidElement, useEffect, useId, useRef, useState } from "react";
import { CaretDown, X } from "@phosphor-icons/react";
import type { ButtonHTMLAttributes, DragEvent, KeyboardEvent, MouseEvent as ReactMouseEvent, ReactElement, ReactNode } from "react";
import type { Tone } from "../app/types";
import { shouldHandleQueryLinkClick } from "../hooks/useQueryParamState";

interface SubTabItem {
  id: string;
  label: ReactNode;
  badge?: ReactNode;
  href?: string;
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
  ariaLabel = "二级导航",
}: {
  tabs: SubTabItem[];
  activeId: string;
  onChange: (id: string) => void;
  rightSlot?: ReactNode;
  className?: string;
  ariaLabel?: string;
}) {
  return (
    <div className={`flex flex-wrap items-center gap-2 border-b border-[var(--line)] pb-2 ${className}`}>
      <nav aria-label={ariaLabel} className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        {tabs.map((tab) => {
          const active = tab.id === activeId;
          const tabClass = `flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm no-underline transition ${active ? "bg-[var(--surface-strong)] text-[var(--text)] shadow-[inset_0_-2px_0_var(--accent)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`;
          const content = (
            <>
              {tab.label}
              {tab.badge !== undefined && tab.badge !== null && tab.badge !== "" ? (
                <span className="ml-0.5 inline-flex items-center">{tab.badge}</span>
              ) : null}
            </>
          );
          return tab.href ? (
            <a
              aria-current={active ? "page" : undefined}
              className={tabClass}
              href={tab.href}
              key={tab.id}
              onClick={(event) => {
                if (!shouldHandleQueryLinkClick(event)) return;
                event.preventDefault();
                onChange(tab.id);
              }}
            >
              {content}
            </a>
          ) : (
            <button
              aria-pressed={active}
              className={tabClass}
              key={tab.id}
              onClick={() => onChange(tab.id)}
              type="button"
            >
              {content}
            </button>
          );
        })}
      </nav>
      {rightSlot ? <div className="flex flex-wrap items-center justify-end gap-2">{rightSlot}</div> : null}
    </div>
  );
}

export const Button = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { tone?: "neutral" | "primary" | "danger" }>(function Button({
  tone = "neutral",
  className = "",
  ...props
}, ref) {
  const toneClass = tone === "primary" ? "button-primary" : tone === "danger" ? "button-danger" : "";
  return <button {...props} className={`button ${toneClass} ${className}`} ref={ref} type={props.type || "button"} />;
});

export function Panel({ title, subtitle, actions, children, className = "" }: { title?: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
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

export function Pill({ children, tone = "neutral", className = "" }: { children: ReactNode; tone?: Tone; className?: string }) {
  const toneClass = tone === "good" ? "pill-good" : tone === "warn" ? "pill-warn" : tone === "danger" ? "pill-danger" : "";
  return <span className={`pill ${toneClass} ${className}`}>{children}</span>;
}

export function Metric({
  label,
  value,
  detail,
  tone = "neutral",
  href,
  onClick,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  tone?: Tone;
  href?: string;
  onClick?: (event: ReactMouseEvent<HTMLAnchorElement | HTMLButtonElement>) => void;
}) {
  const toneClass =
    tone === "good"
      ? "border-[rgba(18,132,79,0.2)] bg-[var(--good-soft)]"
      : tone === "warn"
        ? "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]"
        : tone === "danger"
          ? "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)]"
          : "border-[var(--line)] bg-[var(--surface-soft)]";
  const interactive = Boolean(href || onClick);
  const className = `min-h-24 rounded-lg border p-3 text-left ${toneClass} ${interactive ? "w-full no-underline transition hover:border-[var(--line-strong)]" : ""}`;
  const content = (
    <>
      <span className="muted text-xs">{label}</span>
      <strong className="mt-3 block break-words text-xl leading-tight">{value}</strong>
      {detail ? <small className="muted mt-1 block break-words text-xs leading-relaxed">{detail}</small> : null}
    </>
  );
  if (href) {
    return (
      <a className={className} href={href} onClick={onClick}>
        {content}
      </a>
    );
  }
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
      {withFieldControlDefaults(children, label)}
      {help ? <small className="muted text-xs">{help}</small> : null}
    </label>
  );
}

function withFieldControlDefaults(children: ReactNode, label: string): ReactNode {
  if (Children.count(children) !== 1) return children;
  const child = Children.only(children);
  if (!isValidElement(child) || typeof child.type !== "string") return children;
  if (child.type !== "input" && child.type !== "select" && child.type !== "textarea") return children;
  const control = child as ReactElement<Record<string, unknown>>;
  const extra: Record<string, unknown> = {};
  const defaultClassName = defaultFieldControlClass(child.type, control.props.type);
  const existingClassName = typeof control.props.className === "string" ? control.props.className : "";
  if (defaultClassName && !existingClassName) {
    extra.className = defaultClassName;
  }
  if (control.props.name === undefined) {
    extra.name = fieldNameFromLabel(label);
  }
  if (child.type === "input" && control.props.autoComplete === undefined && control.props.type !== "file" && control.props.type !== "checkbox" && control.props.type !== "radio") {
    extra.autoComplete = "off";
  }
  return Object.keys(extra).length ? cloneElement(control, extra) : children;
}

function defaultFieldControlClass(type: string, inputType: unknown): string {
  if (type === "select") return "select";
  if (type === "textarea") return "textarea";
  if (type !== "input") return "";
  const normalizedType = typeof inputType === "string" ? inputType : "text";
  if (["checkbox", "radio", "file", "hidden"].includes(normalizedType)) return "";
  return "input";
}

function fieldNameFromLabel(label: string): string {
  const normalized = label
    .trim()
    .replace(/[()（）/]+/g, " ")
    .replace(/\s+/g, "_")
    .toLowerCase();
  return normalized || "field";
}

export function ImageDropInput({
  accept = "image/png,image/jpeg,image/webp,image/gif",
  disabled = false,
  file,
  hint = "点击选择，或拖拽图片到这里",
  label = "上传图片",
  name,
  onFiles,
  resetAfterSelect = false,
}: {
  accept?: string;
  disabled?: boolean;
  file?: File;
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

  function openFilePicker() {
    if (!disabled) inputRef.current?.click();
  }

  useEffect(() => {
    if (file) {
      setFiles([file]);
      return;
    }
    if (inputRef.current) inputRef.current.value = "";
    setFileName("");
  }, [file]);

  return (
    <div
      aria-disabled={disabled}
      aria-label={label}
      className={`grid gap-1.5 rounded-lg border border-dashed p-3 text-left transition ${disabled ? "border-[var(--line)] bg-[var(--surface-soft)] opacity-60" : dragging ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line-strong)] bg-[var(--surface-soft)] hover:bg-[var(--surface-strong)]"}`}
      onClick={(event) => {
        if (disabled) return;
        event.preventDefault();
        event.stopPropagation();
        openFilePicker();
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
          event.stopPropagation();
          openFilePicker();
        }
      }}
      role="button"
      tabIndex={disabled ? -1 : 0}
    >
      <input
        accept={accept}
        className="hidden"
        disabled={disabled}
        name={name || "file"}
        onChange={(event) => {
          if (event.target.files) setFiles(event.target.files);
        }}
        onClick={(event) => event.stopPropagation()}
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
    <div aria-live="polite" className={`fixed right-5 bottom-5 z-50 max-w-sm rounded-lg border px-3 py-2 text-sm shadow-[var(--shadow)] ${toneClass}`} role="status">
      {message}
    </div>
  );
}

export interface DangerConfirmOptions {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  objectName?: ReactNode;
  impact?: ReactNode[];
  recovery?: ReactNode;
  confirmationText?: string;
  confirmationLabel?: string;
  confirmationPlaceholder?: string;
}

export function useDangerConfirm() {
  const [options, setOptions] = useState<DangerConfirmOptions | null>(null);
  const [typedConfirmation, setTypedConfirmation] = useState("");
  const dialogRef = useRef<HTMLElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const resolverRef = useRef<((confirmed: boolean) => void) | null>(null);
  const titleId = useId();
  const bodyId = useId();

  function close(confirmed: boolean) {
    const resolver = resolverRef.current;
    resolverRef.current = null;
    setOptions(null);
    setTypedConfirmation("");
    previousFocusRef.current?.focus?.();
    previousFocusRef.current = null;
    resolver?.(confirmed);
  }

  function confirmDanger(nextOptions: DangerConfirmOptions): Promise<boolean> {
    resolverRef.current?.(false);
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setTypedConfirmation("");
    setOptions(nextOptions);
    return new Promise((resolve) => {
      resolverRef.current = resolve;
    });
  }

  useEffect(() => {
    if (!options) return;
    const id = window.setTimeout(() => {
      const first = dialogRef.current?.querySelector<HTMLElement>("[data-dialog-initial-focus]");
      first?.focus();
    }, 0);
    return () => window.clearTimeout(id);
  }, [options]);

  function handleDialogKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      close(false);
      return;
    }
    if (event.key !== "Tab") return;

    const focusable = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
      ) || [],
    );
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  const confirmationMatches = !options?.confirmationText || typedConfirmation === options.confirmationText;
  const dialog = options ? (
    <div
      className="fixed inset-0 z-50 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.56)] p-4"
      onClick={() => close(false)}
    >
      <section
        aria-describedby={bodyId}
        aria-labelledby={titleId}
        aria-modal="true"
        className="w-full max-w-md overflow-hidden rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onKeyDown={handleDialogKeyDown}
        onClick={(event) => event.stopPropagation()}
        ref={dialogRef}
        role="dialog"
      >
        <div className="border-b border-[var(--line)] bg-[var(--danger-soft)] px-4 py-3">
          <h2 className="m-0 text-sm font-semibold text-[var(--danger)]" id={titleId}>{options.title}</h2>
          {options.objectName ? <p className="mono mt-1 mb-0 break-words text-xs text-[var(--muted-strong)]">{options.objectName}</p> : null}
        </div>
        <div className="grid gap-3 p-4 text-sm" id={bodyId}>
          <p className="m-0 leading-relaxed">{options.body}</p>
          {options.impact?.length ? (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <strong className="block text-xs text-[var(--muted-strong)]">影响范围</strong>
              <ul className="mt-2 mb-0 grid gap-1 pl-4 text-xs leading-relaxed text-[var(--muted-strong)]">
                {options.impact.map((item, index) => (
                  <li key={index}>{item}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {options.recovery ? (
            <p className="m-0 rounded-lg border border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] p-3 text-xs leading-relaxed text-[var(--warn)]">
              {options.recovery}
            </p>
          ) : null}
          {options.confirmationText ? (
            <label className="field">
              <span>{options.confirmationLabel || "输入确认文本"}</span>
              <input
                autoComplete="off"
                className="input mono"
                name="danger_confirmation"
                onChange={(event) => setTypedConfirmation(event.target.value)}
                placeholder={options.confirmationPlaceholder || options.confirmationText}
                value={typedConfirmation}
              />
              <small className="muted text-xs">请输入 <span className="mono">{options.confirmationText}</span> 以继续。</small>
            </label>
          ) : null}
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-4 py-3">
          <Button data-dialog-initial-focus onClick={() => close(false)}>{options.cancelLabel || "取消"}</Button>
          <Button disabled={!confirmationMatches} onClick={() => close(true)} tone="danger">
            {options.confirmLabel || "确认执行"}
          </Button>
        </div>
      </section>
    </div>
  ) : null;

  return { confirmDanger, dangerConfirmDialog: dialog };
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
  name,
  disabled = false,
}: {
  checked: boolean;
  label: ReactNode;
  onChange: (checked: boolean) => void;
  variant?: "default" | "row";
  className?: string;
  inputClassName?: string;
  name?: string;
  disabled?: boolean;
}) {
  const disabledClass = disabled ? "opacity-60 pointer-events-none" : "";
  if (variant === "row") {
    const tone = checked ? "border-[rgba(18,132,79,0.22)] bg-[var(--good-soft)]" : "border-[var(--line)] bg-[var(--surface)]";
    return (
      <label className={`grid min-h-9 grid-cols-[auto_minmax(0,1fr)] items-center gap-3 rounded-lg border px-3 py-2 text-sm ${tone} ${disabledClass} ${className}`}>
        <input
          checked={checked}
          className={`h-4 w-4 accent-[var(--accent)] ${inputClassName}`}
          disabled={disabled}
          name={name}
          onChange={(event) => onChange(event.target.checked)}
          type="checkbox"
        />
        <span className="min-w-0">{label}</span>
      </label>
    );
  }
  return (
    <label className={`flex min-h-10 items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-sm ${disabledClass} ${className}`}>
      <span>{label}</span>
      <input
        checked={checked}
        className={inputClassName}
        disabled={disabled}
        name={name}
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
  name,
}: {
  checked: boolean;
  children: ReactNode;
  onChange: (checked: boolean) => void;
  className?: string;
  inputClassName?: string;
  size?: "sm" | "xs";
  align?: "center" | "start";
  name?: string;
}) {
  const sizeClass = size === "xs" ? "text-xs text-[var(--muted-strong)]" : "text-sm";
  const alignClass = align === "start" ? "items-start" : "items-center";
  return (
    <label className={`inline-flex ${alignClass} gap-2 ${sizeClass} ${className}`}>
      <input
        checked={checked}
        className={`accent-[var(--accent)] mt-0.5 ${inputClassName}`}
        name={name}
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
      className={`overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] ${className}`}
      open={defaultOpen}
    >
      <summary
        className="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-3 select-none transition hover:bg-[var(--surface-strong)]"
      >
        <div className="flex flex-col gap-0.5">
          <span className="font-medium text-sm">{title}</span>
          {subtitle ? <span className="text-xs text-[var(--muted)]">{subtitle}</span> : null}
        </div>
        <CaretDown aria-hidden className="collapsible-caret shrink-0 text-[var(--muted)] transition-transform duration-200" size={16} weight="regular" />
      </summary>
      <div className="grid gap-4 border-t border-[var(--line)] px-3 py-3">
        {children}
      </div>
    </details>
  );
}

/**
 * Drawer is a right-side slide-over panel for create/edit/inspect flows that
 * are too dense for a centered modal but should not live permanently on the
 * page (e.g. strategy editor, object inspector). It is the Quiet Workbench
 * counterpart to the centered Dialog used by simpler forms.
 *
 * The caller renders it conditionally ({open && <Drawer/>}); the panel slides
 * in on mount. Close on overlay click or Escape. Keep drawer content scoped to
 * one object — list/table state stays in the page, the drawer only carries the
 * currently edited or inspected object.
 */
export function Drawer({
  title,
  subtitle,
  onClose,
  children,
  footer,
  width = 480,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
}) {
  const [shown, setShown] = useState(false);
  const titleId = useId();
  const panelRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const raf = requestAnimationFrame(() => setShown(true));
    function onKey(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") {
        const dialogs = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"]'));
        if (dialogs[dialogs.length - 1] !== panelRef.current) return;
        event.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-[rgba(16,18,22,0.45)]" onClick={onClose}>
      <section
        aria-labelledby={titleId}
        aria-modal="true"
        className={`flex h-full flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] transition-transform duration-200 ease-out ${shown ? "translate-x-0" : "translate-x-full"}`}
        onClick={(event) => event.stopPropagation()}
        ref={panelRef}
        role="dialog"
        style={{ width: `min(${width}px, 100vw)` }}
      >
        <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
          <div className="min-w-0">
            <h2 className="m-0 text-sm font-semibold" id={titleId}>{title}</h2>
            {subtitle ? <p className="muted mt-1 mb-0 text-xs">{subtitle}</p> : null}
          </div>
          <Button aria-label="关闭" onClick={onClose}>
            <X size={14} />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-5">{children}</div>
        {footer ? <div className="flex flex-wrap items-center justify-end gap-2 border-t border-[var(--line)] px-5 py-3">{footer}</div> : null}
      </section>
    </div>
  );
}
