import { Alert, Stack } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { TransferForm } from "../components/transfer-form";
import { canTransfer } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, TRANSFERS_ROUTE_PATH, transferLinkOptions } from "../lib/routes";
import type { NewTransferSearch } from "../lib/search";

/**
 * A new transfer's draft (phase 2 design §7), opened with the sites the URL
 * names. Saving it, or closing its send dialog, opens the transfer. Needs
 * `inventory:transfers-manage` and both views (I63).
 */
export const NewTransferPage = () => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as NewTransferSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const failed = meta.error ?? sites.error;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navTransfers"), to: TRANSFERS_ROUTE_PATH },
          { label: t("newTransfer") },
        ]}
        title={t("newTransfer")}
        description={t("newTransferDescription")}
      />
      {failed ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {failed.message}
        </Alert>
      ) : meta.isPending || sites.isPending ? (
        <ContentSkeleton rows={4} />
      ) : !canTransfer(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_transfer")}
        </Alert>
      ) : (
        <PageSection>
          <TransferForm
            sites={sites.data ?? []}
            fromSiteId={search.fromSiteId}
            toSiteId={search.toSiteId}
            onFinished={(transfer) => void navigate({ ...transferLinkOptions(transfer.id), replace: true })}
            onCancel={() => void navigate({ to: TRANSFERS_ROUTE_PATH })}
          />
        </PageSection>
      )}
    </Stack>
  );
};
