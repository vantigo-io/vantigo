import { createFileRoute } from "@tanstack/react-router";
import { validatePeopleSearch } from "@vantigo/staff-ui/lib/search";
import { PeoplePage } from "@vantigo/staff-ui/pages/people";

export const Route = createFileRoute("/staff/people/")({
  validateSearch: validatePeopleSearch,
  component: PeoplePage,
});
