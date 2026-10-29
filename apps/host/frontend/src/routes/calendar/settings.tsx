import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/calendar-ui";
import { OutlookConnectButton } from "@vantigo/entra-ui";
import { enabledModuleKeys } from "../../lib/enabled-modules";

/**
 * My calendar, with the calendar's `connection.button` slot filled by the
 * Entra package's button while that module is on (Outlook connection design
 * M8): the calendar package never imports Entra; the host composes them.
 */
const CalendarSettingsRoute = () => (
  <SettingsPage
    connectionButton={
      enabledModuleKeys().includes("entra") ? (props) => <OutlookConnectButton {...props} /> : undefined
    }
  />
);

export const Route = createFileRoute("/calendar/settings")({
  component: CalendarSettingsRoute,
});
