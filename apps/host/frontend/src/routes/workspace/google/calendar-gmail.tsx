import { createFileRoute } from "@tanstack/react-router";
import { GoogleCalendarGmailTab } from "@vantigo/google-ui";

/** Connection for people — the switch, its status and the redirect URI — with the calendar sync and Gmail still to come. */
export const Route = createFileRoute("/workspace/google/calendar-gmail")({
  component: GoogleCalendarGmailTab,
});
