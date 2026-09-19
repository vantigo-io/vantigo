import { createFileRoute } from "@tanstack/react-router";
import { isIsoDate } from "@vantigo/time-ui/lib/week";
import { MyWeekPage, type MyWeekSearch } from "@vantigo/time-ui/pages/my-week";

export const Route = createFileRoute("/time/")({
  // Any calendar date is kept: the page normalises it to its Monday itself,
  // so a link to a Thursday opens that Thursday's week. Anything that is not
  // a date at all — a hand-edited URL, a stale `?week=` from elsewhere — is
  // dropped rather than reaching the API as a week it would refuse.
  validateSearch: (search: Record<string, unknown>): MyWeekSearch => ({
    week: isIsoDate(search.week) ? search.week : undefined,
  }),
  component: MyWeekPage,
});
