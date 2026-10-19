import { createFileRoute } from "@tanstack/react-router";
import { CalendarPage } from "@vantigo/calendar-ui";

export const Route = createFileRoute("/calendar/")({
  component: CalendarPage,
});
