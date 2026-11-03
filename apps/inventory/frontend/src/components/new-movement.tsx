import { Alert, Stack } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { locationQueryOptions } from "../api/locations";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { variantStockQueryOptions } from "../api/stock";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import type { LineEditorKind } from "../lib/lines";
import { INVENTORY_HOME_ROUTE_PATH, MOVEMENTS_ROUTE_PATH } from "../lib/routes";
import type { NewMovementSearch } from "../lib/search";
import { MovementForm } from "./movement-form";

/**
 * A new movement's page: what the form needs before it can open — the
 * caller's capabilities, the sites, and the location or variant it was opened
 * from — then the form, prefilled. Adjustments and moves need
 * `inventory:stock-adjust`, receipts `inventory:receipts-manage` (I15), and
 * every posting the two view keys besides (design §5, `canPost`).
 */
export const NewMovement = ({ kind }: { kind: LineEditorKind }) => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as NewMovementSearch;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const location = useQuery({ ...locationQueryOptions(search.locationId ?? 0), enabled: !!search.locationId });
  const variant = useQuery({ ...variantStockQueryOptions(search.variantId ?? 0), enabled: !!search.variantId });
  const allowed = canPost(meta.data, kind);
  const failed = meta.error ?? sites.error ?? location.error ?? variant.error;
  const loading =
    meta.isPending ||
    sites.isPending ||
    (!!search.locationId && location.isPending) ||
    (!!search.variantId && variant.isPending);

  const siteId = search.siteId ?? location.data?.site.id ?? (sites.data?.length === 1 ? sites.data[0].id : undefined);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navMovements"), to: MOVEMENTS_ROUTE_PATH },
          { label: t(`new_${kind}`) },
        ]}
        title={t(`new_${kind}`)}
        description={t(`newDescription_${kind}`)}
      />
      {failed ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {failed.message}
        </Alert>
      ) : loading ? (
        <ContentSkeleton rows={4} />
      ) : !allowed ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t(`notAllowed_${kind}`)}
        </Alert>
      ) : (
        <MovementForm
          kind={kind}
          sites={sites.data ?? []}
          siteId={siteId}
          locationId={search.locationId}
          variant={variant.data?.variant}
        />
      )}
    </Stack>
  );
};
