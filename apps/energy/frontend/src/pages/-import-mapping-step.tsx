import {
  ActionIcon,
  Alert,
  Box,
  Button,
  Card,
  Group,
  ScrollArea,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  checkImport,
  type EnergyImport,
  IMPORT_FIELD_GROUPS,
  type ImportFieldGroup,
  type ImportMapping,
  type ImportMappingColumn,
  type ImportMode,
  importQueryKey,
  saveImportMapping,
} from "../api/imports";
import { ApiValidationError } from "../api/request";
import { columnLabel } from "../lib/import-columns";
import "../i18n";

/**
 * What the host says the person may do (energy import design D5): the modes
 * the metering-point and meter permissions allow, and whether the customer
 * link — mapping the organisation number and the start, and attaching the
 * pending points afterwards — is theirs, which `energy:supply-periods-manage`
 * decides.
 */
export interface ImportCapabilities {
  /** `energy:metering-points-manage` and `energy:meters-manage`: a point is created with its first meter. */
  canCreate: boolean;
  /** `energy:metering-points-manage`. */
  canUpdate: boolean;
  /** `energy:supply-periods-manage`. */
  canLinkCustomers: boolean;
}

/** How long the page waits after a change before saving the mapping. */
const SAVE_DEBOUNCE_MS = 400;

/** How many of a column's values the mapping table shows beside its header. */
const SAMPLES_SHOWN = 3;

const NOT_IMPORTED = "";

const sameMapping = (a: ImportMapping, b: ImportMapping) => JSON.stringify(a) === JSON.stringify(b);

/**
 * Step 1 of an import (energy import design D3, D9): what the rows do, which
 * column becomes which field, the values every row shares, and the preview —
 * the first rows as the point would be read, each cell's error marked. The
 * mapping is saved as it changes, debounced, and the preview is the save's
 * answer. **Check file** is the step's one action; it needs the GSRN mapped,
 * since every row is matched by it.
 *
 * The customer-link group is not offered to a person who may not write supply
 * periods, and the modes they may not run are disabled with the key named;
 * the server refuses either regardless, and its refusal is shown as it words
 * it.
 */
export const ImportMappingStep = ({ imp, capabilities }: { imp: EnergyImport; capabilities: ImportCapabilities }) => {
  const { t } = useI18n("energy");
  const queryClient = useQueryClient();
  // A suggestion may name the customer link or a mode the person may not
  // use; those are dropped rather than saved only to be refused.
  const [mapping, setMapping] = useState<ImportMapping>(() => {
    const initial = imp.mapping ?? imp.suggested;
    const customerFields = IMPORT_FIELD_GROUPS.find((group) => group.key === "customer")?.fields ?? [];
    const columns = capabilities.canLinkCustomers
      ? initial.columns
      : initial.columns.filter((c) => !customerFields.includes(c.field));
    const mode: ImportMode =
      !capabilities.canCreate && capabilities.canUpdate
        ? "update"
        : capabilities.canCreate && !capabilities.canUpdate
          ? "create"
          : initial.mode;
    return columns === initial.columns && mode === initial.mode ? initial : { mode, columns };
  });
  const [refusals, setRefusals] = useState<string[]>([]);
  const saved = useRef<ImportMapping | null>(imp.mapping);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const save = useMutation({
    mutationFn: (next: ImportMapping) => saveImportMapping(imp.id, next),
    onSuccess: (result, next) => {
      saved.current = next;
      setRefusals([]);
      queryClient.setQueryData(importQueryKey(imp.id), result);
    },
    onError: (error: Error) => {
      setRefusals(error instanceof ApiValidationError ? Object.values(error.fields).flat() : [error.message]);
    },
  });
  const check = useMutation({
    mutationFn: () => checkImport(imp.id),
    onSuccess: (result) => queryClient.setQueryData(importQueryKey(imp.id), result),
    onError: (error: Error) => setRefusals([error.message]),
  });

  // Save what changed, a little after it changed; the first render saves the
  // suggestion when nothing was saved yet, so a check can follow at once.
  useEffect(() => {
    if (saved.current && sameMapping(saved.current, mapping)) return;
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => save.mutate(mapping), saved.current ? SAVE_DEBOUNCE_MS : 0);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
    // save.mutate is stable; the mapping is the one input.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mapping]);

  const setColumns = (fn: (columns: ImportMappingColumn[]) => ImportMappingColumn[]) =>
    setMapping((current) => ({ ...current, columns: fn(current.columns) }));

  const fieldOfColumn = (index: number) => mapping.columns.find((c) => c.column === index)?.field ?? NOT_IMPORTED;
  const columnOfField = (field: string) => mapping.columns.find((c) => c.field === field && c.column != null)?.column;
  const fixedEntries = mapping.columns.filter((c) => c.value != null);
  const isFixed = (field: string) => fixedEntries.some((c) => c.field === field);

  const groupAllowed = (key: ImportFieldGroup["key"]) => key !== "customer" || capabilities.canLinkCustomers;

  /** The Select's data: every field the person may map, by group; a field taken elsewhere is disabled and says where. */
  const fieldOptions = (forColumn?: number) =>
    IMPORT_FIELD_GROUPS.filter((group) => groupAllowed(group.key)).map((group) => ({
      group: t(`importGroup_${group.key}`),
      items: group.fields
        // The GSRN is the match key: always a column, never one value for every row.
        .filter((field) => forColumn != null || field !== "gsrn")
        .map((field) => {
          const takenBy = columnOfField(field);
          const taken = (takenBy != null && takenBy !== forColumn) || (isFixed(field) && forColumn != null);
          return {
            value: field,
            label: taken
              ? t("importFieldTaken", {
                  field: t(`importField_${field}`),
                  by: takenBy != null ? columnLabel(imp.header, takenBy, t) : t("importSameValue"),
                })
              : t(`importField_${field}`),
            disabled: taken,
          };
        }),
    }));

  const mapColumn = (index: number, field: string | null) =>
    setColumns((columns) => {
      const rest = columns.filter((c) => c.column !== index);
      return field ? [...rest.filter((c) => c.field !== field), { field, column: index }] : rest;
    });
  const setFixed = (field: string, value: string) =>
    setColumns((columns) => [...columns.filter((c) => c.field !== field), { field, value }]);
  const replaceFixed = (from: string, to: string | null) =>
    setColumns((columns) => {
      const previous = columns.find((c) => c.field === from);
      const rest = columns.filter((c) => c.field !== from && c.field !== to);
      return to ? [...rest, { field: to, value: previous?.value ?? "" }] : rest;
    });
  const unmappedFields = IMPORT_FIELD_GROUPS.filter((group) => groupAllowed(group.key)).flatMap((group) =>
    group.fields.filter((f) => f !== "gsrn" && columnOfField(f) == null && !isFixed(f)),
  );

  // The preview's columns: the mapped fields in the groups' order, each with
  // the file column it is read from (a fixed value has none).
  const previewFields = useMemo(
    () =>
      IMPORT_FIELD_GROUPS.flatMap((group) =>
        group.fields
          .filter((field) => mapping.columns.some((c) => c.field === field))
          .map((field) => ({ field, column: mapping.columns.find((c) => c.field === field)?.column ?? null })),
      ),
    [mapping],
  );
  const cleanRows = imp.sample.filter((row) => row.errors.length === 0).length;

  const gsrnMapped = columnOfField("gsrn") != null;
  const canCheck = gsrnMapped && !save.isPending && !check.isPending;

  const modeOptions: { value: ImportMode; allowed: boolean; key: string }[] = [
    {
      value: "create",
      allowed: capabilities.canCreate,
      key: "energy:metering-points-manage, energy:meters-manage",
    },
    { value: "update", allowed: capabilities.canUpdate, key: "energy:metering-points-manage" },
    {
      value: "createOrUpdate",
      allowed: capabilities.canCreate && capabilities.canUpdate,
      key: "energy:metering-points-manage, energy:meters-manage",
    },
  ];

  return (
    <Stack gap="lg">
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Text fw={600}>{t("importWhatToDo")}</Text>
          <SegmentedControl
            value={mapping.mode}
            onChange={(value) => setMapping((current) => ({ ...current, mode: value as ImportMode }))}
            data={modeOptions.map((option) => ({
              value: option.value,
              disabled: !option.allowed,
              label: (
                <Tooltip label={t("importModeNeeds", { key: option.key })} disabled={option.allowed}>
                  <span>{t(`importMode_${option.value}`)}</span>
                </Tooltip>
              ),
            }))}
          />
          <Text size="sm" c="dimmed">
            {t(`importModeHelp_${mapping.mode}`)}
          </Text>
        </Stack>
      </Card>

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <div>
            <Text fw={600}>{t("importColumnsTitle")}</Text>
            <Text size="sm" c="dimmed">
              {t("importColumnsHelp")}
            </Text>
            {capabilities.canLinkCustomers && (
              <Text size="sm" c="dimmed" mt="xs">
                {t("importCustomerLinkHelp")}
              </Text>
            )}
          </div>
          <Table.ScrollContainer minWidth={700}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("importColumnInFile")}</Table.Th>
                  <Table.Th>{t("importColumnSamples")}</Table.Th>
                  <Table.Th>{t("importImportedAs")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {imp.header.map((_, index) => {
                  const label = columnLabel(imp.header, index, t);
                  const samples = imp.sample
                    .map((row) => row.cells[index] ?? "")
                    .filter((cell) => cell.trim() !== "")
                    .slice(0, SAMPLES_SHOWN);
                  return (
                    <Table.Tr key={index}>
                      <Table.Td>
                        <Text size="sm" fw={500}>
                          {label}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c="dimmed" lineClamp={1}>
                          {samples.length > 0 ? samples.join(" · ") : "—"}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Select
                          aria-label={t("importImportedAsFor", { column: label })}
                          data={[{ value: NOT_IMPORTED, label: t("importNotImported") }, ...fieldOptions(index)]}
                          value={fieldOfColumn(index)}
                          onChange={(value) => mapColumn(index, value || null)}
                          searchable
                          allowDeselect={false}
                          maw={320}
                        />
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>

          <Stack gap="xs">
            <div>
              <Text fw={600} size="sm">
                {t("importSameValueTitle")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("importSameValueHelp")}
              </Text>
            </div>
            {fixedEntries.map((entry) => (
              <Group key={entry.field} gap="sm" align="flex-end" wrap="nowrap">
                <Select
                  aria-label={t("importSameValueField")}
                  data={fieldOptions()}
                  value={entry.field}
                  onChange={(value) => replaceFixed(entry.field, value)}
                  allowDeselect={false}
                  searchable
                  maw={280}
                />
                <TextInput
                  aria-label={t("importSameValueFor", { field: t(`importField_${entry.field}`) })}
                  value={entry.value ?? ""}
                  onChange={(event) => setFixed(entry.field, event.currentTarget.value)}
                  maw={280}
                />
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={t("importRemoveSameValue", { field: t(`importField_${entry.field}`) })}
                  onClick={() => replaceFixed(entry.field, null)}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ))}
            <Group>
              <Button
                variant="light"
                size="compact-sm"
                leftSection={<IconPlus size={14} />}
                disabled={unmappedFields.length === 0}
                onClick={() => setFixed(unmappedFields[0], "")}
              >
                {t("importAddSameValue")}
              </Button>
            </Group>
          </Stack>
        </Stack>
      </Card>

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <div>
            <Text fw={600}>{t("importPreviewTitle")}</Text>
            <Text size="sm" c="dimmed">
              {previewFields.length === 0
                ? t("importPreviewNothingMapped")
                : t("importPreviewSummary", { clean: cleanRows, total: imp.sample.length })}
            </Text>
          </div>
          {previewFields.length > 0 && (
            <ScrollArea>
              <Table verticalSpacing="xs" withColumnBorders>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("importErrorRow")}</Table.Th>
                    {previewFields.map(({ field }) => (
                      <Table.Th key={field}>{t(`importField_${field}`)}</Table.Th>
                    ))}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {imp.sample.map((row) => {
                    const rowLevel = row.errors.filter((e) => e.column === null);
                    return (
                      <Table.Tr key={row.row}>
                        <Table.Td>
                          {rowLevel.length > 0 ? (
                            <Tooltip label={rowLevel.map((e) => e.message).join(" · ")} multiline maw={360}>
                              <Text size="sm" c="red" fw={600}>
                                {row.row}
                              </Text>
                            </Tooltip>
                          ) : (
                            <Text size="sm">{row.row}</Text>
                          )}
                        </Table.Td>
                        {previewFields.map(({ field, column }) => {
                          const value =
                            column === null
                              ? (mapping.columns.find((c) => c.field === field)?.value ?? "")
                              : (row.cells[column] ?? "");
                          const problems = column === null ? [] : row.errors.filter((e) => e.column === column);
                          const cell = (
                            <Text size="sm" c={problems.length > 0 ? "red" : undefined} lineClamp={1}>
                              {value || "—"}
                            </Text>
                          );
                          return (
                            <Table.Td
                              key={field}
                              style={problems.length > 0 ? { background: "var(--mantine-color-red-light)" } : undefined}
                            >
                              {problems.length > 0 ? (
                                <Tooltip label={problems.map((e) => e.message).join(" · ")} multiline maw={360}>
                                  {cell}
                                </Tooltip>
                              ) : (
                                cell
                              )}
                            </Table.Td>
                          );
                        })}
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </ScrollArea>
          )}
          {imp.sample.some((row) => row.errors.length > 0) && (
            <Box role="list" aria-label={t("importPreviewProblems")}>
              {imp.sample.flatMap((row) =>
                row.errors.map((e, index) => (
                  <Text key={`${row.row}-${index}`} size="sm" role="listitem">
                    {t("importPreviewProblem", {
                      row: row.row,
                      column: e.column === null ? "" : `${columnLabel(imp.header, e.column, t)}: `,
                      message: e.message,
                    })}
                  </Text>
                )),
              )}
            </Box>
          )}
        </Stack>
      </Card>

      {refusals.length > 0 && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importMappingRefused")}>
          {refusals.map((message, index) => (
            <Text key={index} size="sm">
              {message}
            </Text>
          ))}
        </Alert>
      )}

      <Group justify="flex-end">
        <Text size="sm" c="dimmed">
          {save.isPending ? t("importSaving") : gsrnMapped ? t("importCheckHint") : t("importGsrnRequired")}
        </Text>
        <Button disabled={!canCheck} loading={check.isPending} onClick={() => check.mutate()}>
          {t("importCheckFile")}
        </Button>
      </Group>
    </Stack>
  );
};
