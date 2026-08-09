import { IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import type { ShellApp } from "@vantigo/frontend-shell";

const communicationsUrl = import.meta.env.VITE_COMMUNICATIONS_URL || "http://localhost:10012/communications";
const customersUrl = import.meta.env.VITE_CUSTOMERS_URL || "http://localhost:10011/customers";

/**
 * The apps shown in the shell's application switcher. Sibling apps are enabled
 * through environment variables; an explicit empty value disables the entry.
 */
export const shellApps: readonly ShellApp[] = [
  { id: "products", label: "Products", icon: IconPackage, current: true },
  ...(customersUrl ? [{ id: "customers", label: "Customers", icon: IconUsers, url: customersUrl }] : []),
  ...(communicationsUrl
    ? [{ id: "communications", label: "Communications", icon: IconMessage, url: communicationsUrl }]
    : []),
];
