import { Alert, Anchor, Badge, Group, Pagination, Stack, Table, Text, Tooltip, VisuallyHidden } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useId, useState } from "react";
import {
  type CustomerImport,
  type ImportRowOutcome,
  type ImportRowPreview,
  importRowsQueryOptions,
} from "../api/imports";
import { columnLabel } from "../lib/import-columns";
import "../i18n";

const PAGE_SIZE = 50;

/**
 * A value Brønnøysund filled in, marked as the check's preview shows it: the
 * badge is focusable and shows its hint on focus as on hover, and the hint is
 * its accessible description for a screen reader.
 */
const FromRegistry = () => {
  const { t } = useI18n("customers");
  const hintId = useId();
  return (
    <>
      <Tooltip
        label={t("importFromRegistryHint")}
        withArrow
        multiline
        w={260}
        events={{ hover: true, focus: true, touch: true }}
      >
        <Badge size="xs" variant="light" color="gray" tabIndex={0} aria-describedby={hintId} style={{ cursor: "help" }}>
          {t("importFromRegistry")}
        </Badge>
      </Tooltip>
      <VisuallyHidden id={hintId}>{t("importFromRegistryHint")}</VisuallyHidden>
    </>
  );
};

/** A preview's postal address on one line: the lines, the postal code and city, the region, the country. */
const addressLine = (address: ImportRowPreview["postalAddress"]) =>
  address
    ? [
        address.line1,
        address.line2,
        [address.postalCode, address.city].filter(Boolean).join(" "),
        address.region,
        address.country.toUpperCase(),
      ]
        .filter(Boolean)
        .join(", ")
    : "—";

/** One value of a preview, with the marker when Brønnøysund gave it. */
const PreviewValue = ({ value, fromRegistry }: { value: string; fromRegistry: boolean }) => (
  <Group gap={6} wrap="nowrap">
    <Text size="sm">{value}</Text>
    {fromRegistry && <FromRegistry />}
  </Group>
);

/**
 * An import's rows of one outcome (customers import wizard design D8), a page
 * at a time: the failed rows as the errors table — the row, the column as the
 * file spells it, the problem — the created or updated rows with each
 * customer as a link, the onboarding's "did it work?" answered by opening one,
 * and the skipped rows with the earlier row each repeats. A checked import's
 * created or updated rows (checked) are the check's preview instead: what each
 * would write — the name, the organisation number, the postal address — each
 * value Brønnøysund filled in marked.
 */
export const ImportRowsTable = ({
  imp,
  outcome,
  checked = false,
}: {
  imp: CustomerImport;
  outcome: ImportRowOutcome;
  checked?: boolean;
}) => {
  const { t } = useI18n("customers");
  const [page, setPage] = useState(1);
  const { data, isPending, isError, error } = useQuery(
    importRowsQueryOptions(imp.id, outcome, page, PAGE_SIZE, checked, imp.finishedAt),
  );

  if (isPending) return <ContentSkeleton rows={3} />;
  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importRowsCouldNotLoad")}>
        <Text size="sm">{error.message}</Text>
      </Alert>
    );
  }
  const preview = checked && (outcome === "created" || outcome === "updated");
  const captionKey = preview ? (outcome === "created" ? "willCreate" : "willUpdate") : outcome;
  if (data.data.length === 0) return <EmptyState title={t(`importNoRows_${captionKey}`)} />;

  const nameColumn = imp.mapping?.columns.find((c) => c.field === "name" && c.column != null)?.column;
  const failed = outcome === "failed";
  const skipped = outcome === "skipped";
  const nameOf = (cells: string[]) => (nameColumn != null ? (cells[nameColumn] ?? "") : (cells[0] ?? ""));
  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={600}>
        <Table striped verticalSpacing="xs">
          <Table.Caption>{t(`importRowsCaption_${captionKey}`)}</Table.Caption>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("importErrorRow")}</Table.Th>
              {preview ? (
                <>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("importPreviewOrganisationNumber")}</Table.Th>
                  <Table.Th>{t("importPreviewPostalAddress")}</Table.Th>
                </>
              ) : failed ? (
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
              preview ? (
                <PreviewRow key={row.row} row={row.row} preview={row.preview} fallbackName={nameOf(row.cells)} />
              ) : failed ? (
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

/** One row of the check's preview: what the row would write, or its name as the file has it when the row has none. */
const PreviewRow = ({
  row,
  preview,
  fallbackName,
}: {
  row: number;
  preview: ImportRowPreview | null;
  fallbackName: string;
}) => {
  const { t } = useI18n("customers");
  if (!preview) {
    return (
      <Table.Tr>
        <Table.Td>{row}</Table.Td>
        <Table.Td>{fallbackName}</Table.Td>
        <Table.Td>—</Table.Td>
        <Table.Td>—</Table.Td>
      </Table.Tr>
    );
  }
  const from = (field: string) => preview.fromRegistry.includes(field);
  const identity = preview.legalIdentity;
  return (
    <Table.Tr>
      <Table.Td>{row}</Table.Td>
      <Table.Td>
        <PreviewValue value={preview.name} fromRegistry={from("name")} />
      </Table.Td>
      <Table.Td>
        {identity ? (
          <Stack gap={0}>
            <Text size="sm">{identity.id}</Text>
            {identity.name !== preview.name && (
              <Group gap={6} wrap="nowrap">
                <Text size="xs" c="dimmed">
                  {identity.name}
                </Text>
                {from("legalName") && <FromRegistry />}
              </Group>
            )}
          </Stack>
        ) : (
          "—"
        )}
      </Table.Td>
      <Table.Td>
        {preview.postalAddressCleared ? (
          <Text size="sm" c="red">
            {t("importPostalAddressCleared")}
          </Text>
        ) : (
          <PreviewValue
            value={addressLine(preview.postalAddress)}
            fromRegistry={preview.fromRegistry.some((field) => field.startsWith("postal"))}
          />
        )}
      </Table.Td>
    </Table.Tr>
  );
};
