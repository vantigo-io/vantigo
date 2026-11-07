import { appConfig } from "@vantigo/frontend-shell";

/**
 * The modules whose records a pasted URL can turn into a record chip
 * (knowledge design K22): each route's first segment is its module's key, as
 * the backend's MODULES list names it, and its catalog key names it.
 * Knowledge's own pages are article links instead.
 */
export const recordModules = {
  customers: "moduleCustomers",
  energy: "moduleEnergy",
  invoices: "moduleInvoices",
  projects: "moduleProjects",
  products: "moduleProducts",
  communications: "moduleCommunications",
  time: "moduleTime",
  expenses: "moduleExpenses",
  staff: "moduleStaff",
  planning: "modulePlanning",
  inventory: "moduleInventory",
  orders: "moduleOrders",
  calendar: "moduleCalendar",
} as const;

export type RecordModule = keyof typeof recordModules;

/** The record module a route's first segment names, when the backend has that module on. */
export const enabledRecordModule = (
  segment: string,
  enabled: readonly string[] | undefined = appConfig().modules,
): RecordModule | null => {
  if (!Object.hasOwn(recordModules, segment)) return null;
  // No injected list (the Vite dev server) means every module.
  if (enabled && !enabled.includes(segment)) return null;
  return segment as RecordModule;
};
