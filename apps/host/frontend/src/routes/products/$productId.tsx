import { createFileRoute, notFound } from "@tanstack/react-router";
import { ProductDetailsPage } from "@vantigo/products-ui";
import { productPricesQueryOptions, productQueryOptions } from "@vantigo/products-ui/api/products";
import { NotFoundError } from "@vantigo/products-ui/api/request";
export const Route = createFileRoute("/products/$productId")({
  params: {
    parse: ({ productId }) => ({ productId: Number(productId) }),
    stringify: ({ productId }) => ({ productId: String(productId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await Promise.all([
        queryClient.ensureQueryData(productQueryOptions(params.productId)),
        queryClient.ensureQueryData(productPricesQueryOptions(params.productId)),
      ]);
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: ProductDetailsPage,
});
