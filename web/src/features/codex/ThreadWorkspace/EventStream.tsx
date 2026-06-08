import { useEffect, useRef } from "react";
import type { CodexEvent } from "../../../app/types";
import { EmptyState } from "../../../components/ui";
import { EventRow } from "../ThreadEventRow";

export function EventStream({ events }: { events: CodexEvent[] }) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [events.length]);

  return (
    <div className="grid max-h-[52vh] gap-2 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" ref={scrollRef}>
      {events.length ? (
        events.map((event) => <EventRow key={event.id || event.sequence} event={event} />)
      ) : (
        <EmptyState body="发送第一条 prompt 后会在此显示消息、命令、diff 和审批。" title="空会话" />
      )}
    </div>
  );
}
