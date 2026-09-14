import { createFileRoute } from "@tanstack/react-router";
import { contactsQueryOptions } from "@vantigo/customers-ui/api/contacts";
import { ContactsPage } from "@vantigo/customers-ui/pages/contacts.index";
export const Route = createFileRoute("/contacts/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context: { queryClient }, deps }) =>
    queryClient.ensureQueryData(
      contactsQueryOptions({ page: deps.page, pageSize: 25, search: deps.search || undefined }),
    ),
  component: ContactsPage,
});
