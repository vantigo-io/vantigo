import { createFileRoute } from "@tanstack/react-router";
import { ProjectInvoicingTab } from "./-project-invoicing-tab";

export const Route = createFileRoute("/projects/$projectId/invoicing")({
  component: ProjectInvoicingTab,
});
