import { Alert, Anchor, Group, Pagination, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconPuzzle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { unbuildsQueryOptions } from "../api/orders";
import { useManufacturingFormat } from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, orderLinkOptions, unbuildLinkOptions } from "../lib/routes";

/** A page of the unbuilds list. */
const UNBUILD_PAGE_SIZE = 50;

/**
 * The unbuilds (manufacturing phase 2 M123; nb Demonteringer), newest first:
 * what each took apart — the order and the lot when it came from one — and
 * what it restored, into which lots, each linked to its page. An unbuild is
 * made from a done order's page.
 */
export const UnbuildsPage = () => {
  const { t, quantity, stamp } = useManufacturingFormat();
  const [page, setPage] = useState(1);
  const list = useQuery(unbuildsQueryOptions({ page }));
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH }, { label: t("navUnbuilds") }]}
        title={t("navUnbuilds")}
        description={t("unbuildsDescription")}
      />
      <PageSection>
        <div data-assist="manufacturing.unbuilds.list">
          {list.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {list.error.message}
            </Alert>
          ) : list.isPending ? (
            <ContentSkeleton rows={3} />
          ) : list.data.items.length === 0 ? (
            <EmptyState icon={IconPuzzle} title={t("noUnbuilds")} description={t("noUnbuildsHint")} />
          ) : (
            <Stack gap="md">
              <Table.ScrollContainer minWidth={900}>
                <Table>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("unbuildNumber")}</Table.Th>
                      <Table.Th>{t("product")}</Table.Th>
                      <Table.Th>{t("quantity")}</Table.Th>
                      <Table.Th>{t("order")}</Table.Th>
                      <Table.Th>{t("restored")}</Table.Th>
                      <Table.Th>{t("postedAt")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {list.data.items.map((unbuild) => (
                      <Table.Tr key={unbuild.id}>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...unbuildLinkOptions(unbuild.id)} {...props} />}
                          >
                            {unbuild.number}
                          </Anchor>
                          {unbuild.quarantined && unbuild.releasedAt === null && (
                            <Text size="xs" c="orange">
                              {t("unbuildQuarantined")}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{unbuild.variant.sku}</Text>
                          {unbuild.lotNumber && (
                            <Text size="xs" c="dimmed">
                              {t("lotValue", { number: unbuild.lotNumber })}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>{quantity(unbuild.quantity, unbuild.variant.unit)}</Table.Td>
                        <Table.Td>
                          {unbuild.orderId && unbuild.orderNumber ? (
                            <Anchor
                              size="sm"
                              renderRoot={(props) => <Link {...orderLinkOptions(unbuild.orderId ?? 0)} {...props} />}
                            >
                              {unbuild.orderNumber}
                            </Anchor>
                          ) : (
                            t("unbuildFreeStanding")
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Stack gap={0}>
                            {unbuild.lines.map((line) => (
                              <Text size="xs" key={`${line.variant.id}-${line.lotId ?? 0}`}>
                                {`${line.variant.sku} · ${quantity(line.quantity, line.variant.unit)}${line.lotNumber ? ` · ${line.lotNumber}` : ""}`}
                              </Text>
                            ))}
                          </Stack>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{stamp(unbuild.postedAt)}</Text>
                          <Text size="xs" c="dimmed">
                            {unbuild.postedByDisplay}
                          </Text>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
              {list.data.total > UNBUILD_PAGE_SIZE && (
                <Group justify="flex-end">
                  <Pagination total={Math.ceil(list.data.total / UNBUILD_PAGE_SIZE)} value={page} onChange={setPage} />
                </Group>
              )}
            </Stack>
          )}
        </div>
      </PageSection>
    </Stack>
  );
};
