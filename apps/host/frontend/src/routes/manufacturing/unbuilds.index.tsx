import { createFileRoute } from "@tanstack/react-router";
import { UnbuildsPage } from "@vantigo/manufacturing-ui/pages/unbuilds";

/** The unbuilds, under the manufacturing orders (manufacturing phase 2 M123). */
export const Route = createFileRoute("/manufacturing/unbuilds/")({
  component: UnbuildsPage,
});
