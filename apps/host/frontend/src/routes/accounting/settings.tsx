import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/accounting-ui/pages/settings";

/** The books' settings and the lock (accounting design A8). */
export const Route = createFileRoute("/accounting/settings")({
  component: SettingsPage,
});
