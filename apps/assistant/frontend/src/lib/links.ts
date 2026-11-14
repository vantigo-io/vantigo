import { appUrl } from "@vantigo/frontend-shell";

export type LinkTarget =
  /** A route of the app: opened by the host's router, so the panel stays open. */
  | { kind: "app"; route: string }
  /** The documentation, served by the server beside the app. */
  | { kind: "docs"; href: string }
  /** Anything else: shown as text with its address, never as a link. */
  | { kind: "blocked"; address?: string };

/**
 * The first path segments of the routes the host has, which the server's
 * prompt names and `navigate` accepts (assistant/prompt.go appRoutes). A link
 * in an answer opens only these and the instance's own documentation.
 */
export const APP_ROUTE_PREFIXES: readonly string[] = [
  "/dashboard",
  "/customers",
  "/energy",
  "/invoices",
  "/projects",
  "/products",
  "/communications",
  "/time",
  "/expenses",
  "/staff",
  "/planning",
  "/inventory",
  "/orders",
  "/manufacturing",
  "/accounting",
  "/knowledge",
  "/calendar",
  "/agents",
  "/assistant",
  "/settings",
];

const isAppRoute = (href: string) => {
  const path = href.split(/[?#]/, 1)[0];
  return APP_ROUTE_PREFIXES.some((prefix) => path === prefix || path.startsWith(`${prefix}/`));
};

/**
 * Where a link in an answer goes. Records are linked as in-app routes
 * (`/customers/42`) and the documentation by its site path (`/docs/en/...`),
 * and only those are links: an address elsewhere on the web, `mailto:`,
 * `tel:`, a script URL or a protocol-relative address is shown as text with
 * the address visible, so the person sees where it points and can choose to
 * copy it. (An answer is shaped by what the model read, which is not always
 * to be trusted.)
 */
export const classifyLink = (href: string | undefined): LinkTarget => {
  if (!href) return { kind: "blocked" };
  const address = href.trim();
  if (address.startsWith("//") || address.includes("\\") || address.includes("..")) return { kind: "blocked", address };
  if (
    address === "/docs" ||
    address.startsWith("/docs/") ||
    address.startsWith("/docs?") ||
    address.startsWith("/docs#")
  )
    return { kind: "docs", href: appUrl(address) };
  if (address.startsWith("/") && isAppRoute(address)) return { kind: "app", route: address };
  return { kind: "blocked", address };
};
