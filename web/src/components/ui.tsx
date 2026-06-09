import type { ButtonHTMLAttributes, ReactNode } from "react";
import type { Tone } from "../app/types";

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
