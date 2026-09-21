import { createFileRoute } from "@tanstack/react-router";
import { CustomerOverviewTab } from "./-customer-overview-tab";

export const Route = createFileRoute("/customers/$customerId/")({
  component: CustomerOverviewTab,
});
