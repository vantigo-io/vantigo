import { Alert, Anchor, Group, Pagination, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type EnergyImport, type ImportRow, type ImportRowsFilter, importRowsQueryOptions } from "../api/imports";
import { columnLabel } from "../lib/import-columns";
import "../i18n";

const PAGE_SIZE = 50;

/** The tables the import pages show: four by outcome, two by what the row did about its customer. */
export type ImportRowsKind = "failed" | "skipped" | "created" | "updated" | "switched" | "pending";

const filterOf = (kind: ImportRowsKind): ImportRowsFilter =>
  kind === "switched" || kind === "pending" ? { link: kind } : { outcome: kind };

const PointLink = ({ row }: { row: ImportRow }) =>
  row.meteringPointId !== null ? (
    <Anchor component={Link} to={`/energy/metering-points/${row.meteringPointId}` as never} size="sm">
      {row.gsrn ?? row.meteringPointId}
    </Anchor>
  ) : (
    (row.gsrn ?? "—")
  );

const CustomerLink = ({ id, name }: { id: number | null; name: string | null }) =>
  id !== null ? (
    <Anchor component={Link} to={`/customers/${id}` as never} size="sm">
      {name ?? `#${id}`}
    </Anchor>
  ) : (
    "—"
  );

/**
 * An import's rows of one kind (energy import design D4, D9), a page at a
 * time: the failed rows as the errors table — the row, the column as the file
 * spells it, the problem — the skipped totals lines with their reason, the
 * points created or updated with the point and its customer as links, the
 * switches with both customers and the date, and the pending points with the
 * customer they wait for and, after an attach, why one is still waiting.
 */
export const ImportRowsTable = ({ imp, kind }: { imp: EnergyImport; kind: ImportRowsKind }) => {
  const { t, formatters } = useI18n("energy");
  const [page, setPage] = useState(1);
  const { data, isPending, isError, error } = useQuery(importRowsQueryOptions(imp.id, filterOf(kind), page, PAGE_SIZE));

  if (isPending) return <ContentSkeleton rows={3} />;
  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importRowsCouldNotLoad")}>
        <Text size="sm">{error.message}</Text>
      </Alert>
    );
  }
  if (data.data.length === 0) return <EmptyState title={t("importNoRows")} />;

  const date = (value: string | null) => (value ? formatters.formatDate(value) : "—");
  const headers: Record<ImportRowsKind, string[]> = {
    failed: [t("importErrorRow"), t("importErrorColumn"), t("importErrorMessage")],
    skipped: [t("importErrorRow"), t("importSkippedReason")],
    created: [t("importErrorRow"), t("importField_gsrn"), t("customer")],
    updated: [t("importErrorRow"), t("importField_gsrn"), t("customer")],
    switched: [
      t("importErrorRow"),
      t("importField_gsrn"),
      t("importCurrentCustomer"),
      t("importNewCustomer"),
      t("importDate"),
    ],
    pending: [
      t("importErrorRow"),
      t("importField_gsrn"),
      t("customer"),
      t("importCurrentCustomer"),
      t("importErrorMessage"),
    ],
  };
  // After the switch, the customer the point had is the previous one.
  if (kind === "switched" && imp.state !== "checked") headers.switched[2] = t("importPreviousCustomer");

  const cellsOf = (row: ImportRow) => {
    switch (kind) {
      case "skipped":
        return [row.errors.map((e) => e.message).join(" ")];
      case "created":
      case "updated":
        return [<PointLink row={row} />, <CustomerLink id={row.customerId} name={row.customerName} />];
      case "switched":
        return [
          <PointLink row={row} />,
          <CustomerLink id={row.switchFromCustomerId} name={row.switchFromCustomerName} />,
          <CustomerLink id={row.customerId} name={row.customerName} />,
          date(row.supplyStart),
        ];
      default:
        return [
          <PointLink row={row} />,
          <CustomerLink id={row.customerId} name={row.customerName} />,
          <CustomerLink id={row.switchFromCustomerId} name={row.switchFromCustomerName} />,
          row.errors.map((e) => e.message).join(" ") || "—",
        ];
    }
  };

  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={600}>
        <Table striped verticalSpacing="xs">
          <Table.Caption>
            {kind === "switched" && imp.state === "checked"
              ? t("importSwitchTableCaption")
              : t(`importRowsCaption_${kind}`)}
          </Table.Caption>
          <Table.Thead>
            <Table.Tr>
              {headers[kind].map((header) => (
                <Table.Th key={header}>{header}</Table.Th>
              ))}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.data.map((row) =>
              kind === "failed" ? (
                row.errors.map((e, index) => (
                  // A row can fail on the same column twice; the index keeps them apart.
                  <Table.Tr key={`${row.row}:${e.column ?? ""}:${index}`}>
                    <Table.Td>{index === 0 ? row.row : ""}</Table.Td>
                    <Table.Td>{e.column === null ? "—" : columnLabel(imp.header, e.column, t)}</Table.Td>
                    <Table.Td>{e.message}</Table.Td>
                  </Table.Tr>
                ))
              ) : (
                <Table.Tr key={row.row}>
                  <Table.Td>{row.row}</Table.Td>
                  {cellsOf(row).map((cell, index) => (
                    <Table.Td key={index}>{cell}</Table.Td>
                  ))}
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
