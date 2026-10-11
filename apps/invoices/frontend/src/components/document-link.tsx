import { Anchor } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { invoiceHref, invoiceLinkOptions } from "../lib/routes";

/**
 * A link to one document: a real href, so it opens in a new tab, and the
 * router's own navigation on a plain click.
 */
export const DocumentLink = ({ invoiceId, children }: { invoiceId: number; children: ReactNode }) => {
  const navigate = useNavigate() as (options: unknown) => void;
  return (
    <Anchor
      href={appUrl(invoiceHref(invoiceId))}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        navigate(invoiceLinkOptions(invoiceId));
      }}
    >
      {children}
    </Anchor>
  );
};

export interface RouteLinkProps {
  /** The path as a plain URL, for the href. */
  href: string;
  /** What the router's `navigate` takes to reach it. */
  to: { to: string; params?: Record<string, string> };
  "aria-label"?: string;
  children: ReactNode;
}

/**
 * A link to one of the host's routes this package's pages live at — a run,
 * paper letters — as `DocumentLink` is to a document: a real href, and the
 * router's own navigation on a plain click.
 */
export const RouteLink = ({ href, to, children, "aria-label": label }: RouteLinkProps) => {
  const navigate = useNavigate() as (options: unknown) => void;
  return (
    <Anchor
      href={appUrl(href)}
      aria-label={label}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </Anchor>
  );
};
