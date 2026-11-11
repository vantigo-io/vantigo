import { Alert, Anchor, Badge, Button, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconListDetails, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, KpiCard, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { ordersQueryOptions, useOrdersLive } from "../api/orders";
import { manufacturingStatsQueryOptions } from "../api/stats";
import { canManageBoms, canManageOrders } from "../lib/capabilities";
import { orderStatusColor, useManufacturingFormat } from "../lib/format";
import {
  BOMS_ROUTE_PATH,
  NEW_BOM_ROUTE_PATH,
  NEW_ORDER_ROUTE_PATH,
  ORDERS_ROUTE_PATH,
  orderLinkOptions,
  WORK_CENTERS_ROUTE_PATH,
} from "../lib/routes";

/** How many of the late and due-this-week orders the overview lists. */
const DUE_LIST_SIZE = 20;

/** The last day of the viewer's week, Sunday, as a calendar date (`2026-11-15`). */
const endOfWeek = (today: Date = new Date()): string => {
  const sunday = new Date(today.getFullYear(), today.getMonth(), today.getDate() + ((7 - today.getDay()) % 7));
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${sunday.getFullYear()}-${pad(sunday.getMonth() + 1)}-${pad(sunday.getDate())}`;
};

/** The open orders that are late or due by the end of this week, by due date (M7). */
const DueThisWeek = () => {
  const { t, quantity, day } = useManufacturingFormat();
  const live = useOrdersLive();
  const orders = useQuery(ordersQueryOptions({ dueTo: endOfWeek(), sort: "dueDate", pageSize: DUE_LIST_SIZE }, live));
  return (
    <PageSection title={t("dueThisWeek")}>
      <div data-assist="manufacturing.overview.due">
        {orders.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {orders.error.message}
          </Alert>
        ) : orders.isPending ? (
          <ContentSkeleton rows={2} />
        ) : orders.data.items.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("nothingDueThisWeek")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={600}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("orderNumber")}</Table.Th>
                  <Table.Th>{t("product")}</Table.Th>
                  <Table.Th>{t("quantity")}</Table.Th>
                  <Table.Th>{t("dueDate")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {orders.data.items.map((order) => (
                  <Table.Tr key={order.id}>
                    <Table.Td>
                      <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(order.id)} {...props} />}>
                        {order.number}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>{`${order.variant.sku} · ${order.variant.productName}`}</Table.Td>
                    <Table.Td>{quantity(order.quantity, order.variant.unit)}</Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        {order.dueDate ? day(order.dueDate) : "—"}
                        {order.late && (
                          <Badge size="xs" variant="light" color="red">
                            {t("late")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={orderStatusColor[order.status]}>
                        {t(`orderStatus_${order.status}`)}
                      </Badge>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </div>
    </PageSection>
  );
};

/**
 * The app's home (M7): the manufacturing orders' cards — to start today, in
 * progress, late — beside the work centers' and the BoMs', all from
 * `GET /stats`, and below them the orders late or due this week. With no
 * BoM yet the page points to Bills of materials instead.
 */
export const ManufacturingOverviewPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const stats = useQuery(manufacturingStatsQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("overviewTitle")}
        description={t("overviewDescription")}
        actions={
          (canManageBoms(meta.data) || canManageOrders(meta.data)) && (
            <Group gap="xs">
              {canManageOrders(meta.data) && (
                <Button
                  data-assist="manufacturing.overview.new-order"
                  leftSection={<IconPlus size={16} />}
                  renderRoot={(props) => <Link to={NEW_ORDER_ROUTE_PATH} {...props} />}
                >
                  {t("newOrder")}
                </Button>
              )}
              {canManageBoms(meta.data) && (
                <Button
                  variant={canManageOrders(meta.data) ? "default" : "filled"}
                  data-assist="manufacturing.overview.new-bom"
                  leftSection={<IconPlus size={16} />}
                  renderRoot={(props) => <Link to={NEW_BOM_ROUTE_PATH} {...props} />}
                >
                  {t("newBom")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {stats.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {stats.error.message}
        </Alert>
      ) : stats.isPending ? (
        <ContentSkeleton rows={1} rowHeight={150} />
      ) : (
        <>
          <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="md" data-assist="manufacturing.overview.cards">
            <KpiCard
              label={t("ordersToStartToday")}
              value={stats.data.ordersToStartToday}
              href={`${ORDERS_ROUTE_PATH}?status=confirmed`}
            />
            <KpiCard
              label={t("ordersInProgress")}
              value={stats.data.ordersInProgress}
              href={`${ORDERS_ROUTE_PATH}?status=in_progress`}
            />
            <KpiCard label={t("ordersLate")} value={stats.data.ordersLate} href={`${ORDERS_ROUTE_PATH}?late=true`} />
            <KpiCard
              label={t("activeWorkCenters")}
              value={stats.data.activeWorkCenters}
              href={WORK_CENTERS_ROUTE_PATH}
            />
            <KpiCard
              label={t("blockedWorkCenters")}
              value={stats.data.blockedWorkCenters}
              href={WORK_CENTERS_ROUTE_PATH}
            />
            <KpiCard label={t("activeBoms")} value={stats.data.activeBoms} href={BOMS_ROUTE_PATH} />
          </SimpleGrid>
          {stats.data.activeBoms > 0 && <DueThisWeek />}
          {stats.data.activeBoms === 0 && (
            <EmptyState
              icon={IconListDetails}
              title={t("noBomsYet")}
              description={t("noBomsYetHint")}
              action={
                <Button variant="light" renderRoot={(props) => <Link to={BOMS_ROUTE_PATH} {...props} />}>
                  {t("goToBoms")}
                </Button>
              }
            />
          )}
        </>
      )}
    </Stack>
  );
};
