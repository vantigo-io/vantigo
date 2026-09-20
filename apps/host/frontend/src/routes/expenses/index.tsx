import { createFileRoute } from "@tanstack/react-router";
import { type MyExpensesSearch, validateMyExpensesSearch } from "@vantigo/expenses-ui/lib/search";
import { MyExpenses } from "./-my-expenses";

export const Route = createFileRoute("/expenses/")({
  validateSearch: (search: Record<string, unknown>): MyExpensesSearch => validateMyExpensesSearch(search),
  component: MyExpenses,
});
