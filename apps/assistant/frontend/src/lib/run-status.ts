import { useSyncExternalStore } from "react";

/**
 * What the Assistant button's badge shows: `working` while a run is going,
 * `done` once it has finished and the person has not looked yet, `idle`
 * otherwise. (From delivery B, `waiting` joins them for a confirmation.) The
 * conversation view reports it; the button, which lives in the header and not
 * under the panel, reads it.
 */
export type AssistantRunStatus = "idle" | "working" | "done";

let status: AssistantRunStatus = "idle";
const listeners = new Set<() => void>();

export const setAssistantRunStatus = (next: AssistantRunStatus) => {
  if (next === status) return;
  status = next;
  for (const listener of listeners) listener();
};

export const getAssistantRunStatus = () => status;

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const useAssistantRunStatus = (): AssistantRunStatus =>
  useSyncExternalStore(subscribe, getAssistantRunStatus, () => "idle");
