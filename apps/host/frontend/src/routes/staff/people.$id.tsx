import { createFileRoute, notFound } from "@tanstack/react-router";
import { validatePersonSearch } from "@vantigo/staff-ui/lib/search";
import { PersonPage } from "@vantigo/staff-ui/pages/person";

/**
 * One person. The path is the package's own `PERSON_ROUTE_PATH` — People's
 * rows, New person and the org view link to it — so it is not a free choice
 * here; the page reads the id from the router itself.
 *
 * A path that is not a positive whole number never reaches the API as
 * `GET /people/NaN`: it is a not-found, which the `/staff` layout's own
 * boundary renders. The module gate and the permission gate are the
 * layout's and the guard's, as for every other staff route.
 */
export const Route = createFileRoute("/staff/people/$id")({
  validateSearch: validatePersonSearch,
  beforeLoad: ({ params }) => {
    if (!/^\d+$/.test(params.id) || !Number.isSafeInteger(Number(params.id)) || Number(params.id) <= 0) {
      throw notFound();
    }
  },
  component: PersonPage,
});
