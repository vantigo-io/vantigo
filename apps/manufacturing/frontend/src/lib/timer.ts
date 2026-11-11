import { useEffect, useState } from "react";

/** How long an open log runs before the shop floor warns of it (M29). */
export const LONG_RUNNING_MS = 12 * 60 * 60 * 1000;

/** Milliseconds since `startedAt`, at least 0; 0 when there is no start. */
export const elapsedMs = (startedAt: string | null | undefined, now: number): number =>
  startedAt ? Math.max(0, now - new Date(startedAt).getTime()) : 0;

/** A running timer's text: `1:02:03`, hours unbounded. */
export const formatElapsed = (ms: number): string => {
  const seconds = Math.floor(ms / 1000);
  const two = (value: number) => String(value).padStart(2, "0");
  return `${Math.floor(seconds / 3600)}:${two(Math.floor(seconds / 60) % 60)}:${two(seconds % 60)}`;
};

/**
 * The time since `startedAt`, counted on the client once a second (M32): the
 * server sends no ticks. Null `startedAt` counts nothing and sets no interval.
 */
export const useElapsed = (startedAt: string | null | undefined): number => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!startedAt) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [startedAt]);
  return elapsedMs(startedAt, now);
};
