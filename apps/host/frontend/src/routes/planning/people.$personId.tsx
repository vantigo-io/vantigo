import { createFileRoute, notFound } from "@tanstack/react-router";
import { validateWindowSearch } from "@vantigo/planning-ui/lib/search";
import { PersonTimelinePage } from "@vantigo/planning-ui/pages/person-timeline";
import { isPositiveId } from "./-view-gate";

/**
 * One person's timeline (planning design S12): anyone's for `planning:view`,
 * the caller's own for `planning:access` alone — the server answers 404 for
 * anyone else's — so the guard asks only for the app here. A path that is not
 * a positive whole number is the layout's not-found.
 */
export const Route = createFileRoute("/planning/people/$personId")({
  validateSearch: validateWindowSearch,
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.personId)) throw notFound();
  },
  component: PersonTimelinePage,
});
