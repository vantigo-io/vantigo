import { Alert, Anchor, Button, Group, NumberInput, Select, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCalendarDue, IconDownload, IconPrinter } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { downloadBlob } from "../api/labels";
import {
  DEFAULT_EXPIRING_DAYS,
  expiringLotsQueryOptions,
  exportExpiringLots,
  lotStatuses,
  useLotsLive,
} from "../api/lots";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { LotStatusBadges } from "../components/lot-status";
import { PrintLotLabelsDialog } from "../components/print-lot-labels-dialog";
import { canViewLots } from "../lib/capabilities";
import { refusalMessage, useInventoryFormat } from "../lib/format";
import { EXPIRING_ROUTE_PATH, INVENTORY_HOME_ROUTE_PATH, lotLinkOptions, variantStockLinkOptions } from "../lib/routes";
import type { ExpiringSearch } from "../lib/search";

/** Whole days from one date (YYYY-MM-DD) to another, negative once it has passed. */
const daysBetween = (from: string, to: string) => Math.round((Date.parse(to) - Date.parse(from)) / 86_400_000);

/**
 * The lots expiring soon (manufacturing phase 2 M72): every lot holding stock
 * whose expiry falls within the days chosen — 30 unless asked — or has
 * passed, by expiry, each with the days left, its item, its status and what
 * lies of it, at the site when one is chosen. The filters live in the URL.
 * Export CSV hands over the same rows; Print labels prints the lots' labels.
 * A stock read: both views alone. It refetches on the lots' live topic.
 */
export const ExpiringPage = () => {
  const { t, quantity, day } = useInventoryFormat();
  const search = useSearch({ strict: false }) as ExpiringSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const live = useLotsLive();
  const filter = { days: search.days, siteId: search.siteId, status: search.status };
  const page = useQuery(expiringLotsQueryOptions(filter, live));
  const [days, setDays] = useState<number | "">(search.days ?? DEFAULT_EXPIRING_DAYS);
  const go = (patch: Partial<ExpiringSearch>) =>
    void navigate({ to: EXPIRING_ROUTE_PATH, search: { ...search, ...patch }, replace: true });
  const lots = page.data?.lots ?? [];

  const exporting = useMutation({
    mutationFn: () => exportExpiringLots(filter),
    onSuccess: (blob) => downloadBlob(blob, `expiring-${page.data?.today ?? "today"}.csv`),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotExport"), message: refusalMessage(t, error) }),
  });
  const [printing, setPrinting] = useState(false);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("expiringTitle") }]}
        title={t("expiringTitle")}
        description={t("expiringDescription")}
        actions={
          lots.length > 0 && (
            <Group gap="xs">
              <Button variant="default" leftSection={<IconPrinter size={16} />} onClick={() => setPrinting(true)}>
                {t("printLotLabels")}
              </Button>
              <Button
                variant="default"
                leftSection={<IconDownload size={16} />}
                loading={exporting.isPending}
                onClick={() => exporting.mutate()}
              >
                {t("exportCsv")}
              </Button>
            </Group>
          )
        }
      />
      <PrintLotLabelsDialog
        opened={printing}
        onClose={() => setPrinting(false)}
        lotIds={lots.map((lot) => lot.id)}
        name={`expiring-${page.data?.today ?? ""}`}
      />
      {meta.data && !canViewLots(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_viewLots")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="md" data-assist="inventory.expiring.list">
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              <NumberInput
                label={t("expiringWithinDays")}
                min={1}
                max={365}
                allowDecimal={false}
                allowNegative={false}
                value={days}
                onChange={(value) => setDays(value === "" ? "" : Number(value))}
                onBlur={() =>
                  go({
                    days: days === "" || days === DEFAULT_EXPIRING_DAYS ? undefined : Math.min(365, Math.max(1, days)),
                  })
                }
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
                onChange={(next) => go({ siteId: next === null ? undefined : Number(next) })}
              />
              <Select
                label={t("columnStatus")}
                placeholder={t("allStatuses")}
                clearable
                data={lotStatuses.map((value) => ({ value, label: t(`lotStatus_${value}`) }))}
                value={search.status ?? null}
                onChange={(next) => go({ status: (next ?? undefined) as ExpiringSearch["status"] })}
              />
            </SimpleGrid>
            {page.error ? (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
                {page.error.message}
              </Alert>
            ) : page.isPending ? (
              <ContentSkeleton rows={4} />
            ) : lots.length === 0 ? (
              <EmptyState
                size="sm"
                icon={IconCalendarDue}
                title={t("noExpiring")}
                description={t("noExpiringHint", { date: day(page.data.until) })}
              />
            ) : (
              <>
                <Table.ScrollContainer minWidth={720}>
                  <Table striped verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("columnExpiry")}</Table.Th>
                        <Table.Th ta="right">{t("columnDaysLeft")}</Table.Th>
                        <Table.Th>{t("columnLot")}</Table.Th>
                        <Table.Th>{t("columnSku")}</Table.Th>
                        <Table.Th>{t("columnProduct")}</Table.Th>
                        <Table.Th>{t("columnStatus")}</Table.Th>
                        <Table.Th ta="right">{t("columnOnHand")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {lots.map((lot) => {
                        const left = lot.expiry ? daysBetween(page.data.today, lot.expiry) : 0;
                        return (
                          <Table.Tr key={lot.id} data-expired={lot.expired ? "true" : undefined}>
                            <Table.Td>
                              <Text size="sm" c={lot.expired ? "red" : undefined}>
                                {lot.expiry ? day(lot.expiry) : "—"}
                              </Text>
                            </Table.Td>
                            <Table.Td ta="right">
                              <Text size="sm" c={left < 0 ? "red" : left <= 7 ? "orange" : undefined}>
                                {left < 0 ? t("daysAgo", { count: -left }) : t("daysLeft", { count: left })}
                              </Text>
                            </Table.Td>
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
                            <Table.Td>
                              {lot.status === "available" && !lot.expired ? (
                                <Text size="sm" c="dimmed">
                                  {t("lotStatus_available")}
                                </Text>
                              ) : (
                                <LotStatusBadges status={lot.status} expired={lot.expired} />
                              )}
                            </Table.Td>
                            <Table.Td ta="right">{quantity(lot.onHand, lot.variant.unit)}</Table.Td>
                          </Table.Tr>
                        );
                      })}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
                {page.data.more && (
                  <Text size="sm" c="dimmed">
                    {t("expiringMore")}
                  </Text>
                )}
              </>
            )}
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
