import { IconMessage, IconUsers } from "@tabler/icons-react";
import type { ShellApp } from "@vantigo/frontend-shell";

const customersUrl = import.meta.env.VITE_CUSTOMERS_URL || "http://localhost:10011/customers";

/**
 * The apps shown in the shell's application switcher. Sibling apps are enabled
 * through environment variables; an explicit empty value disables the entry.
 */
export const shellApps: readonly ShellApp[] = [
  ...(customersUrl ? [{ id: "customers", label: "Customers", icon: IconUsers, url: customersUrl }] : []),
  { id: "communications", label: "Communications", icon: IconMessage, current: true },
];
