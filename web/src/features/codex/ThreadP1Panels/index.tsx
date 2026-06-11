import { useEffect, useState } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexThread } from "../../../app/types";
import { Button } from "../../../components/ui";
import { BrowserPane } from "./BrowserPane";
import { CommandPane } from "./CommandPane";
import { ReviewPane } from "./ReviewPane";

type PanelId = "review" | "commands" | "browser";

const PANELS: Array<[PanelId, string]> = [
  ["review", "Review"],
  ["commands", "Commands"],
  ["browser", "Preview"],
];

export function ThreadP1Panels({ actions, thread, onRefresh }: { actions: AppActions; thread: CodexThread; onRefresh: () => void }) {
  const [panel, setPanel] = useState<PanelId>("review");
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  return (
    <>
      <Button className="h-8 min-h-8 px-2 text-xs" onClick={() => setOpen(true)}>
        Tools
      </Button>
      {open ? (
        <div className="fixed inset-0 z-40">
          <button aria-label="关闭 Codex tools 抽屉" className="absolute inset-0 h-full w-full bg-black/[0.04]" onClick={() => setOpen(false)} type="button" />
          <aside aria-labelledby="codexToolsTitle" aria-modal="true" className="absolute top-4 right-4 bottom-4 flex w-[min(720px,calc(100vw-2rem))] flex-col overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]" role="dialog">
            <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] px-4 py-3">
              <div className="min-w-0">
                <h2 className="m-0 text-sm font-semibold" id="codexToolsTitle">Codex tools</h2>
                <p className="muted mt-1 mb-0 text-xs">低频 review、命令和预览工具</p>
              </div>
              <Button className="h-8 min-h-8 px-2 text-xs" onClick={() => setOpen(false)}>
                关闭
              </Button>
            </div>
            <div className="flex flex-wrap gap-1 border-b border-[var(--line)] p-2">
              {PANELS.map(([id, label]) => (
                <button className={`rounded-md px-2 py-1 text-xs ${panel === id ? "bg-[var(--surface-strong)] text-[var(--text)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`} key={id} onClick={() => setPanel(id)} type="button">
                  {label}
                </button>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-3">
              {panel === "review" ? <ReviewPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
              {panel === "commands" ? <CommandPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
              {panel === "browser" ? <BrowserPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
            </div>
          </aside>
        </div>
      ) : null}
    </>
  );
}
