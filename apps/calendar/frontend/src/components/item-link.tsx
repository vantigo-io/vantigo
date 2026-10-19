import { Anchor } from "@mantine/core";
import type { MouseEvent, ReactNode } from "react";
import { useCalendarNavigate } from "../lib/navigate";

/**
 * A client-side link to a path that may carry its own query string — a
 * provider's record, or an appointment on the calendar. A modified click
 * keeps the browser's own behaviour (a new tab).
 */
export const ItemLink = ({ href, children }: { href: string; children: ReactNode }) => {
  const navigate = useCalendarNavigate();
  const follow = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    void navigate({ href });
  };
  return (
    <Anchor href={href} onClick={follow} size="sm" c="inherit" underline="hover">
      {children}
    </Anchor>
  );
};
