import { createFileRoute } from "@tanstack/react-router";
import { CustomerEnergyPage } from "@vantigo/energy-ui";

export const Route = createFileRoute("/customers/$customerId/energy")({
  component: CustomerEnergyPage,
});
