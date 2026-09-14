import { createFileRoute } from "@tanstack/react-router";
import { customerStatsQueryOptions, customersQueryOptions } from "@vantigo/customers-ui/api/customers";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";
export const Route = createFileRoute("/customers/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context: { queryClient }, deps }) =>
    Promise.all([
      queryClient.ensureQueryData(
        customersQueryOptions({ page: deps.page, pageSize: 25, search: deps.search || undefined }),
      ),
      queryClient.ensureQueryData(customerStatsQueryOptions()),
    ]),
  component: CustomersPage,
});
