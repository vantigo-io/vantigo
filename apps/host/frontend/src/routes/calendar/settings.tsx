import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/calendar-ui";
import { connectionButtonFor } from "../../lib/connections";

/**
 * My calendar, with the calendar's `connection.button` slot filled by every
 * enabled provider's button from the host's list (Outlook connection design
 * M8, Google Workspace design G9): the calendar package never imports a
 * provider; the host composes them.
 */
const CalendarSettingsRoute = () => <SettingsPage connectionButton={connectionButtonFor()} />;

export const Route = createFileRoute("/calendar/settings")({
  component: CalendarSettingsRoute,
});
