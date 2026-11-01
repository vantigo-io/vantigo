import { appUrl } from "@vantigo/frontend-shell";

export type LinkTarget =
  /** A route of the app: opened by the host's router, so the panel stays open. */
  | { kind: "app"; route: string }
  /** The documentation, served by the server beside the app. */
  | { kind: "docs"; href: string }
  | { kind: "external"; href: string }
  | { kind: "blocked" };

/**
 * Where a link in an answer goes. Answers link records as in-app routes
 * (`/customers/42`), the documentation by its site path (`/docs/en/...`) and
 * the rest of the web by an absolute URL. Anything else (a script URL, a
 * protocol-relative address) goes nowhere.
 */
export const classifyLink = (href: string | undefined): LinkTarget => {
  if (!href) return { kind: "blocked" };
  if (href.startsWith("//")) return { kind: "blocked" };
  if (href === "/docs" || href.startsWith("/docs/")) return { kind: "docs", href: appUrl(href) };
  if (href.startsWith("/")) return { kind: "app", route: href };
  if (/^https?:\/\//i.test(href) || /^mailto:/i.test(href)) return { kind: "external", href };
  return { kind: "blocked" };
};
