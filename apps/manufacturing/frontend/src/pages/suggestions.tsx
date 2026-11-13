import { Alert, Anchor, Badge, Button, Checkbox, Group, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconBulb, IconDownload, IconPlayerPlay } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  buySuggestionsCsv,
  createOrdersFromSuggestions,
  dismissSuggestion,
  type ManufacturingSuggestion,
  startReplenishmentRun,
  suggestionsQueryOptions,
  useSuggestionsLive,
} from "../api/replenishment";
import { saveCsv } from "../api/reports";
import { canManageOrders } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, ORDERS_ROUTE_PATH, orderLinkOptions } from "../lib/routes";

/** A positive decimal with at most three decimals, as the server reads a quantity. */
const QUANTITY = /^\d+([.,]\d{1,3})?$/;

/** Listens to one site's suggestions topic: a component per site, so the hook count never changes. */
const SiteLive = ({ siteId }: { siteId: number }) => {
  useSuggestionsLive(siteId);
  return null;
};

/**
 * The replenishment suggestions (manufacturing phase 2 M111; nb Forslag),
 * under the manufacturing orders: one table per site, make rows first, each
 * with its projected stock, the reorder point, the quantity (editable before
 * an order is made), the need-by date and the reason as a sentence. Select
 * make rows and Create draft MOs or Create and confirm; Dismiss a row until
 * the next run; buy rows are read-only, with Export CSV. Run now asks the
 * nightly job to run at once. Read again on each site's live topic.
 */
export const SuggestionsPage = () => {
  const { t, decimal, day, stamp } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const list = useQuery(suggestionsQueryOptions());
  const manage = canManageOrders(meta.data);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [quantities, setQuantities] = useState<Record<number, string>>({});

  const reason = (s: ManufacturingSuggestion) =>
    t("suggestionReason", {
      projected: decimal(s.reason.projected),
      reorderPoint: decimal(s.reason.reorderPoint),
      onHand: decimal(s.reason.onHand),
      reserved: decimal(s.reason.reserved),
      incoming: decimal(s.reason.incoming),
      inProduction: decimal(s.reason.inProduction),
      neededByProduction: decimal(s.reason.neededByProduction),
      neededBySales: decimal(s.reason.neededBySales),
    });

  const creating = useMutation({
    mutationFn: (confirm: boolean) =>
      createOrdersFromSuggestions({
        confirm,
        items: [...selected].map((id) => {
          const quantity = quantities[id]?.trim().replace(",", ".");
          return quantity ? { suggestionId: id, quantity } : { suggestionId: id };
        }),
      }),
    onSuccess: async (result) => {
      if (result.made.length > 0)
        notifications.show({
          color: "teal",
          title: t("suggestionOrdersMade", { count: result.made.length }),
          message: result.made.map((one) => one.number).join(", "),
        });
      for (const failure of result.failed)
        notifications.show({ color: "red", title: t("suggestionNotMade"), message: failure.detail });
      setSelected(new Set());
      setQuantities({});
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error) }),
  });
  const dismissing = useMutation({
    mutationFn: dismissSuggestion,
    onSuccess: () => invalidate(),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error) }),
  });
  const running = useMutation({
    mutationFn: () => startReplenishmentRun(),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("suggestionsRunStarted"), message: t("suggestionsRunStartedHint") });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error) }),
  });

  const sites = list.data?.sites ?? [];
  const buyRows = sites.flatMap((group) =>
    group.suggestions.filter((s) => s.kind === "buy").map((suggestion) => ({ site: group.site.code, suggestion })),
  );
  const invalidQuantity = [...selected].some((id) => quantities[id] !== undefined && !QUANTITY.test(quantities[id]));
  const toggle = (id: number, on: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("navOrders"), to: ORDERS_ROUTE_PATH },
          { label: t("suggestions") },
        ]}
        title={t("suggestions")}
        description={t("suggestionsDescription")}
        actions={
          <Group gap="xs">
            {buyRows.length > 0 && (
              <Button
                variant="default"
                leftSection={<IconDownload size={16} />}
                onClick={() =>
                  saveCsv({ blob: buySuggestionsCsv(buyRows), fileName: "manufacturing-buy-suggestions.csv" })
                }
              >
                {t("exportBuyCsv")}
              </Button>
            )}
            {manage && (
              <Button
                variant="default"
                leftSection={<IconPlayerPlay size={16} />}
                loading={running.isPending}
                data-assist="manufacturing.suggestions.run"
                onClick={() => running.mutate()}
              >
                {t("runNow")}
              </Button>
            )}
          </Group>
        }
      />
      {sites.map((group) => (
        <SiteLive key={group.site.id} siteId={group.site.id} />
      ))}
      {list.data && (
        <Group gap="md">
          <Text size="sm" c="dimmed" data-testid="last-run">
            {list.data.lastRun
              ? t("lastRun", {
                  at: stamp(list.data.lastRun.finishedAt ?? list.data.lastRun.requestedAt),
                  status: t(`runStatus_${list.data.lastRun.status}`),
                })
              : t("noRunYet")}
          </Text>
          {!list.data.salesCounted && (
            <Badge variant="light" color="yellow">
              {t("salesNotCounted")}
            </Badge>
          )}
        </Group>
      )}
      {manage && sites.length > 0 && (
        <Group gap="xs" data-assist="manufacturing.suggestions.actions">
          <Button
            disabled={selected.size === 0 || invalidQuantity}
            loading={creating.isPending && creating.variables === false}
            onClick={() => creating.mutate(false)}
          >
            {t("createDraftOrders")}
          </Button>
          <Button
            variant="light"
            disabled={selected.size === 0 || invalidQuantity}
            loading={creating.isPending && creating.variables === true}
            onClick={() => creating.mutate(true)}
          >
            {t("createAndConfirm")}
          </Button>
          <Text size="sm" c="dimmed">
            {t("selectedCount", { count: selected.size })}
          </Text>
        </Group>
      )}
      {list.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {list.error.message}
        </Alert>
      ) : list.isPending ? (
        <ContentSkeleton rows={3} />
      ) : sites.length === 0 ? (
        <EmptyState icon={IconBulb} title={t("noSuggestions")} description={t("noSuggestionsHint")} />
      ) : (
        sites.map((group) => (
          <PageSection key={group.site.id} title={`${group.site.code} · ${group.site.name}`}>
            <Table.ScrollContainer minWidth={960}>
              <Table data-assist="manufacturing.suggestions.list">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th w={40} />
                    <Table.Th>{t("variant")}</Table.Th>
                    <Table.Th>{t("suggestionKind")}</Table.Th>
                    <Table.Th ta="right">{t("projected")}</Table.Th>
                    <Table.Th ta="right">{t("reorderPoint")}</Table.Th>
                    <Table.Th>{t("quantity")}</Table.Th>
                    <Table.Th>{t("needBy")}</Table.Th>
                    <Table.Th>{t("reason")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {group.suggestions.map((s) => {
                    const make = s.kind === "make";
                    const value = quantities[s.id] ?? s.quantity;
                    return (
                      <Table.Tr key={s.id}>
                        <Table.Td>
                          {make && manage && (
                            <Checkbox
                              aria-label={t("selectSuggestion", { sku: s.variant.sku })}
                              checked={selected.has(s.id)}
                              onChange={(event) => toggle(s.id, event.currentTarget.checked)}
                            />
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{s.variant.sku}</Text>
                          <Text size="xs" c="dimmed">
                            {s.variant.productName}
                          </Text>
                          {s.reorderOpen && (
                            <Text size="xs" c="orange">
                              {t("reorderOpen")}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={make ? "blue" : "gray"}>
                            {t(`suggestionKind_${s.kind}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td ta="right">{decimal(s.reason.projected)}</Table.Td>
                        <Table.Td ta="right">{decimal(s.reason.reorderPoint)}</Table.Td>
                        <Table.Td>
                          {make && manage ? (
                            <TextInput
                              size="xs"
                              w={110}
                              aria-label={t("quantityOf", { sku: s.variant.sku })}
                              value={value}
                              rightSection={
                                <Text size="xs" c="dimmed">
                                  {s.variant.unit}
                                </Text>
                              }
                              error={QUANTITY.test(value) ? undefined : t("quantityPositive")}
                              onChange={(event) => {
                                const next = event.currentTarget.value;
                                setQuantities((current) => ({ ...current, [s.id]: next }));
                              }}
                            />
                          ) : (
                            `${decimal(s.quantity)} ${s.variant.unit}`
                          )}
                        </Table.Td>
                        <Table.Td>{day(s.needBy)}</Table.Td>
                        <Table.Td>
                          <Text size="xs">{reason(s)}</Text>
                        </Table.Td>
                        <Table.Td>
                          {s.orderId ? (
                            <Anchor
                              size="sm"
                              renderRoot={(props) => <Link {...orderLinkOptions(s.orderId ?? 0)} {...props} />}
                            >
                              {s.orderNumber ?? t("order")}
                            </Anchor>
                          ) : (
                            manage && (
                              <Button
                                size="xs"
                                variant="subtle"
                                color="gray"
                                loading={dismissing.isPending && dismissing.variables === s.id}
                                onClick={() => dismissing.mutate(s.id)}
                              >
                                {t("dismiss")}
                              </Button>
                            )
                          )}
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </PageSection>
        ))
      )}
    </Stack>
  );
};
