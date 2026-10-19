import { createFileRoute } from "@tanstack/react-router";
import { CalendarPage } from "@vantigo/calendar-ui";
import { validateCalendarSearch } from "@vantigo/calendar-ui/lib/search";

export const Route = createFileRoute("/calendar/")({
  validateSearch: validateCalendarSearch,
  component: CalendarPage,
});
