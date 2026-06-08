import { useState } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexThread } from "../../../app/types";
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

  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
      <div className="flex flex-wrap gap-1 border-b border-[var(--line)] p-2">
        {PANELS.map(([id, label]) => (
          <button className={`rounded-md px-2 py-1 text-xs ${panel === id ? "bg-[var(--surface-strong)] text-[var(--text)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`} key={id} onClick={() => setPanel(id)} type="button">
            {label}
          </button>
        ))}
      </div>
      <div className="p-3">
        {panel === "review" ? <ReviewPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
        {panel === "commands" ? <CommandPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
        {panel === "browser" ? <BrowserPane actions={actions} thread={thread} onRefresh={onRefresh} /> : null}
      </div>
    </div>
  );
}
