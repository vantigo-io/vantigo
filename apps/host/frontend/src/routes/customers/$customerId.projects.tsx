import { createFileRoute } from "@tanstack/react-router";
import { CustomerProjectsTab } from "./-customer-projects-tab";

export const Route = createFileRoute("/customers/$customerId/projects")({
  component: CustomerProjectsTab,
});
