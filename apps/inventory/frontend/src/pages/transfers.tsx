import { Alert, Button, Select, SimpleGrid, Stack, TextInput } from "@mantine/core";
import { IconAlertCircle, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { type InventoryTransferStatusFilter, transferStatuses } from "../api/transfers";
import { TransferList } from "../components/transfer-list";
import { canTransfer, canViewTransfers } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, newTransferLinkOptions, TRANSFERS_ROUTE_PATH } from "../lib/routes";
import type { TransfersSearch } from "../lib/search";

/**
 * The transfers between sites (phase 2 design §7), newest first, filtered by
 * status — open, the default, is draft, sent and partly received — by the
 * from and the to site, and by a transfer number or a line's SKU. The filters
 * live in the URL, so a site's Incoming and Outgoing link here. New transfer
 * needs `inventory:transfers-manage` (I63).
 */
export const TransfersPage = () => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as TransfersSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const [q, setQ] = useState(search.q ?? "");
  // The field follows the URL: a link, Back or Forward that changes the search changes what it holds.
  const [shownQ, setShownQ] = useState(search.q);
  if (shownQ !== search.q) {
    setShownQ(search.q);
    setQ(search.q ?? "");
  }

  const filter = (patch: Partial<TransfersSearch>) =>
    void navigate({ to: TRANSFERS_ROUTE_PATH, search: { ...search, ...patch }, replace: true });
  const siteOptions = (sites.data ?? []).map((site) => ({
    value: String(site.id),
    label: `${site.code} · ${site.name}`,
  }));
  const status: InventoryTransferStatusFilter | undefined =
    search.status === "all" ? undefined : (search.status ?? "open");

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("navTransfers") }]}
        title={t("navTransfers")}
        description={t("transfersDescription")}
        actions={
          canTransfer(meta.data) && (
            <Button
              data-assist="inventory.transfers.new"
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => (
                <Link
                  {...newTransferLinkOptions({ fromSiteId: search.fromSiteId, toSiteId: search.toSiteId })}
                  {...props}
                />
              )}
            >
              {t("newTransfer")}
            </Button>
          )
        }
      />
      {meta.data && !canViewTransfers(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_viewTransfers")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="md" data-assist="inventory.transfers.list">
            <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
              <Select
                label={t("status")}
                data={[
                  { value: "open", label: t("transferStatus_open") },
                  ...transferStatuses.map((value) => ({ value, label: t(`transferStatus_${value}`) })),
                  { value: "all", label: t("allStatuses") },
                ]}
                value={search.status ?? "open"}
                onChange={(next) =>
                  filter({ status: next === null || next === "open" ? undefined : (next as TransfersSearch["status"]) })
                }
              />
              <Select
                label={t("fromSite")}
                placeholder={t("allSites")}
                clearable
                data={siteOptions}
                value={search.fromSiteId === undefined ? null : String(search.fromSiteId)}
                onChange={(next) => filter({ fromSiteId: next === null ? undefined : Number(next) })}
              />
              <Select
                label={t("toSite")}
                placeholder={t("allSites")}
                clearable
                data={siteOptions}
                value={search.toSiteId === undefined ? null : String(search.toSiteId)}
                onChange={(next) => filter({ toSiteId: next === null ? undefined : Number(next) })}
              />
              <TextInput
                label={t("searchTransfers")}
                placeholder={t("searchTransfersPlaceholder")}
                leftSection={<IconSearch size={16} />}
                value={q}
                onChange={(event) => setQ(event.currentTarget.value)}
                onBlur={() => filter({ q: q.trim() || undefined })}
                onKeyDown={(event) => {
                  if (event.key !== "Enter") return;
                  event.preventDefault();
                  filter({ q: q.trim() || undefined });
                }}
              />
            </SimpleGrid>
            <TransferList filter={{ status, fromSiteId: search.fromSiteId, toSiteId: search.toSiteId, q: search.q }} />
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
