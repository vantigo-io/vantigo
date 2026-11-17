import { useQuery } from "@tanstack/react-query";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { ProductStock } from "@vantigo/inventory-ui";
import { variantStockHref } from "@vantigo/inventory-ui/lib/routes";
import { ProductDetailsPage } from "@vantigo/products-ui";
import { productQueryOptions } from "@vantigo/products-ui/api/products";
import { NotFoundError } from "@vantigo/products-ui/api/request";
import { ProductSuppliersCard } from "@vantigo/purchasing-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasAllPermissions } from "../../navigation";

/**
 * The product page with the panels other modules add (inventory design I27):
 * Products never names Inventory, so this route passes the Stock card through
 * the page's `extraPanels` slot, and the link its delete dialog shows for a
 * variant with stock through `stockHref`. That link is a plain anchor in a
 * notification, outside the router, so it is a complete href, the base path
 * included (`appUrl`). Both are given when the module is
 * on and the caller holds `inventory:stock-view` and `inventory:locations-view`
 * — every stock read asks for both (design §5). Purchasing's **Suppliers**
 * card (purchasing design P13) goes through the same slot, when that module is
 * on and the caller holds `purchasing:access`, with the product's variants.
 */
const ProductPage = () => {
  const { productId } = Route.useParams();
  const inventoryOn = enabledModuleKeys().includes("inventory");
  const purchasingOn = enabledModuleKeys().includes("purchasing");
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: (inventoryOn || purchasingOn) && !!session,
    retry: false,
    staleTime: 300_000,
  });
  const stock =
    inventoryOn &&
    hasAllPermissions(authorization.data?.permissions, ["inventory:locations-view", "inventory:stock-view"]);
  const suppliers = purchasingOn && hasAllPermissions(authorization.data?.permissions, ["purchasing:access"]);
  const panels =
    stock || suppliers ? (
      <>
        {stock && <ProductStock productId={productId} />}
        {suppliers && <ProductSuppliers productId={productId} />}
      </>
    ) : undefined;
  return stock ? (
    <ProductDetailsPage extraPanels={panels} stockHref={stockHref} />
  ) : (
    <ProductDetailsPage extraPanels={panels} />
  );
};

/** Purchasing's card for the product's variants, read from the product the route's loader holds. */
const ProductSuppliers = ({ productId }: { productId: number }) => {
  const product = useQuery(productQueryOptions(productId));
  if (!product.data) return null;
  return <ProductSuppliersCard variantIds={product.data.variants.map((v) => v.id)} />;
};

/** The variant's stock page as a complete href: Products draws it as a plain anchor, outside the router. */
const stockHref = (variantId: number) => appUrl(variantStockHref(variantId));

export const Route = createFileRoute("/products/$productId")({
  params: {
    parse: ({ productId }) => ({ productId: Number(productId) }),
    stringify: ({ productId }) => ({ productId: String(productId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(productQueryOptions(params.productId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: ProductPage,
});
