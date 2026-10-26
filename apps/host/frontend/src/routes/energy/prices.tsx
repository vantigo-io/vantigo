import { createFileRoute } from "@tanstack/react-router";
import { PricesPage } from "./-prices-page";

const PERIODS = ["day", "week", "month", "year", "range"] as const;
const AREA = /^[A-Z]{2}\d{0,2}$/;
// Years 1000–9999: the page's date arithmetic (`Date.UTC`) reads 0–99 as 19xx.
const MONTH = /^[1-9]\d{3}-(0[1-9]|1[0-2])$/;

/** A civil date, `YYYY-MM-DD` in the years 1000–9999, that exists: 2026-02-30 does not. */
const isDay = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[1-9]\d{3}-\d{2}-\d{2}$/.test(value) &&
  new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;

/**
 * The Prices page's view (spot-price service design S8): `areas` a comma
 * list of up to five price areas, `period`, `date` (`YYYY-MM-DD`), and
 * `from`/`to` (`YYYY-MM`) for a range. A value that is none of these is
 * dropped, and the page shows its default.
 */
export const Route = createFileRoute("/energy/prices")({
  validateSearch: (
    search: Record<string, unknown>,
  ): {
    areas?: string;
    period?: (typeof PERIODS)[number];
    date?: string;
    from?: string;
    to?: string;
  } => {
    const areas =
      typeof search.areas === "string"
        ? [
            ...new Set(
              search.areas
                .split(",")
                .map((area) => area.trim().toUpperCase())
                .filter((area) => AREA.test(area)),
            ),
          ].slice(0, 5)
        : [];
    const period = PERIODS.find((item) => item === search.period);
    return {
      ...(areas.length > 0 ? { areas: areas.join(",") } : {}),
      ...(period ? { period } : {}),
      ...(isDay(search.date) ? { date: search.date } : {}),
      ...(typeof search.from === "string" && MONTH.test(search.from) ? { from: search.from } : {}),
      ...(typeof search.to === "string" && MONTH.test(search.to) ? { to: search.to } : {}),
    };
  },
  component: PricesPage,
});
