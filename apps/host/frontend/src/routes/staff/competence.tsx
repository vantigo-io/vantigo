import { createFileRoute } from "@tanstack/react-router";
import { validateCompetenceSearch } from "@vantigo/staff-ui/lib/search";
import { CompetencePage } from "@vantigo/staff-ui/pages/competence";

export const Route = createFileRoute("/staff/competence")({
  validateSearch: validateCompetenceSearch,
  component: CompetencePage,
});
