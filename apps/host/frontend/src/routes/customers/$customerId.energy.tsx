import { createFileRoute } from "@tanstack/react-router";
import { CustomerEnergyTab } from "./-customer-energy-tab";

export const Route = createFileRoute("/customers/$customerId/energy")({
  component: CustomerEnergyTab,
});
