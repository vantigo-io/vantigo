import { Alert, Anchor, Button, Chip, Group, Pagination, Stack, Switch, Table, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  PAGE_SIZE,
  type PurchaseOrderFilters,
  type PurchaseOrderStatus,
  type PurchaseOrderSummary,
  purchaseOrdersQueryOptions,
} from "../api/purchasing";
import { OrderStatusBadges } from "../components/common";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";
import { newPurchaseOrderLinkOptions, purchaseOrderLinkOptions, supplierLinkOptions } from "../lib/routes";

const statuses: PurchaseOrderStatus[] = ["draft", "sent", "partially_received", "received", "closed", "cancelled"];

/**
 * Purchase orders (P23): every order, newest first, with its state, the
 * supplier, the expected day and the goods lines received and invoiced as
 * fractions; filtered by state, Late, Mine, Unconfirmed and a search over the
 * number, the supplier and the supplier's reference. *New purchase order* is
 * offered with purchasing:orders-manage.
 */
export const PurchaseOrdersPage = ({ access = noPurchasingAccess }: { access?: PurchasingAccess }) => {
  const { t } = usePurchasingFormat();
  const [filters, setFilters] = useState<PurchaseOrderFilters>({ page: 1 });
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 300);
  const list = useQuery(
    purchaseOrdersQueryOptions({ ...filters, q: debounced.trim() || undefined, pageSize: PAGE_SIZE }),
  );
  const set = (next: Partial<PurchaseOrderFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const pages = list.data ? Math.max(1, Math.ceil(list.data.total / PAGE_SIZE)) : 1;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("ordersTitle")}
        description={t("ordersDescription")}
        actions={
          access.canManageOrders && (
            <Button
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newPurchaseOrderLinkOptions()} {...props} />}
              data-assist="purchasing.orders.new"
            >
              {t("newOrder")}
            </Button>
          )
        }
      />
      <PageSection>
        <Stack gap="md">
          <Chip.Group
            multiple
            value={[...(filters.status ?? [])]}
            onChange={(value) => set({ status: value as PurchaseOrderStatus[] })}
          >
            <Group gap={6} aria-label={t("status")} role="group">
              {statuses.map((status) => (
                <Chip key={status} value={status} size="xs" variant="outline">
                  {t(`status.${status}`)}
                </Chip>
              ))}
            </Group>
          </Chip.Group>
          <Group align="flex-end" wrap="wrap">
            <TextInput
              label={t("search")}
              placeholder={t("searchOrdersPlaceholder")}
              leftSection={<IconSearch size={14} />}
              value={search}
              onChange={(e) => {
                setSearch(e.currentTarget.value);
                set({});
              }}
              w={320}
            />
            <Switch
              label={t("onlyLate")}
              mb={8}
              checked={filters.late === true}
              onChange={(e) => set({ late: e.currentTarget.checked || undefined })}
            />
            <Switch
              label={t("onlyUnconfirmed")}
              mb={8}
              checked={filters.unconfirmed === true}
              onChange={(e) => set({ unconfirmed: e.currentTarget.checked || undefined })}
            />
            <Switch
              label={t("onlyMine")}
              mb={8}
              checked={filters.mine === true}
              onChange={(e) => set({ mine: e.currentTarget.checked || undefined })}
            />
          </Group>
          {list.isError && (
            <Alert color="red" title={t("failedToLoadOrders")}>
              {refusalMessage(list.error, t)}
            </Alert>
          )}
          {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
          {list.data && list.data.items.length === 0 && (
            <EmptyState title={t("noOrders")} description={t("noOrdersDescription")} />
          )}
          {list.data && list.data.items.length > 0 && (
            <>
              <PurchaseOrderTable rows={list.data.items} />
              <Group justify="space-between">
                <Text size="sm" c="dimmed">
                  {t("orderCount", { count: list.data.total })}
                </Text>
                {pages > 1 && <Pagination total={pages} value={filters.page ?? 1} onChange={(page) => set({ page })} />}
              </Group>
            </>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};

/** The orders as rows, each linking to its page and its supplier. */
export const PurchaseOrderTable = ({ rows }: { rows: readonly PurchaseOrderSummary[] }) => {
  const { t, money, date } = usePurchasingFormat();
  return (
    <Table.ScrollContainer minWidth={760}>
      <Table striped highlightOnHover data-testid="purchase-orders">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("number")}</Table.Th>
            <Table.Th>{t("supplier")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("orderDate")}</Table.Th>
            <Table.Th>{t("expectedOn")}</Table.Th>
            <Table.Th>{t("received")}</Table.Th>
            <Table.Th>{t("invoiced")}</Table.Th>
            <Table.Th ta="right">{t("netTotal")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id}>
              <Table.Td>
                <Anchor renderRoot={(props) => <Link {...purchaseOrderLinkOptions(row.id)} {...props} />}>
                  {row.number ?? t("draftNumber")}
                </Anchor>
              </Table.Td>
              <Table.Td>
                <Anchor
                  renderRoot={(props) => <Link {...supplierLinkOptions(row.supplierId)} {...props} />}
                  c="inherit"
                >
                  {row.supplierName}
                </Anchor>
                {row.supplierReference && (
                  <Text size="xs" c="dimmed">
                    {row.supplierReference}
                  </Text>
                )}
              </Table.Td>
              <Table.Td>
                <OrderStatusBadges status={row.status} late={row.late} unconfirmed={row.unconfirmed} />
              </Table.Td>
              <Table.Td>{date(row.orderDate)}</Table.Td>
              <Table.Td>{row.expectedOn ? date(row.expectedOn) : "—"}</Table.Td>
              <Table.Td>
                {row.goodsLines > 0 ? t("fraction", { done: row.receivedLines, of: row.goodsLines }) : "—"}
              </Table.Td>
              <Table.Td>
                {row.lineCount > 0 ? t("fraction", { done: row.invoicedLines, of: row.lineCount }) : "—"}
              </Table.Td>
              <Table.Td ta="right">{money(row.netTotal, row.currency)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
