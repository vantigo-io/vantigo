import { useQueryClient } from "@tanstack/react-query";
import { type AssistantChip, type AssistantRouteInfo, assistantContext } from "@vantigo/assistant-ui";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { meteringPointQueryOptions } from "@vantigo/energy-ui/api/energy";
import { i18n } from "@vantigo/frontend-shell";
import { invoiceQueryOptions } from "@vantigo/invoices-ui/api/invoices";
import { projectQueryOptions } from "@vantigo/projects-ui/api/projects";
import { useSyncExternalStore } from "react";
import { type AppKey, type AreaKey, areaForKey } from "../apps";
import { activeNavPath, type NavItem } from "../navigation";

/**
 * The record on screen, for the assistant's context chip (design S10). The
 * module frontends cannot import the assistant (and no module imports
 * another), so the host registers one small resolver per detail route here,
 * each reading the cache entry the page itself has already loaded: a label,
 * never a fetch and never the page. A route with no resolver, or whose data is
 * not loaded yet, falls back to the screen's name.
 *
 * The cache keys are the modules' own query options, so a rename there cannot
 * go unnoticed: the resolvers call the same functions the pages do.
 */

const numberParam = (route: AssistantRouteInfo, name: string) => {
  const value = Number(route.params[name]);
  return Number.isSafeInteger(value) ? value : undefined;
};

/** A word of the host catalog in the person's language; the chip is read by them and by the model. */
const word = (key: string) => i18n.t(key, { ns: "host" });

let registered = false;

/** Registers the host's resolvers once. */
export const registerAssistantContexts = () => {
  if (registered) return;
  registered = true;

  assistantContext.register("/customers/$customerId", (route, queryClient) => {
    const id = numberParam(route, "customerId");
    const customer = id === undefined ? undefined : queryClient.getQueryData(customerQueryOptions(id).queryKey);
    return customer?.name ? { label: `${word("navigation.customer")} ${customer.name}` } : undefined;
  });

  assistantContext.register("/energy/metering-points/$meteringPointId", (route, queryClient) => {
    const id = numberParam(route, "meteringPointId");
    const point = id === undefined ? undefined : queryClient.getQueryData(meteringPointQueryOptions(id).queryKey);
    return point?.gsrn ? { label: `${word("navigation.meteringPoint")} ${point.gsrn}` } : undefined;
  });

  assistantContext.register("/invoices/$invoiceId", (route, queryClient) => {
    const id = numberParam(route, "invoiceId");
    const invoice = id === undefined ? undefined : queryClient.getQueryData(invoiceQueryOptions(id).queryKey);
    if (!invoice) return undefined;
    return {
      label: invoice.number ? `${word("navigation.invoice")} ${invoice.number}` : word("navigation.invoiceDraft"),
    };
  });

  assistantContext.register("/projects/$projectId", (route, queryClient) => {
    const id = numberParam(route, "projectId");
    const project = id === undefined ? undefined : queryClient.getQueryData(projectQueryOptions(id).queryKey);
    return project?.name
      ? { label: [word("navigation.project"), project.code, project.name].filter(Boolean).join(" ") }
      : undefined;
  });
};

/** The matched routes the root layout reads: the deepest one is the screen. */
interface MatchLike {
  routeId: string;
  pathname: string;
  params: Record<string, unknown>;
}

export const routeInfoFrom = (matches: readonly MatchLike[]): AssistantRouteInfo | undefined => {
  const deepest = matches[matches.length - 1];
  if (!deepest) return undefined;
  const params = Object.fromEntries(
    matches.flatMap((match) => Object.entries(match.params)).map(([key, value]) => [key, String(value)]),
  );
  return { routeId: deepest.routeId, pathname: deepest.pathname, params };
};

/** The name of the screen: the sidebar entry the path falls under, else the app's or area's own name. */
export const screenNameKey = (activeKey: AppKey | AreaKey | undefined, pathname: string): string | undefined => {
  if (pathname === "/dashboard") return "navigation.dashboard";
  if (!activeKey) return undefined;
  const area = areaForKey(activeKey);
  const items: readonly NavItem[] = area.navSections.flatMap((section) => section.items);
  const best = activeNavPath(pathname, items);
  return items.find((item) => item.to === best)?.label ?? area.label;
};

/**
 * The record's label while its data is in the cache, as a primitive so the
 * layout re-renders only when it changes, not on every cache event.
 */
export const useRecordLabel = (route: AssistantRouteInfo | undefined): string | undefined => {
  const queryClient = useQueryClient();
  return useSyncExternalStore(
    (notify) => queryClient.getQueryCache().subscribe(notify),
    () => (route ? assistantContext.resolve(route, queryClient)?.label : undefined),
    () => undefined,
  );
};

/** The chip for this screen: the record's label when a resolver has one, else the screen's name. */
export const assistantChipFor = (
  route: AssistantRouteInfo | undefined,
  activeKey: AppKey | AreaKey | undefined,
  recordLabel: string | undefined,
  screenName: string | undefined,
): AssistantChip | undefined => {
  const label = recordLabel ?? screenName;
  if (!route || !label) return undefined;
  const params = Object.fromEntries(Object.entries(route.params).filter(([, value]) => value.length <= 100));
  return {
    app: activeKey ?? "home",
    route: route.routeId.length > 1 ? route.routeId.replace(/\/+$/, "") : route.routeId,
    label,
    ...(Object.keys(params).length > 0 ? { params } : {}),
  };
};
