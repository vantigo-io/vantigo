import { createFileRoute } from "@tanstack/react-router";
import { type ReimbursementsSearch, validateReimbursementsSearch } from "@vantigo/expenses-ui/lib/search";
import { ReimbursementsPage } from "@vantigo/expenses-ui/pages/reimbursements";

export const Route = createFileRoute("/expenses/reimbursements")({
  validateSearch: (search: Record<string, unknown>): ReimbursementsSearch => validateReimbursementsSearch(search),
  component: ReimbursementsPage,
});
