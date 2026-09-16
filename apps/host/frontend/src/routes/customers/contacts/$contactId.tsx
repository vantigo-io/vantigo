import { createFileRoute, notFound } from "@tanstack/react-router";
import { contactQueryOptions } from "@vantigo/customers-ui/api/contacts";
import { NotFoundError } from "@vantigo/customers-ui/api/request";
import { ContactDetailsPage } from "@vantigo/customers-ui/pages/contacts.$contactId";
export const Route = createFileRoute("/customers/contacts/$contactId")({
  params: {
    parse: ({ contactId }) => ({ contactId: Number(contactId) }),
    stringify: ({ contactId }) => ({ contactId: String(contactId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(contactQueryOptions(params.contactId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: ContactDetailsPage,
});
