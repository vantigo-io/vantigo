import { createFileRoute } from "@tanstack/react-router";
import { validateAbsenceSearch } from "@vantigo/staff-ui/lib/search";
import { AbsencePage } from "@vantigo/staff-ui/pages/absence";

export const Route = createFileRoute("/staff/absence")({
  validateSearch: validateAbsenceSearch,
  component: AbsencePage,
});
