import { createFileRoute } from "@tanstack/react-router";
import { EntraPage } from "@vantigo/entra-ui";

export const Route = createFileRoute("/entra/")({
  component: EntraPage,
});
