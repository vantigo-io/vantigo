import { createFileRoute } from "@tanstack/react-router";
import { GoogleCalendarGmailTab } from "@vantigo/google-ui";

/** Each person's Google connection, the calendar sync and Gmail, still to come; a route from the start. */
export const Route = createFileRoute("/workspace/google/calendar-gmail")({
  component: GoogleCalendarGmailTab,
});
