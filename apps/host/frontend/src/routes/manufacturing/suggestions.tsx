import { createFileRoute } from "@tanstack/react-router";
import { SuggestionsPage } from "@vantigo/manufacturing-ui/pages/suggestions";

/** The replenishment suggestions, under the manufacturing orders (manufacturing phase 2 M111). */
export const Route = createFileRoute("/manufacturing/suggestions")({
  component: SuggestionsPage,
});
