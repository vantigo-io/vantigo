import type { QueryClient } from "@tanstack/react-query";
import type { AssistantMessageContext } from "../api/types";

/**
 * The route the person is on, as the host reads it from its router: the
 * matched route's template (`/customers/$customerId/`), the URL's path and the
 * route's ids as text.
 */
export interface AssistantRouteInfo {
  routeId: string;
  pathname: string;
  params: Record<string, string>;
}

/** What a resolver says about the record on screen. */
export interface AssistantRecordContext {
  /** A short name for the record, as the person knows it: "Customer Ola Nordmann". */
  label: string;
}

/**
 * Describes the record a route shows from data already in the query cache. It
 * must never fetch and never read the page: the context is the label and the
 * route's ids, nothing else (S10).
 */
export type AssistantContextResolver = (
  route: AssistantRouteInfo,
  queryClient: QueryClient,
) => AssistantRecordContext | undefined;

interface Registration {
  template: string;
  resolve: AssistantContextResolver;
}

const trimTemplate = (value: string) => (value.length > 1 ? value.replace(/\/+$/, "") : value);

/**
 * Whether a registration for `template` covers `routeId`: the route itself or
 * any child of it, so a customer's Invoices tab is still the customer.
 */
export const templateCovers = (template: string, routeId: string) => {
  const wanted = trimTemplate(template);
  const actual = trimTemplate(routeId);
  return actual === wanted || actual.startsWith(`${wanted}/`);
};

/**
 * The registry of per-route `assistantContext` resolvers. The module frontends
 * cannot import this package (the deny list forbids it), so the host owns the
 * registrations: it calls `assistantContext.register` once per detail route,
 * reading the module's own query options, so no module package imports another.
 * The longest matching template wins, so a nested route can refine its parent.
 */
export const createAssistantContextRegistry = () => {
  const registrations: Registration[] = [];
  return {
    /** Registers a resolver for a route template; the returned function removes it. */
    register(template: string, resolve: AssistantContextResolver): () => void {
      const registration = { template, resolve };
      registrations.push(registration);
      return () => {
        const index = registrations.indexOf(registration);
        if (index !== -1) registrations.splice(index, 1);
      };
    },
    /** The record on screen, or undefined when no resolver covers the route or its data is not loaded. */
    resolve(route: AssistantRouteInfo, queryClient: QueryClient): AssistantRecordContext | undefined {
      const candidates = registrations
        .filter((one) => templateCovers(one.template, route.routeId))
        .sort((left, right) => right.template.length - left.template.length);
      for (const candidate of candidates) {
        const found = candidate.resolve(route, queryClient);
        if (found?.label) return found;
      }
      return undefined;
    },
    clear() {
      registrations.length = 0;
    },
  };
};

export const assistantContext = createAssistantContextRegistry();

/** The chip the panel shows and sends: the message context of the contract, with the label required. */
export type AssistantChip = AssistantMessageContext & { label: string };
