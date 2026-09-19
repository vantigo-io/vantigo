import { createFileRoute } from "@tanstack/react-router";
import { ApprovalsPage, type ApprovalsSearch } from "@vantigo/time-ui/pages/approvals";

/** A page number, or none at all: the page falls back to the first one. */
const positiveInteger = (value: unknown) => {
  const count = Number(value);
  return value !== undefined && value !== "" && Number.isInteger(count) && count > 0 ? count : undefined;
};

export const Route = createFileRoute("/time/approvals")({
  validateSearch: (search: Record<string, unknown>): ApprovalsSearch => ({ page: positiveInteger(search.page) }),
  component: ApprovalsPage,
});
