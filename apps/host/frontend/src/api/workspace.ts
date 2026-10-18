import { request } from "./request";

/** The workspace's own settings (time zones design D2 and D3). */
export interface WorkspaceSettings {
  /** The business time zone, an IANA name: the calendar every bookkeeping day is judged in. */
  businessTimeZone: string;
}

export const workspaceSettingsQueryKey = ["owner", "workspace"] as const;

export const fetchWorkspaceSettings = () => request<WorkspaceSettings>("/api/v1/identity/owner/workspace");

export const updateWorkspaceSettings = (body: WorkspaceSettings) =>
  request<WorkspaceSettings>("/api/v1/identity/owner/workspace", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

/**
 * Every IANA name this browser knows, so the zone is picked rather than
 * typed — and the zone the workspace already runs on even when the browser
 * has never heard of it, or a save would silently clear it.
 */
export const timeZoneOptions = (current: string): string[] => {
  const known = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return known.includes(current) ? [...known] : [current, ...known];
};
