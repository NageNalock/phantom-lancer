export function startSequentialPolling(
  poll: (signal: AbortSignal) => Promise<void>,
  intervalMs: number,
) {
  const controller = new AbortController();
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined;

  const schedule = () => {
    timer = globalThis.setTimeout(async () => {
      try {
        await poll(controller.signal);
      } finally {
        if (!controller.signal.aborted) schedule();
      }
    }, intervalMs);
  };

  schedule();
  return () => {
    controller.abort();
    if (timer !== undefined) globalThis.clearTimeout(timer);
  };
}
