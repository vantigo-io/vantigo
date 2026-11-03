import { createFileRoute } from "@tanstack/react-router";
import { ImportPage } from "@vantigo/inventory-ui/pages/import";

/** The opening-balance import (inventory design §7, B). */
export const Route = createFileRoute("/inventory/import")({
  component: ImportPage,
});
