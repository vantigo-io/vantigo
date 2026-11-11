import { createFileRoute } from "@tanstack/react-router";
import { NewBomPage } from "@vantigo/manufacturing-ui/pages/bom";

/** A new bill of materials' editor; it says so when the caller may not write one. */
export const Route = createFileRoute("/manufacturing/boms/new")({
  component: NewBomPage,
});
