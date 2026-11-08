import { AGENTS_HOME_ROUTE_PATH, AGENTS_REVIEW_ROUTE_PATH, AGENTS_SETTINGS_ROUTE_PATH } from "./routes";

/** One sidebar destination of the app; the host owns its label and icon. */
export interface AgentsNavItem {
  key: "agents" | "review" | "settings";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry. */
  requiredPermissions: readonly string[];
}

export const agentsNavItems: readonly AgentsNavItem[] = [
  { key: "agents", label: "navAgents", to: AGENTS_HOME_ROUTE_PATH, requiredPermissions: ["agents:view"] },
  { key: "review", label: "navReview", to: AGENTS_REVIEW_ROUTE_PATH, requiredPermissions: ["agents:review"] },
  { key: "settings", label: "navSettings", to: AGENTS_SETTINGS_ROUTE_PATH, requiredPermissions: ["agents:manage"] },
];

/** The entries the caller's permissions offer. */
export const visibleAgentsNavItems = (permissions: readonly string[] | undefined) =>
  agentsNavItems.filter(
    (item) => permissions?.includes("*") === true || item.requiredPermissions.some((p) => permissions?.includes(p)),
  );
