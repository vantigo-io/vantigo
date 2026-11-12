import { createFileRoute } from "@tanstack/react-router";
import { validateTraceSearch } from "@vantigo/inventory-ui/lib/search";
import { TracePage } from "@vantigo/inventory-ui/pages/trace";

/** A lot's trace (manufacturing phase 2 M87): where it went or where it came from, by the search. */
export const Route = createFileRoute("/inventory/trace")({
  validateSearch: validateTraceSearch,
  component: TracePage,
});
