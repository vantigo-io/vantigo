/**
 * The four tabs of the Entra page, which the host mounts as child routes of
 * `/workspace/entra` (Integrations design T3). The package links between
 * them by these paths, as it links to Staff's settings by `/staff/settings`;
 * the host's routes are the other half of the pair.
 */
export const entraTabs = [
  { value: "status", path: "/workspace/entra", labelKey: "tabStatus" },
  { value: "connection", path: "/workspace/entra/connection", labelKey: "tabConnection" },
  { value: "accounts", path: "/workspace/entra/accounts", labelKey: "tabAccounts" },
  { value: "outlook", path: "/workspace/entra/outlook", labelKey: "tabOutlook" },
] as const;

export type EntraTab = (typeof entraTabs)[number]["value"];

const tabOf = (tab: EntraTab) => {
  const found = entraTabs.find((candidate) => candidate.value === tab);
  if (!found) throw new Error(`unknown Entra tab "${tab}"`);
  return found;
};

export const entraTabPath = (tab: EntraTab): string => tabOf(tab).path;

/** The tab a location belongs to; anything under `/workspace/entra` that is not another tab's is *Status*. */
export const entraTabForPath = (pathname: string): EntraTab =>
  entraTabs.find((tab) => tab.value !== "status" && (pathname === tab.path || pathname.startsWith(`${tab.path}/`)))
    ?.value ?? "status";
export const entraTabLabelKey = (tab: EntraTab) => tabOf(tab).labelKey;
