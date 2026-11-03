import { Alert, Anchor, Badge, Button, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconArrowsExchange } from "@tabler/icons-react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { type MovementsFilter, movementsInfiniteQueryOptions } from "../api/movements";
import { movementKindColor, useInventoryFormat } from "../lib/format";
import { movementLinkOptions } from "../lib/routes";

export interface MovementListProps {
  filter: MovementsFilter;
  /** Rows a page; the list's own page asks for more than a panel does. */
  limit?: number;
}

/**
 * Movements newest first (I30), a page at a time by the server's cursor, with
 * Load more while there are more: the kind, when, the site, who, the reason
 * or supplier, and the number of lines.
 */
export const MovementList = ({ filter, limit = 100 }: MovementListProps) => {
  const { t, stamp } = useInventoryFormat();
  const movements = useInfiniteQuery(movementsInfiniteQueryOptions(filter, limit));

  if (movements.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {movements.error.message}
      </Alert>
    );
  if (movements.isPending) return <ContentSkeleton rows={4} />;
  const rows = movements.data.pages.flatMap((page) => page.movements);
  if (rows.length === 0) return <EmptyState size="sm" icon={IconArrowsExchange} title={t("noMovements")} />;

  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={640}>
        <Table striped verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("columnMovement")}</Table.Th>
              <Table.Th>{t("columnPostedAt")}</Table.Th>
              <Table.Th>{t("site")}</Table.Th>
              <Table.Th>{t("columnDetails")}</Table.Th>
              <Table.Th>{t("columnPostedBy")}</Table.Th>
              <Table.Th ta="right">{t("columnLines")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((movement) => (
              <Table.Tr key={movement.id}>
                <Table.Td>
                  <Group gap="xs" wrap="nowrap">
                    <Badge variant="light" color={movementKindColor[movement.kind]}>
                      {t(`kind_${movement.kind}`)}
                    </Badge>
                    <Anchor size="sm" renderRoot={(props) => <Link {...movementLinkOptions(movement.id)} {...props} />}>
                      {t("movementNumber", { id: movement.id })}
                    </Anchor>
                  </Group>
                </Table.Td>
                <Table.Td>{stamp(movement.postedAt)}</Table.Td>
                <Table.Td>{movement.site.code}</Table.Td>
                <Table.Td>
                  <Text size="sm" lineClamp={1}>
                    {[
                      movement.reason ? t(`reason_${movement.reason}`) : undefined,
                      movement.supplierName,
                      movement.supplierReference,
                      movement.note,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </Text>
                </Table.Td>
                <Table.Td>{movement.postedBy.displayName}</Table.Td>
                <Table.Td ta="right">{movement.lineCount}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {movements.hasNextPage && (
        <Group justify="center">
          <Button variant="light" onClick={() => void movements.fetchNextPage()} loading={movements.isFetchingNextPage}>
            {t("loadMore")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};
