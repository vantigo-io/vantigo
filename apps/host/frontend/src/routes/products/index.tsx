import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useStockTotals } from "@vantigo/inventory-ui/lib/stock-totals";
import type { ProductResponse } from "@vantigo/products-ui";
import { ProductsPage } from "@vantigo/products-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasAllPermissions } from "../../navigation";

/**
 * The product list with the On hand column (inventory design I33): Products
 * never names Inventory, so this route passes the column through the page's
 * `extraColumns`. It is given when the module is on and the caller holds
 * `inventory:stock-view` and `inventory:locations-view` — the product page's
 * Stock card's gate, as every stock read asks for both (design §5).
 *
 * Products hands the page's rows to `extraColumns`, so the column knows every
 * variant on the page; each cell is a component that reads the page's totals
 * with the same ids, which is one stock read for the page (≤ 100 ids), shared
 * by every cell through the query cache. A product's figure is the sum over
 * its variants; a Service product's cell is blank.
 */
const ProductsListPage = () => {
  const { t } = useI18n("host");
  const inventoryOn = enabledModuleKeys().includes("inventory");
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: inventoryOn && !!session,
    retry: false,
    staleTime: 300_000,
  });
  const stock =
    inventoryOn &&
    hasAllPermissions(authorization.data?.permissions, ["inventory:locations-view", "inventory:stock-view"]);
  // One page whether the column is given or not, so its answering the permissions never remounts the list.
  return (
    <ProductsPage
      extraColumns={
        stock
          ? (products) => {
              const variantIds = products
                .filter((product) => product.type !== "Service")
                .flatMap((product) => (product.variants ?? []).map((variant) => variant.id));
              return [
                {
                  key: "inventory-on-hand",
                  header: t("navigation.inventoryOnHand"),
                  cell: (product) => <OnHandCell product={product} variantIds={variantIds} />,
                },
              ];
            }
          : undefined
      }
    />
  );
};

/** One product's on hand, from the page's totals; blank until they answer, and when the read failed. */
const OnHandCell = ({ product, variantIds }: { product: ProductResponse; variantIds: readonly number[] }) => {
  const { formatters } = useI18n("host");
  const { data: totals } = useStockTotals(variantIds);
  if (product.type === "Service" || !totals) return null;
  return formatters.formatNumber(
    (product.variants ?? []).reduce((sum, variant) => sum + (totals.get(variant.id) ?? 0), 0),
  );
};

export const Route = createFileRoute("/products/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    status: ["Draft", "Active", "Discontinued"].includes(String(search.status))
      ? (String(search.status) as "Draft" | "Active" | "Discontinued")
      : "",
    categoryId:
      Number.isInteger(Number(search.categoryId)) && Number(search.categoryId) > 0 ? Number(search.categoryId) : "",
    // Present only when true: the create form opens on arrival (Spotlight's quick action).
    ...(search.create === true || search.create === "true" ? { create: true as const } : {}),
  }),
  component: ProductsListPage,
});
