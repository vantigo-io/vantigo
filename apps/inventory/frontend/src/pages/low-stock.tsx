import { Alert, Button, Group, Paper, Select, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload, IconSearch, IconTrendingDown, IconTruckDelivery } from "@tabler/icons-react";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { downloadBlob } from "../api/labels";
import { inventoryMetaQueryOptions } from "../api/meta";
import { exportLowStock, type InventoryLowStockRow, lowStockInfiniteQueryOptions } from "../api/placements";
import { sitesQueryOptions } from "../api/sites";
import { createTransfer, type InventoryTransferInput } from "../api/transfers";
import { LowStockList } from "../components/low-stock-list";
import { canTransfer, canViewTransfers } from "../lib/capabilities";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { lowStockKey, lowStockTransfer, richestSource, uniqueLowStockRows } from "../lib/low-stock";
import { INVENTORY_HOME_ROUTE_PATH, LOW_STOCK_ROUTE_PATH, transferLinkOptions } from "../lib/routes";
import type { LowStockSearch } from "../lib/search";

/** Today's calendar day where the viewer is, as the export's file is named (`low-stock-2026-11-09.csv`). */
const today = () => {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
};

/**
 * The low-stock list (phase 2 design I58–I60, §7): every item at or below its
 * reorder point at an active site, filtered by site and searched by SKU or
 * name, a page at a time. Rows at one site are chosen and *Create transfer to
 * {site} from [site]* drafts a transfer of their suggested quantities —
 * asking first when a draft is already open for one of them — and opens it;
 * it needs `inventory:transfers-manage` (I63). *Export CSV* downloads the list
 * as filtered. The list needs both views.
 */
export const LowStockPage = () => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as LowStockSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const invalidate = useInvalidateInventory();
  const meta = useQuery(inventoryMetaQueryOptions());
  const allowed = canViewTransfers(meta.data);
  const transferring = canTransfer(meta.data);
  const sites = useQuery({ ...sitesQueryOptions(), enabled: allowed });
  const [q, setQ] = useState(search.q ?? "");
  // The field follows the URL: a link, Back or Forward that changes the search changes what it holds.
  const [shownQ, setShownQ] = useState(search.q);
  if (shownQ !== search.q) {
    setShownQ(search.q);
    setQ(search.q ?? "");
  }
  const filter = { siteId: search.siteId, q: search.q };
  const list = useInfiniteQuery({ ...lowStockInfiniteQueryOptions(filter), enabled: allowed });
  // A row shown on an earlier page is not shown again: a SKU refreshed between two page reads can move a row
  // past the cursor, and the server answers it once more (I58).
  const rows = uniqueLowStockRows(list.data?.pages.flatMap((page) => page.rows) ?? []);
  // The keys chosen; the rows are always the list's as last read, so a row a refetch dropped is no longer chosen
  // and a suggestion that moved is sent as it is now.
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [fromSiteId, setFromSiteId] = useState<number>();

  const chosen = rows.filter((row) => selected.has(lowStockKey(row)));
  const toSite = chosen[0]?.site;
  const fromOptions = (sites.data ?? [])
    .filter((site) => site.id !== toSite?.id)
    .map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }));
  const from = fromSiteId ?? richestSource(chosen) ?? (fromOptions[0] ? Number(fromOptions[0].value) : undefined);

  const clear = () => {
    setSelected(new Set());
    setFromSiteId(undefined);
  };
  const filterBy = (patch: Partial<LowStockSearch>) => {
    clear();
    void navigate({ to: LOW_STOCK_ROUTE_PATH, search: { ...search, ...patch }, replace: true });
  };
  const applySearch = () => {
    const next = q.trim() || undefined;
    if (next !== search.q) filterBy({ q: next });
  };

  const exporting = useMutation({
    mutationFn: () => exportLowStock(filter),
    onSuccess: (blob) => downloadBlob(blob, `low-stock-${today()}.csv`),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotExport"), message: refusalMessage(t, error) }),
  });

  const creating = useMutation({
    mutationFn: (input: InventoryTransferInput) => createTransfer(input),
    onSuccess: async (transfer) => {
      notifications.show({
        color: "teal",
        title: t("draftSaved"),
        message: t("transferNumber", { number: transfer.number }),
      });
      clear();
      await invalidate();
      await navigate(transferLinkOptions(transfer.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreateTransfer"), message: refusalMessage(t, error) }),
  });

  const create = () => {
    if (!toSite || from === undefined) return;
    const input = lowStockTransfer(chosen, from, toSite.id);
    // A draft to the site already holds one of them (I59): a second is made only when the person says so.
    if (chosen.some((row) => row.openDrafts.length > 0))
      modals.openConfirmModal({
        title: t("createTransferAgainTitle"),
        children: <Text size="sm">{t("createTransferAgain", { site: toSite.name })}</Text>,
        labels: { confirm: t("createTransferAnyway"), cancel: t("cancel") },
        onConfirm: () => creating.mutate(input),
      });
    else creating.mutate(input);
  };

  const siteOptions = (sites.data ?? []).map((site) => ({
    value: String(site.id),
    label: `${site.code} · ${site.name}`,
  }));

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("navLowStock") }]}
        title={t("lowStockTitle")}
        description={t("lowStockDescription")}
        actions={
          allowed && (
            <Button
              variant="default"
              leftSection={<IconDownload size={16} />}
              loading={exporting.isPending}
              onClick={() => exporting.mutate()}
            >
              {t("exportCsv")}
            </Button>
          )
        }
      />
      {meta.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {meta.error.message}
        </Alert>
      ) : meta.isPending ? (
        <ContentSkeleton rows={4} />
      ) : !allowed ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_lowStock")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="md" data-assist="inventory.low-stock.list">
            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              <Select
                label={t("site")}
                placeholder={t("allSites")}
                clearable
                data={siteOptions}
                value={search.siteId === undefined ? null : String(search.siteId)}
                onChange={(next) => filterBy({ siteId: next === null ? undefined : Number(next) })}
              />
              <TextInput
                label={t("searchTransfers")}
                placeholder={t("lowStockSearch")}
                leftSection={<IconSearch size={16} />}
                value={q}
                onChange={(event) => setQ(event.currentTarget.value)}
                onBlur={applySearch}
                onKeyDown={(event) => {
                  if (event.key !== "Enter") return;
                  event.preventDefault();
                  applySearch();
                }}
              />
            </SimpleGrid>
            {list.error ? (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
                {list.error.message}
              </Alert>
            ) : list.isPending ? (
              <ContentSkeleton rows={4} />
            ) : rows.length === 0 ? (
              <EmptyState size="sm" icon={IconTrendingDown} title={t("lowStockEmpty")} />
            ) : (
              <>
                <LowStockList
                  rows={rows}
                  showSite={search.siteId === undefined}
                  {...(transferring
                    ? {
                        selected: new Set(chosen.map(lowStockKey)),
                        chosenSiteId: toSite?.id,
                        onToggle: (row: InventoryLowStockRow, checked: boolean) => {
                          const next = new Set(chosen.map(lowStockKey));
                          if (checked) next.add(lowStockKey(row));
                          else next.delete(lowStockKey(row));
                          if (next.size === 0) setFromSiteId(undefined);
                          setSelected(next);
                        },
                      }
                    : {})}
                />
                {list.hasNextPage && (
                  <Group justify="center">
                    <Button variant="light" onClick={() => void list.fetchNextPage()} loading={list.isFetchingNextPage}>
                      {t("loadMore")}
                    </Button>
                  </Group>
                )}
              </>
            )}
            {transferring && toSite && (
              <Paper withBorder p="sm">
                <Group gap="sm" align="flex-end">
                  <Text size="sm" fw={500}>
                    {t("lowStockSelected", { count: chosen.length })}
                  </Text>
                  <Select
                    label={t("createTransferTo", { site: toSite.name })}
                    data={fromOptions}
                    value={from === undefined ? null : String(from)}
                    onChange={(next) => setFromSiteId(next === null ? undefined : Number(next))}
                    allowDeselect={false}
                  />
                  <Button
                    leftSection={<IconTruckDelivery size={16} />}
                    disabled={from === undefined}
                    loading={creating.isPending}
                    onClick={create}
                  >
                    {t("createTransfer")}
                  </Button>
                </Group>
              </Paper>
            )}
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
