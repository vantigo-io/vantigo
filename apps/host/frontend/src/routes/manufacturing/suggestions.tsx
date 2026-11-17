import { createFileRoute } from "@tanstack/react-router";
import { SuggestionsPage } from "@vantigo/manufacturing-ui/pages/suggestions";
import { OrderFromProposalsLink } from "@vantigo/purchasing-ui";
import { useCanOrderFromProposals } from "../../lib/purchasing-access";

/**
 * The replenishment suggestions, under the manufacturing orders (manufacturing
 * phase 2 M111), with Purchasing's **Order…** on a buy row when Purchasing is
 * on and the caller may make orders from proposals (purchasing design P35): a
 * buy row is bought there, and the host composes the link.
 */
const SuggestionsRoutePage = () => {
  const order = useCanOrderFromProposals();
  return (
    <SuggestionsPage
      renderBuyAction={
        order ? (s, siteId) => <OrderFromProposalsLink variantId={s.variant.id} siteId={siteId} /> : undefined
      }
    />
  );
};

export const Route = createFileRoute("/manufacturing/suggestions")({
  component: SuggestionsRoutePage,
});
