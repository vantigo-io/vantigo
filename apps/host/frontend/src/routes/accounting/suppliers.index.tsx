import { createFileRoute } from "@tanstack/react-router";
import { SuppliersPage } from "@vantigo/accounting-ui/pages/suppliers";

/** The suppliers (accounting design A17). */
export const Route = createFileRoute("/accounting/suppliers/")({
  component: SuppliersPage,
});
