import { createFileRoute } from "@tanstack/react-router";
import { SuppliersRoutePage } from "./-purchasing-pages";

/** The register's suppliers as Purchasing shows them (P13). */
export const Route = createFileRoute("/purchasing/suppliers/")({
  component: SuppliersRoutePage,
});
