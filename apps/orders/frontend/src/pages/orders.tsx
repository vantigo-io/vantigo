import {
  Alert,
  Button,
  Chip,
  Group,
  Pagination,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { PAGE_SIZE } from "../api/list-query";
import { type OrderListFilters, orderListQueryOptions } from "../api/orders";
import type { OrderStatus } from "../api/types";
import { CustomerPicker } from "../components/customer-picker";
import { OrderTable } from "../components/document-tables";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { newSalesOrderLinkOptions } from "../lib/routes";

const statuses: OrderStatus[] = ["draft", "confirmed", "cancelled"];

/**
 * The orders list (O18, O31a): the open ones by default — neither done nor
 * cancelled — or all, filtered by state, customer, the caller's own and a
 * search; each row with its delivery and invoice status and the done tag.
 * *New order* is offered with orders:sales-manage.
 */
export const OrdersPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const [filters, setFilters] = useState<OrderListFilters>({ page: 1, open: true });
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 300);
  const list = useQuery(orderListQueryOptions({ ...filters, q: debounced.trim() || undefined, pageSize: PAGE_SIZE }));
  const set = (next: Partial<OrderListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const pages = list.data ? Math.max(1, Math.ceil(list.data.total / PAGE_SIZE)) : 1;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("salesOrdersTitle")}
        description={t("salesOrdersDescription")}
        actions={
          access.canManageOrders && (
            <Button
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newSalesOrderLinkOptions()} {...props} />}
              data-assist="orders.orders.new"
            >
              {t("newSalesOrder")}
            </Button>
          )
        }
      />
      <Stack gap="sm">
        <Group gap="md" wrap="wrap">
          <SegmentedControl
            aria-label={t("openOrAll")}
            value={filters.open ? "open" : "all"}
            onChange={(value) => set({ open: value === "open" ? true : undefined })}
            data={[
              { value: "open", label: t("openOrders") },
              { value: "all", label: t("allOrders") },
            ]}
          />
          <Chip.Group
            multiple
            value={filters.status ?? []}
            onChange={(value) => set({ status: value as OrderStatus[] })}
          >
            <Group gap={6} aria-label={t("status")} role="group">
              {statuses.map((status) => (
                <Chip key={status} value={status} size="xs" variant="outline">
                  {t(`orderStatus.${status}`)}
                </Chip>
              ))}
            </Group>
          </Chip.Group>
        </Group>
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
          />
          {access.canViewCustomers && (
            <CustomerPicker
              label={t("customerFilter")}
              anyStatus
              value={filters.customerId ?? null}
              onChange={(id) => set({ customerId: id ?? undefined })}
            />
          )}
          {access.userId && (
            <Switch
              label={t("onlyMine")}
              mb={8}
              checked={filters.salespersonId === access.userId}
              onChange={(e) => set({ salespersonId: e.currentTarget.checked ? access.userId : undefined })}
            />
          )}
        </Group>
      </Stack>
      {list.isError && (
        <Alert color="red" title={t("failedToLoadOrders")}>
          {refusalMessage(list.error, t)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && list.data.items.length === 0 && (
        <EmptyState
          title={t("noOrders")}
          description={filters.open ? t("noOpenOrdersDescription") : t("noOrdersDescription")}
        />
      )}
      {list.data && list.data.items.length > 0 && (
        <>
          <OrderTable rows={list.data.items} />
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("orderCount", { count: list.data.total })}
            </Text>
            {pages > 1 && <Pagination total={pages} value={filters.page ?? 1} onChange={(page) => set({ page })} />}
          </Group>
        </>
      )}
    </Stack>
  );
};
