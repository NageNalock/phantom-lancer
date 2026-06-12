import { useCallback, useEffect, useRef, useState } from "react";
import type { MouseEvent } from "react";

type QueryValue = string | null | undefined;

interface QueryParamStateOptions {
  clearKeys?: readonly string[];
}

export function useQueryParamState<T extends string>(
  key: string,
  values: readonly T[],
  fallback: T,
  options: QueryParamStateOptions = {},
): [T, (next: T) => void, (next: T) => string] {
  const read = useCallback(() => readQueryParam(key, values, fallback), [fallback, key, values]);
  const [state, setState] = useState<T>(read);
  const stateRef = useRef(state);
  const clearKeys = options.clearKeys || [];

  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  useEffect(() => {
    const onPopState = () => setState(read());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [read]);

  const setQueryState = useCallback((next: T) => {
    if (!values.includes(next)) return;
    if (next !== stateRef.current) setState(next);
    writeQueryParams({ [key]: next === fallback ? null : next }, clearKeys);
  }, [clearKeys, fallback, key, values]);

  const hrefFor = useCallback((next: T) => {
    return buildQueryHref({ [key]: next === fallback ? null : next }, clearKeys);
  }, [clearKeys, fallback, key]);

  return [state, setQueryState, hrefFor];
}

function readQueryParam<T extends string>(key: string, values: readonly T[], fallback: T): T {
  const params = new URLSearchParams(window.location.search);
  const value = params.get(key);
  return value && values.includes(value as T) ? (value as T) : fallback;
}

export function buildQueryHref(updates: Record<string, QueryValue>, clearKeys: readonly string[] = []): string {
  const url = new URL(window.location.href);
  for (const key of clearKeys) {
    url.searchParams.delete(key);
  }
  for (const [key, value] of Object.entries(updates)) {
    if (value === null || value === undefined) {
      url.searchParams.delete(key);
    } else {
      url.searchParams.set(key, value);
    }
  }
  return `${url.pathname}${url.search}${url.hash}`;
}

function writeQueryParams(updates: Record<string, QueryValue>, clearKeys: readonly string[] = []) {
  const href = buildQueryHref(updates, clearKeys);
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (href !== current) {
    window.history.pushState(null, "", href);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }
}

export function shouldHandleQueryLinkClick(event: MouseEvent<HTMLAnchorElement>): boolean {
  return event.button === 0 && !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey && event.currentTarget.target !== "_blank";
}
