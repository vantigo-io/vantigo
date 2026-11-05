import { Alert, Anchor, Badge, Button, Group, Progress, Select, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconClipboardList, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { countStatuses, countsQueryOptions, type InventoryCountStatus } from "../api/counts";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { OpenCountDialog } from "../components/open-count-dialog";
import { canCount } from "../lib/capabilities";
import { countStatusColor, useInventoryFormat } from "../lib/format";
import { COUNTS_ROUTE_PATH, countLinkOptions, INVENTORY_HOME_ROUTE_PATH } from "../lib/routes";
import { validateCountsSearch } from "../lib/search";

/**
 * The counts (I16), newest first: each one's site and scope, its status, who
 * opened it and when, how far the counting has come and how many lines'
 * balances moved since it opened. Filtered by status and site. Open count
 * starts one, as `?new=1` does on arrival. Counts need `inventory:counts-manage` and both views (design §5).
 */
export const CountsPage = () => {
  const { t, stamp } = useInventoryFormat();
  const meta = useQuery(inventoryMetaQueryOptions());
  const allowed = canCount(meta.data);
  const [status, setStatus] = useState<InventoryCountStatus | undefined>("counting");
  const [siteId, setSiteId] = useState<number>();
  // The spotlight's Start count lands here with `new`; the form opens, and closing it drops `new` from the URL.
  const search = validateCountsSearch(useSearch({ strict: false }) as Record<string, unknown>);
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const [opened, setOpening] = useState(false);
  const opening = opened || search.new === 1;
  const closeOpening = () => {
    setOpening(false);
    if (search.new) void navigate({ to: COUNTS_ROUTE_PATH, search: {}, replace: true });
  };
  const sites = useQuery({ ...sitesQueryOptions(), enabled: allowed });
  const counts = useQuery({ ...countsQueryOptions({ status, siteId }), enabled: allowed });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("navCounts") }]}
        title={t("navCounts")}
        description={t("countsDescription")}
        actions={
          allowed && (
            <Button
              data-assist="inventory.counts.new"
              leftSection={<IconPlus size={16} />}
              onClick={() => setOpening(true)}
            >
              {t("openCount")}
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
          {t("notAllowed_count")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="md">
            <Group gap="sm">
              <Select
                label={t("status")}
                w={180}
                clearable
                placeholder={t("allStatuses")}
                data={countStatuses.map((value) => ({ value, label: t(`countStatus_${value}`) }))}
                value={status ?? null}
                onChange={(next) => setStatus((next as InventoryCountStatus | null) ?? undefined)}
              />
              <Select
                label={t("site")}
                w={240}
                clearable
                placeholder={t("allSites")}
                data={(sites.data ?? []).map((one) => ({ value: String(one.id), label: `${one.code} · ${one.name}` }))}
                value={siteId === undefined ? null : String(siteId)}
                onChange={(next) => setSiteId(next === null ? undefined : Number(next))}
              />
            </Group>
            {counts.error ? (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
                {counts.error.message}
              </Alert>
            ) : counts.isPending ? (
              <ContentSkeleton rows={4} />
            ) : counts.data.length === 0 ? (
              <EmptyState size="sm" icon={IconClipboardList} title={t("noCounts")} description={t("noCountsHint")} />
            ) : (
              <Table.ScrollContainer minWidth={760}>
                <Table striped verticalSpacing="xs">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("columnCount")}</Table.Th>
                      <Table.Th>{t("site")}</Table.Th>
                      <Table.Th>{t("countScope")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("columnOpened")}</Table.Th>
                      <Table.Th>{t("columnProgress")}</Table.Th>
                      <Table.Th>{t("columnMoved")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {counts.data.map((count) => (
                      <Table.Tr key={count.id}>
                        <Table.Td>
                          <Anchor size="sm" renderRoot={(props) => <Link {...countLinkOptions(count.id)} {...props} />}>
                            {t("countNumber", { id: count.id })}
                          </Anchor>
                        </Table.Td>
                        <Table.Td>{count.site.code}</Table.Td>
                        <Table.Td>
                          <Text size="sm" ff="monospace">
                            {count.scope.kind === "site" ? t("countWholeSite") : count.scope.fullCode}
                          </Text>
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={countStatusColor[count.status]}>
                            {t(`countStatus_${count.status}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{count.openedBy.displayName}</Text>
                          <Text size="xs" c="dimmed">
                            {stamp(count.openedAt)}
                          </Text>
                        </Table.Td>
                        <Table.Td miw={140}>
                          <Text size="xs">
                            {t("countProgress", { counted: count.countedLines, lines: count.lineCount })}
                          </Text>
                          <Progress
                            size="sm"
                            value={count.lineCount === 0 ? 0 : (count.countedLines / count.lineCount) * 100}
                          />
                        </Table.Td>
                        <Table.Td>
                          {count.movedLines > 0 ? (
                            <Badge variant="light" color="orange">
                              {t("movedLines", { count: count.movedLines })}
                            </Badge>
                          ) : (
                            <Text size="sm" c="dimmed">
                              —
                            </Text>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Stack>
        </PageSection>
      )}
      {opening && allowed && <OpenCountDialog opened onClose={closeOpening} siteId={siteId} />}
    </Stack>
  );
};
