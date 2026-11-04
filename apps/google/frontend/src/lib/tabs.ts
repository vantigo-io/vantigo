/**
 * The four tabs of the Google Workspace page, which the host mounts as child
 * routes of `/workspace/google` (Google Workspace design G16). Accounts and
 * Calendar and Gmail are routes from the start so links never move.
 */
export const googleTabs = [
  { value: "status", path: "/workspace/google", labelKey: "tabStatus" },
  { value: "connection", path: "/workspace/google/connection", labelKey: "tabConnection" },
  { value: "accounts", path: "/workspace/google/accounts", labelKey: "tabAccounts" },
  { value: "calendar-gmail", path: "/workspace/google/calendar-gmail", labelKey: "tabCalendarGmail" },
] as const;

export type GoogleTab = (typeof googleTabs)[number]["value"];

const tabOf = (tab: GoogleTab) => {
  const found = googleTabs.find((candidate) => candidate.value === tab);
  if (!found) throw new Error(`unknown Google tab "${tab}"`);
  return found;
};

export const googleTabPath = (tab: GoogleTab): string => tabOf(tab).path;

/** The tab a location belongs to; anything under `/workspace/google` that is not another tab's is *Status*. */
export const googleTabForPath = (pathname: string): GoogleTab =>
  googleTabs.find((tab) => tab.value !== "status" && (pathname === tab.path || pathname.startsWith(`${tab.path}/`)))
    ?.value ?? "status";

export const googleTabLabelKey = (tab: GoogleTab) => tabOf(tab).labelKey;
