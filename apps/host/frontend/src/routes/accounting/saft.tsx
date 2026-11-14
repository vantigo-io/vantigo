import { createFileRoute } from "@tanstack/react-router";
import { SaftExportPage } from "@vantigo/accounting-ui/pages/saft";

/** SAF-T Financial: the file an audit asks for (accounting design A24). */
export const Route = createFileRoute("/accounting/saft")({
  component: SaftExportPage,
});
