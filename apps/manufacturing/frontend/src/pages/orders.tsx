import {
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  Group,
  MultiSelect,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconArrowsJoin, IconClipboardList, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  mergeOrders,
  ORDER_PAGE_SIZE,
  orderOrigins,
  orderSorts,
  orderStatuses,
  ordersQueryOptions,
  reservationStatuses,
  useOrdersLive,
} from "../api/orders";
import type { ManufacturingPerson } from "../api/people";
import { manufacturingSitesQueryOptions } from "../api/sites";
import type { ManufacturingVariantRef } from "../api/variants";
import { PersonPicker } from "../components/person-picker";
import { VariantPicker } from "../components/variant-picker";
import { useRecordActions } from "../lib/actions";
import { canManageOrders } from "../lib/capabilities";
import { orderStatusColor, reservationColor, useManufacturingFormat } from "../lib/format";
import {
  MANUFACTURING_HOME_ROUTE_PATH,
  NEW_ORDER_ROUTE_PATH,
  ORDERS_ROUTE_PATH,
  orderLinkOptions,
} from "../lib/routes";
import { type OrdersSearch, orderFilterOf } from "../lib/search";

/**
 * The manufacturing orders (M24): filtered by status — the open ones by
 * default — site, variant, due range, late, origin, responsible and
 * reservation status, sorted by due date, number or priority, a page at a
 * time. The filters live in the URL. The list shows the reservation status
 * only, never live availability (M21a), and follows `manufacturing.orders`,
 * polling only while live is down. New manufacturing order needs
 * `manufacturing:orders-manage`.
 */
export const OrdersPage = () => {
  const { t, quantity, day } = useManufacturingFormat();
  const search = useSearch({ strict: false }) as OrdersSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const live = useOrdersLive();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const page = search.page ?? 1;
  const orders = useQuery(ordersQueryOptions(orderFilterOf(search), live));
  const [q, setQ] = useState(search.q ?? "");
  // The variant and the responsible filters show what they name once a row of it has been seen or it was picked here.
  const [variant, setVariant] = useState<ManufacturingVariantRef>();
  const [person, setPerson] = useState<ManufacturingPerson>();
  const shownVariant =
    search.variantId === undefined
      ? undefined
      : variant?.id === search.variantId
        ? variant
        : orders.data?.items.find((order) => order.variant.id === search.variantId)?.variant;
  const shownPerson =
    search.responsibleUserId === undefined
      ? undefined
      : person?.id === search.responsibleUserId
        ? person
        : (orders.data?.items.find((order) => order.responsible?.id === search.responsibleUserId)?.responsible ??
          undefined);
  const siteName = new Map((sites.data ?? []).map((site) => [site.id, site.code]));
  // Merge (manufacturing phase 2 M125): the drafts and confirmed orders chosen
  // here, each with the revision this view showed — an order changed since is
  // revision_conflict, never merged as it is now unseen. A choice lasts while
  // its page and filter do.
  const { run, confirm } = useRecordActions();
  const [chosen, setChosen] = useState<readonly { id: number; number: string; revision: number }[]>([]);
  const searchKey = JSON.stringify(search);
  const [chosenFor, setChosenFor] = useState(searchKey);
  if (chosenFor !== searchKey) {
    setChosenFor(searchKey);
    setChosen([]);
  }
  const manage = canManageOrders(meta.data);
  const mergeable = (status: string) => status === "draft" || status === "confirmed";
  const merge = () =>
    confirm(
      t("mergeOrdersTitle"),
      t("mergeOrdersBody", { numbers: chosen.map((order) => order.number).join(", ") }),
      t("mergeOrders"),
      () =>
        void run(
          async () => {
            const result = await mergeOrders(chosen.map((order) => ({ id: order.id, revision: order.revision })));
            setChosen([]);
            void navigate(orderLinkOptions(result.order.id));
          },
          t("ordersMerged"),
          t("couldNotMerge"),
        ),
    );

  const filter = (patch: Partial<OrdersSearch>) =>
    void navigate({ to: ORDERS_ROUTE_PATH, search: { ...search, page: undefined, ...patch }, replace: true });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH }, { label: t("navOrders") }]}
        title={t("navOrders")}
        description={t("ordersDescription")}
        actions={
          manage && (
            <Group gap="xs">
              {chosen.length >= 2 && (
                <Button
                  variant="default"
                  data-assist="manufacturing.orders.merge"
                  leftSection={<IconArrowsJoin size={16} />}
                  onClick={merge}
                >
                  {t("mergeOrdersCount", { count: chosen.length })}
                </Button>
              )}
              <Button
                data-assist="manufacturing.orders.new"
                leftSection={<IconPlus size={16} />}
                renderRoot={(props) => <Link to={NEW_ORDER_ROUTE_PATH} {...props} />}
              >
                {t("newOrder")}
              </Button>
            </Group>
          )
        }
      />
      <PageSection>
        <Stack gap="md" data-assist="manufacturing.orders.list">
          <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} data-assist="manufacturing.orders.filters">
            <TextInput
              label={t("search")}
              placeholder={t("searchOrdersPlaceholder")}
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
            <MultiSelect
              label={t("status")}
              placeholder={search.status ? undefined : t("openStatuses")}
              data={orderStatuses.map((value) => ({ value, label: t(`orderStatus_${value}`) }))}
              value={search.status ?? []}
              onChange={(next) => filter({ status: next.length > 0 ? (next as OrdersSearch["status"]) : undefined })}
            />
            <Select
              label={t("site")}
              placeholder={t("allSites")}
              clearable
              data={(sites.data ?? []).map((site) => ({
                value: String(site.id),
                label: `${site.code} · ${site.name}`,
              }))}
              value={search.siteId ? String(search.siteId) : null}
              onChange={(next) => filter({ siteId: next ? Number(next) : undefined })}
            />
            <VariantPicker
              label={t("variant")}
              value={shownVariant}
              onChange={(next) => {
                setVariant(next);
                filter({ variantId: next?.id });
              }}
            />
            <TextInput
              type="date"
              label={t("dueFrom")}
              value={search.dueFrom ?? ""}
              onChange={(event) => filter({ dueFrom: event.currentTarget.value || undefined })}
            />
            <TextInput
              type="date"
              label={t("dueTo")}
              value={search.dueTo ?? ""}
              onChange={(event) => filter({ dueTo: event.currentTarget.value || undefined })}
            />
            <Select
              label={t("origin")}
              placeholder={t("anyOrigin")}
              clearable
              data={orderOrigins.map((value) => ({ value, label: t(`origin_${value}`) }))}
              value={search.origin ?? null}
              onChange={(next) => filter({ origin: (next ?? undefined) as OrdersSearch["origin"] })}
            />
            <PersonPicker
              label={t("responsible")}
              value={shownPerson}
              onChange={(next) => {
                setPerson(next);
                filter({ responsibleUserId: next?.id });
              }}
            />
            <Select
              label={t("reservation")}
              placeholder={t("anyReservation")}
              clearable
              data={reservationStatuses.map((value) => ({ value, label: t(`reservation_${value}`) }))}
              value={search.reservation ?? null}
              onChange={(next) => filter({ reservation: (next ?? undefined) as OrdersSearch["reservation"] })}
            />
            <Select
              label={t("sortBy")}
              allowDeselect={false}
              data={orderSorts.map((value) => ({ value, label: t(`sort_${value}`) }))}
              value={search.sort ?? "dueDate"}
              onChange={(next) =>
                filter({ sort: next === null || next === "dueDate" ? undefined : (next as OrdersSearch["sort"]) })
              }
            />
            <Switch
              label={t("lateOnly")}
              mt={{ base: 0, md: "xl" }}
              checked={search.late === true}
              onChange={(event) => filter({ late: event.currentTarget.checked ? true : undefined })}
            />
          </SimpleGrid>
          {orders.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {orders.error.message}
            </Alert>
          ) : orders.isPending ? (
            <ContentSkeleton rows={3} />
          ) : orders.data.items.length === 0 ? (
            <EmptyState
              icon={IconClipboardList}
              title={t("noOrders")}
              description={search.status === undefined && canManageOrders(meta.data) ? t("noOrdersHint") : undefined}
            />
          ) : (
            <>
              <Table.ScrollContainer minWidth={900}>
                <Table highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      {manage && <Table.Th w={40} />}
                      <Table.Th>{t("orderNumber")}</Table.Th>
                      <Table.Th>{t("product")}</Table.Th>
                      <Table.Th>{t("producedOfQuantity")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("reservation")}</Table.Th>
                      <Table.Th>{t("dueDate")}</Table.Th>
                      <Table.Th>{t("site")}</Table.Th>
                      <Table.Th>{t("responsible")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {orders.data.items.map((order) => (
                      <Table.Tr key={order.id}>
                        {manage && (
                          <Table.Td>
                            {mergeable(order.status) && (
                              <Checkbox
                                aria-label={t("chooseToMerge", { number: order.number })}
                                checked={chosen.some((one) => one.id === order.id)}
                                onChange={(event) => {
                                  const on = event.currentTarget.checked;
                                  setChosen((current) =>
                                    on
                                      ? [...current, { id: order.id, number: order.number, revision: order.revision }]
                                      : current.filter((one) => one.id !== order.id),
                                  );
                                }}
                              />
                            )}
                          </Table.Td>
                        )}
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            <Anchor renderRoot={(props) => <Link {...orderLinkOptions(order.id)} {...props} />}>
                              {order.number}
                            </Anchor>
                            {order.priority === "urgent" && (
                              <Badge size="xs" variant="filled" color="red">
                                {t("priority_urgent")}
                              </Badge>
                            )}
                            {order.origin.cancelled && (
                              <Badge size="xs" variant="filled" color="orange" data-testid="origin-cancelled">
                                {t("originCancelled")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{order.variant.sku}</Text>
                          <Text size="xs" c="dimmed">
                            {order.variant.productName}
                          </Text>
                        </Table.Td>
                        <Table.Td>
                          {t("producedOf", {
                            produced: quantity(order.produced),
                            quantity: quantity(order.quantity, order.variant.unit),
                          })}
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={orderStatusColor[order.status]}>
                            {t(`orderStatus_${order.status}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          {order.reservationStatus ? (
                            <Badge variant="light" color={reservationColor[order.reservationStatus]}>
                              {t(`reservation_${order.reservationStatus}`)}
                            </Badge>
                          ) : (
                            "—"
                          )}
                        </Table.Td>
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
                        <Table.Td>{siteName.get(order.siteId) ?? ""}</Table.Td>
                        <Table.Td>{order.responsible?.displayName ?? ""}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
              {orders.data.total > ORDER_PAGE_SIZE && (
                <Group justify="flex-end">
                  <Pagination
                    total={Math.ceil(orders.data.total / ORDER_PAGE_SIZE)}
                    value={page}
                    onChange={(next) => filter({ page: next > 1 ? next : undefined })}
                  />
                </Group>
              )}
            </>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};
