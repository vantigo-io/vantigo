import { createFileRoute } from "@tanstack/react-router";
import { type ApprovalsSearch, validateApprovalsSearch } from "@vantigo/expenses-ui/lib/search";
import { ApprovalsPage } from "@vantigo/expenses-ui/pages/approvals";

export const Route = createFileRoute("/expenses/approvals")({
  validateSearch: (search: Record<string, unknown>): ApprovalsSearch => validateApprovalsSearch(search),
  component: ApprovalsPage,
});
