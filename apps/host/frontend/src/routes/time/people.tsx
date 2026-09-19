import { createFileRoute } from "@tanstack/react-router";
import { PeoplePage, type PeopleSearch } from "@vantigo/time-ui/pages/people";

/** The widest window the API takes; a link carrying more is no window at all. */
const MAX_WEEKS = 12;

const weekWindow = (value: unknown) => {
  const weeks = Number(value);
  return value !== undefined && value !== "" && Number.isInteger(weeks) && weeks >= 1 && weeks <= MAX_WEEKS
    ? weeks
    : undefined;
};

export const Route = createFileRoute("/time/people")({
  // The overview keeps its window in the URL, so a pasted link shows the same
  // weeks. Outside 1–12 the API refuses the request, so anything else is
  // dropped here and the page falls back to its own default of four.
  validateSearch: (search: Record<string, unknown>): PeopleSearch => ({ weeks: weekWindow(search.weeks) }),
  component: PeoplePage,
});
