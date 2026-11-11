import { createFileRoute } from "@tanstack/react-router";
import { ManufacturingOverviewPage } from "@vantigo/manufacturing-ui/pages/overview";

/**
 * The app's home (manufacturing design M7): delivery A's counts of work
 * centers and BoMs. It is `manufacturing:access`'s, which is what the guard
 * asks of the whole prefix.
 */
export const Route = createFileRoute("/manufacturing/")({
  component: ManufacturingOverviewPage,
});
