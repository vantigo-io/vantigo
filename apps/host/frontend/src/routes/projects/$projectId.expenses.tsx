import { createFileRoute } from "@tanstack/react-router";
import { ProjectExpensesTab } from "./-project-expenses-tab";

export const Route = createFileRoute("/projects/$projectId/expenses")({
  component: ProjectExpensesTab,
});
