import { Alert, Anchor, Button, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconTruckDelivery } from "@tabler/icons-react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { type TransfersFilter, transfersInfiniteQueryOptions } from "../api/transfers";
import { useInventoryFormat } from "../lib/format";
import { transferLinkOptions } from "../lib/routes";
import { OverdueMark, TransferStatusBadge } from "./transfer-status";

export interface TransferListProps {
  filter: TransfersFilter;
  /** The differences' column instead of the lines', as the variant page lists them (I46). */
  showMissing?: boolean;
  /** What an empty list says. */
  emptyTitle?: string;
}

/**
 * Transfers newest first (I36), a page at a time by the server's cursor, with
 * Load more while there are more: the number, the two sites, the status, the
 * lines, the expected arrival and the overdue mark (I62).
 */
export const TransferList = ({ filter, showMissing, emptyTitle }: TransferListProps) => {
  const { t, quantity, day } = useInventoryFormat();
  const transfers = useInfiniteQuery(transfersInfiniteQueryOptions(filter));

  if (transfers.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {transfers.error.message}
      </Alert>
    );
  if (transfers.isPending) return <ContentSkeleton rows={4} />;
  const rows = transfers.data.pages.flatMap((page) => page.transfers);
  if (rows.length === 0)
    return <EmptyState size="sm" icon={IconTruckDelivery} title={emptyTitle ?? t("noTransfers")} />;

  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={720}>
        <Table striped verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("columnTransfer")}</Table.Th>
              <Table.Th>{t("fromSite")}</Table.Th>
              <Table.Th>{t("toSite")}</Table.Th>
              <Table.Th>{t("status")}</Table.Th>
              <Table.Th ta="right">{showMissing ? t("notReceived") : t("columnLines")}</Table.Th>
              <Table.Th>{t("expectedArrival")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((transfer) => (
              <Table.Tr key={transfer.id}>
                <Table.Td>
                  <Anchor size="sm" renderRoot={(props) => <Link {...transferLinkOptions(transfer.id)} {...props} />}>
                    {t("transferNumber", { number: transfer.number })}
                  </Anchor>
                  {transfer.reference && (
                    <Text size="xs" c="dimmed" lineClamp={1}>
                      {transfer.reference}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td>{transfer.fromSite.code}</Table.Td>
                <Table.Td>{transfer.toSite.code}</Table.Td>
                <Table.Td>
                  <TransferStatusBadge status={transfer.status} />
                </Table.Td>
                <Table.Td ta="right">{showMissing ? quantity(transfer.missing) : transfer.lineCount}</Table.Td>
                <Table.Td>
                  <Group gap="xs" wrap="nowrap">
                    <Text size="sm">{transfer.expectedArrival ? day(transfer.expectedArrival) : "—"}</Text>
                    {transfer.overdue && <OverdueMark />}
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {transfers.hasNextPage && (
        <Group justify="center">
          <Button variant="light" onClick={() => void transfers.fetchNextPage()} loading={transfers.isFetchingNextPage}>
            {t("loadMore")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};
