import { useNavigate } from "@tanstack/react-router";
import type { CalendarSearch } from "./search";

export interface CalendarNavigateOptions {
  to?: string;
  /** A path with its own query string — a provider's `path` — parsed by the router. */
  href?: string;
  search?: CalendarSearch | ((previous: CalendarSearch) => CalendarSearch);
  replace?: boolean;
}

/**
 * The router's `navigate`, typed for this package. The package is mounted by
 * the host's router, whose route tree it cannot see, so the typed overloads
 * would refuse every path; this names the few shapes the calendar uses.
 */
export const useCalendarNavigate = () =>
  useNavigate() as unknown as (options: CalendarNavigateOptions) => Promise<void>;
