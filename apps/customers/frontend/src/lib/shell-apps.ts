import { IconMessage, IconUsers } from "@tabler/icons-react";
import type { ShellApp } from "@vantigo/frontend-shell";

const communicationsUrl = import.meta.env.VITE_COMMUNICATIONS_URL || "http://localhost:10012";

/**
 * The apps shown in the shell's application switcher. Sibling apps are enabled
 * through environment variables; an explicit empty value disables the entry.
 */
export const shellApps: readonly ShellApp[] = [
  { id: "customers", label: "Customers", icon: IconUsers, current: true },
  ...(communicationsUrl
    ? [{ id: "communications", label: "Communications", icon: IconMessage, url: communicationsUrl }]
    : []),
];
