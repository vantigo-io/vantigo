import { Anchor } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { usePurchasingFormat } from "../lib/format";
import { proposalsLinkOptions } from "../lib/routes";

/**
 * **Order…** (purchasing design P35): the link the host composes onto
 * Inventory's low-stock row and Manufacturing's buy suggestion, to the
 * proposals filtered to the variant at the site. The host offers it only when
 * Purchasing is enabled and the caller may make orders from proposals.
 */
export const OrderFromProposalsLink = ({ variantId, siteId }: { variantId: number; siteId: number }) => {
  const { t } = usePurchasingFormat();
  return (
    <Anchor size="sm" renderRoot={(props) => <Link {...proposalsLinkOptions({ variantId, siteId })} {...props} />}>
      {t("orderFromProposals")}
    </Anchor>
  );
};
