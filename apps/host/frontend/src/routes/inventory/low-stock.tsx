import { createFileRoute } from "@tanstack/react-router";
import { validateLowStockSearch } from "@vantigo/inventory-ui/lib/search";
import { LowStockPage } from "@vantigo/inventory-ui/pages/low-stock";
import { OrderFromProposalsLink } from "@vantigo/purchasing-ui";
import { useCanOrderFromProposals } from "../../lib/purchasing-access";

/**
 * The low-stock list (inventory phase 2 design §7, B), filtered to a site by
 * the search, with Purchasing's **Order…** on each row when Purchasing is on
 * and the caller may make orders from proposals (purchasing design P35):
 * Inventory never names Purchasing, so the host composes it.
 */
const LowStockRoutePage = () => {
  const order = useCanOrderFromProposals();
  return (
    <LowStockPage
      renderRowAction={
        order ? (row) => <OrderFromProposalsLink variantId={row.variant.id} siteId={row.site.id} /> : undefined
      }
    />
  );
};

export const Route = createFileRoute("/inventory/low-stock")({
  validateSearch: validateLowStockSearch,
  component: LowStockRoutePage,
});
