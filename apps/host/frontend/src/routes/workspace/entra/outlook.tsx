import { createFileRoute } from "@tanstack/react-router";
import { EntraOutlookTab } from "@vantigo/entra-ui";

/** The Outlook connection (and the calendar sync that runs on it) and the Mail channels, each a switch. */
export const Route = createFileRoute("/workspace/entra/outlook")({
  component: EntraOutlookTab,
});
