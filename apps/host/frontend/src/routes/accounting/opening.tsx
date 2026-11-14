import { createFileRoute } from "@tanstack/react-router";
import { OpeningPage } from "@vantigo/accounting-ui/pages/opening";

/** The opening balances (accounting design A9), reached from the overview and Set up the books. */
export const Route = createFileRoute("/accounting/opening")({
  component: OpeningPage,
});
