import { createFileRoute, notFound } from "@tanstack/react-router";
import { customerQueryOptions, NotFoundError } from "@vantigo/customers-ui/api/customers";
import { CustomerDetailsPage } from "@vantigo/customers-ui/pages/customers.$customerId";
export const Route = createFileRoute("/customers/$customerId")({
  params: {
    parse: ({ customerId }) => ({ customerId: Number(customerId) }),
    stringify: ({ customerId }) => ({ customerId: String(customerId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(customerQueryOptions(params.customerId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: CustomerDetailsPage,
});
