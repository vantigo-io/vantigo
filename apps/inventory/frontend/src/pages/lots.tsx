import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconBarcode, IconPrinter, IconSearch } from "@tabler/icons-react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { holdCauses, lotStatuses, lotsInfiniteQueryOptions, useLotsLive } from "../api/lots";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { LotStatusBadges } from "../components/lot-status";
import { PrintLotLabelsDialog } from "../components/print-lot-labels-dialog";
import { canViewLots } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, LOTS_ROUTE_PATH, lotLinkOptions, variantStockLinkOptions } from "../lib/routes";
import type { LotsSearch } from "../lib/search";

/**
 * The lots (manufacturing phase 2 M60), by item then number: each lot's
 * number, its item, its supplier lot, its expiry and best-before dates, its
 * status and what lies of it — at the site when one is chosen. Filtered by a
 * number, a supplier lot or an SKU, by site, status and hold cause; only lots
 * with stock unless asked. The filters live in the URL. A stock read: both
 * views alone. The list refetches on its live topic and polls while it is
 * down.
 */
export const LotsPage = () => {
  const { t, quantity, day } = useInventoryFormat();
  const search = useSearch({ strict: false }) as LotsSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const live = useLotsLive();
  const [q, setQ] = useState(search.q ?? "");
  const [shownQ, setShownQ] = useState(search.q);
  if (shownQ !== search.q) {
    setShownQ(search.q);
    setQ(search.q ?? "");
  }
  const lots = useInfiniteQuery(
    lotsInfiniteQueryOptions(
      {
        q: search.q,
        siteId: search.siteId,
        variantId: search.variantId,
        status: search.status,
        holdCause: search.holdCause,
        withStock: search.stock === "all" ? false : undefined,
      },
      live,
    ),
  );
  const filter = (patch: Partial<LotsSearch>) =>
    void navigate({ to: LOTS_ROUTE_PATH, search: { ...search, ...patch }, replace: true });
  const rows = lots.data?.pages.flatMap((page) => page.lots) ?? [];
  const [printing, setPrinting] = useState(false);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("lotsTitle") }]}
        title={t("lotsTitle")}
        description={t("lotsDescription")}
        actions={
          rows.length > 0 && (
            <Button variant="default" leftSection={<IconPrinter size={16} />} onClick={() => setPrinting(true)}>
              {t("printLotLabels")}
            </Button>
          )
        }
      />
      <PrintLotLabelsDialog
        opened={printing}
        onClose={() => setPrinting(false)}
        lotIds={rows.slice(0, 1000).map((lot) => lot.id)}
        name="lots"
        onlyShown={lots.hasNextPage || rows.length > 1000}
      />
      {meta.data && !canViewLots(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_viewLots")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="md" data-assist="inventory.lots.list">
            <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
              <TextInput
                label={t("searchLots")}
                placeholder={t("searchLotsPlaceholder")}
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
              <Select
                label={t("site")}
                placeholder={t("allSites")}
                clearable
                data={(sites.data ?? []).map((site) => ({
                  value: String(site.id),
                  label: `${site.code} · ${site.name}`,
                }))}
                value={search.siteId === undefined ? null : String(search.siteId)}
                onChange={(next) => filter({ siteId: next === null ? undefined : Number(next) })}
              />
              <Select
                label={t("columnStatus")}
                placeholder={t("allStatuses")}
                clearable
                data={lotStatuses.map((value) => ({ value, label: t(`lotStatus_${value}`) }))}
                value={search.status ?? null}
                onChange={(next) => filter({ status: (next ?? undefined) as LotsSearch["status"] })}
              />
              <Select
                label={t("holdCause")}
                placeholder={t("anyCause")}
                clearable
                data={holdCauses.map((value) => ({ value, label: t(`holdCause_${value}`) }))}
                value={search.holdCause ?? null}
                onChange={(next) => filter({ holdCause: (next ?? undefined) as LotsSearch["holdCause"] })}
              />
            </SimpleGrid>
            <Checkbox
              label={t("withStockOnly")}
              checked={search.stock !== "all"}
              onChange={(event) => filter({ stock: event.currentTarget.checked ? undefined : "all" })}
            />
            {lots.error ? (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
                {lots.error.message}
              </Alert>
            ) : lots.isPending ? (
              <ContentSkeleton rows={4} />
            ) : rows.length === 0 ? (
              <EmptyState size="sm" icon={IconBarcode} title={t("noLots")} description={t("noLotsHint")} />
            ) : (
              <Table.ScrollContainer minWidth={720}>
                <Table striped verticalSpacing="xs">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("columnLot")}</Table.Th>
                      <Table.Th>{t("columnSku")}</Table.Th>
                      <Table.Th>{t("columnProduct")}</Table.Th>
                      <Table.Th>{t("columnSupplierLot")}</Table.Th>
                      <Table.Th>{t("columnExpiry")}</Table.Th>
                      <Table.Th>{t("columnBestBefore")}</Table.Th>
                      <Table.Th>{t("columnStatus")}</Table.Th>
                      <Table.Th ta="right">{t("columnOnHand")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {rows.map((lot) => (
                      <Table.Tr key={lot.id}>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            ff="monospace"
                            renderRoot={(props) => <Link {...lotLinkOptions(lot.id)} {...props} />}
                          >
                            {lot.number}
                          </Anchor>
                        </Table.Td>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...variantStockLinkOptions(lot.variant.id)} {...props} />}
                          >
                            {lot.variant.sku}
                          </Anchor>
                        </Table.Td>
                        <Table.Td>{lot.variant.productName}</Table.Td>
                        <Table.Td>{lot.supplierLot ?? ""}</Table.Td>
                        <Table.Td>
                          <Text size="sm" c={lot.expired ? "red" : undefined}>
                            {lot.expiry ? day(lot.expiry) : "—"}
                          </Text>
                        </Table.Td>
                        <Table.Td>{lot.bestBefore ? day(lot.bestBefore) : "—"}</Table.Td>
                        <Table.Td>
                          <Group gap={4}>
                            {lot.status === "available" && !lot.expired ? (
                              <Text size="sm" c="dimmed">
                                {t("lotStatus_available")}
                              </Text>
                            ) : (
                              <LotStatusBadges status={lot.status} expired={lot.expired} />
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td ta="right">{quantity(lot.onHand, lot.variant.unit)}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
            {lots.hasNextPage && (
              <Group justify="center">
                <Button variant="subtle" loading={lots.isFetchingNextPage} onClick={() => void lots.fetchNextPage()}>
                  {t("loadMore")}
                </Button>
              </Group>
            )}
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
