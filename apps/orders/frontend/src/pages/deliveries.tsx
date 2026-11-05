import { Alert, Anchor, Chip, Group, Pagination, SegmentedControl, Stack, Table, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type DeliveryListFilters, deliveryListQueryOptions } from "../api/deliveries";
import { PAGE_SIZE } from "../api/list-query";
import { orderListQueryOptions } from "../api/orders";
import type { DeliveryStatusOfDelivery } from "../api/types";
import { OrderTable } from "../components/document-tables";
import { DeliveryDocumentBadge } from "../components/status-badges";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { deliveryLinkOptions, salesOrderLinkOptions } from "../lib/routes";

const statuses: DeliveryStatusOfDelivery[] = ["draft", "posted", "cancelled"];

/**
 * The deliveries (orders design O47): the delivery documents, filtered by
 * state and a search, and *To deliver* — the confirmed orders with a
 * quantity still to deliver, their back orders (restordre). A delivery is
 * made from its order's page.
 */
export const DeliveriesPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t, dateTime } = useOrdersFormat();
  const [view, setView] = useState<"toDeliver" | "deliveries">("toDeliver");
  const [filters, setFilters] = useState<DeliveryListFilters>({ page: 1, status: ["draft"] });
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 300);
  const q = debounced.trim() || undefined;
  const deliveries = useQuery({
    ...deliveryListQueryOptions({ ...filters, q, pageSize: PAGE_SIZE }),
    enabled: view === "deliveries",
  });
  const toDeliver = useQuery({
    ...orderListQueryOptions({ toDeliver: true, q, page, pageSize: PAGE_SIZE }),
    enabled: view === "toDeliver",
  });
  const set = (next: Partial<DeliveryListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const list = view === "deliveries" ? deliveries : toDeliver;
  const total = list.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  return (
    <Stack gap="lg">
      <PageHeader title={t("deliveriesTitle")} description={t("deliveriesDescription")} />
      <Group gap="md" wrap="wrap" align="flex-end">
        <SegmentedControl
          aria-label={t("deliveriesView")}
          value={view}
          onChange={(value) => setView(value as typeof view)}
          data={[
            { value: "toDeliver", label: t("toDeliver") },
            { value: "deliveries", label: t("deliveryDocuments") },
          ]}
          data-assist="orders.deliveries.view"
        />
        {view === "deliveries" && (
          <Chip.Group
            multiple
            value={(filters.status ?? []) as string[]}
            onChange={(value) => set({ status: value as DeliveryStatusOfDelivery[] })}
          >
            <Group gap={6} aria-label={t("status")} role="group">
              {statuses.map((status) => (
                <Chip key={status} value={status} size="xs" variant="outline">
                  {t(`deliveryDocumentStatus.${status}`)}
                </Chip>
              ))}
            </Group>
          </Chip.Group>
        )}
        <TextInput
          label={t("search")}
          placeholder={t("searchDeliveriesPlaceholder")}
          leftSection={<IconSearch size={14} />}
          value={search}
          onChange={(e) => {
            setSearch(e.currentTarget.value);
            set({});
            setPage(1);
          }}
        />
      </Group>
      {list.isError && (
        <Alert color="red" title={t("failedToLoadDeliveries")}>
          {refusalMessage(list.error, t)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && total === 0 && (
        <EmptyState
          title={view === "toDeliver" ? t("nothingToDeliver") : t("noDeliveries")}
          description={view === "toDeliver" ? t("nothingToDeliverDescription") : t("noDeliveriesDescription")}
        />
      )}
      {view === "toDeliver" && toDeliver.data && total > 0 && <OrderTable rows={toDeliver.data.items} />}
      {view === "deliveries" && deliveries.data && total > 0 && (
        <Table.ScrollContainer minWidth={640}>
          <Table verticalSpacing="xs" striped highlightOnHover data-testid="deliveries-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("deliveryNumberColumn")}</Table.Th>
                <Table.Th>{t("orderColumn")}</Table.Th>
                <Table.Th>{t("customer")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th>{t("postedAt")}</Table.Th>
                <Table.Th ta="right">{t("lines")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {deliveries.data.items.map((d) => (
                <Table.Tr key={d.id}>
                  <Table.Td>
                    <Anchor size="sm" renderRoot={(props) => <Link {...deliveryLinkOptions(d.id)} {...props} />}>
                      {t("deliveryNumber", { number: d.number })}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    <Anchor size="sm" renderRoot={(props) => <Link {...salesOrderLinkOptions(d.orderId)} {...props} />}>
                      {t("orderNumber", { number: d.orderNumber })}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{d.customerName}</Text>
                  </Table.Td>
                  <Table.Td>
                    <DeliveryDocumentBadge status={d.status} />
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{d.postedAt ? dateTime(d.postedAt) : "—"}</Text>
                  </Table.Td>
                  <Table.Td ta="right">
                    <Text size="sm">{d.lines}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {list.data && total > 0 && (
        <Group justify="space-between">
          <Text size="sm" c="dimmed">
            {view === "toDeliver" ? t("orderCount", { count: total }) : t("deliveryCount", { count: total })}
          </Text>
          {pages > 1 && (
            <Pagination
              total={pages}
              value={view === "toDeliver" ? page : (filters.page ?? 1)}
              onChange={(next) => (view === "toDeliver" ? setPage(next) : set({ page: next }))}
            />
          )}
        </Group>
      )}
      {!access.canManageDeliveries && (
        <Text size="xs" c="dimmed">
          {t("deliveriesReadOnly")}
        </Text>
      )}
    </Stack>
  );
};
