import { Alert, Anchor, Group, Pagination, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type CustomerImport, type ImportRowOutcome, importRowsQueryOptions } from "../api/imports";
import { columnLabel } from "../lib/import-columns";
import "../i18n";

const PAGE_SIZE = 50;

/**
 * An import's rows of one outcome (customers import wizard design D8), a page
 * at a time: the failed rows as the errors table — the row, the column as the
 * file spells it, the problem — the created or updated rows with each
 * customer as a link, the onboarding's "did it work?" answered by opening one,
 * and the skipped rows with the earlier row each repeats.
 */
export const ImportRowsTable = ({ imp, outcome }: { imp: CustomerImport; outcome: ImportRowOutcome }) => {
  const { t } = useI18n("customers");
  const [page, setPage] = useState(1);
  const { data, isPending, isError, error } = useQuery(importRowsQueryOptions(imp.id, outcome, page, PAGE_SIZE));

  if (isPending) return <ContentSkeleton rows={3} />;
  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importRowsCouldNotLoad")}>
        <Text size="sm">{error.message}</Text>
      </Alert>
    );
  }
  if (data.data.length === 0) return <EmptyState title={t(`importNoRows_${outcome}`)} />;

  const nameColumn = imp.mapping?.columns.find((c) => c.field === "name" && c.column != null)?.column;
  const failed = outcome === "failed";
  const skipped = outcome === "skipped";
  const nameOf = (cells: string[]) => (nameColumn != null ? (cells[nameColumn] ?? "") : (cells[0] ?? ""));
  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={600}>
        <Table striped verticalSpacing="xs">
          <Table.Caption>{t(`importRowsCaption_${outcome}`)}</Table.Caption>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("importErrorRow")}</Table.Th>
              {failed ? (
                <>
                  <Table.Th>{t("importErrorColumn")}</Table.Th>
                  <Table.Th>{t("importErrorMessage")}</Table.Th>
                </>
              ) : skipped ? (
                <>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("importSkippedReason")}</Table.Th>
                </>
              ) : (
                <>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("name")}</Table.Th>
                </>
              )}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.data.map((row) =>
              failed ? (
                row.errors.map((e, index) => (
                  // A row can fail on the same column twice; the index keeps them apart.
                  <Table.Tr key={`${row.row}:${e.column ?? ""}:${index}`}>
                    <Table.Td>{index === 0 ? row.row : ""}</Table.Td>
                    <Table.Td>{e.column === null ? "—" : columnLabel(imp.header, e.column, t)}</Table.Td>
                    <Table.Td>{e.message}</Table.Td>
                  </Table.Tr>
                ))
              ) : skipped ? (
                <Table.Tr key={row.row}>
                  <Table.Td>{row.row}</Table.Td>
                  <Table.Td>{nameOf(row.cells)}</Table.Td>
                  <Table.Td>{row.errors.map((e) => e.message).join(" ")}</Table.Td>
                </Table.Tr>
              ) : (
                <Table.Tr key={row.row}>
                  <Table.Td>{row.row}</Table.Td>
                  <Table.Td>
                    {row.customerId !== null ? (
                      <Anchor component={Link} to={`/customers/${row.customerId}` as never} size="sm">
                        #{row.customerNumber ?? row.customerId}
                      </Anchor>
                    ) : (
                      "—"
                    )}
                  </Table.Td>
                  <Table.Td>{nameOf(row.cells)}</Table.Td>
                </Table.Tr>
              ),
            )}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {data.pagination.totalPages > 1 && (
        <Group justify="flex-end">
          <Pagination value={page} onChange={setPage} total={data.pagination.totalPages} size="sm" />
        </Group>
      )}
    </Stack>
  );
};
