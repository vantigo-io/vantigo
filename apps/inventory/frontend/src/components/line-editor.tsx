import { ActionIcon, Button, Group, NumberInput, SegmentedControl, Stack, Table, Text, TextInput } from "@mantine/core";
import { IconPlus, IconScan, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { type Dispatch, type ReactNode, type SetStateAction, useRef, useState } from "react";
import { lookupQueryOptions } from "../api/lookup";
import type { InventoryAdjustmentMode } from "../api/movements";
import type { InventoryVariantRef } from "../api/stock";
import { stockAtSitesQueryOptions } from "../api/transfers";
import { QUANTITY_DECIMALS, refusalMessage, useInventoryFormat } from "../lib/format";
import {
  type EditorKind,
  type EditorLine,
  isPacked,
  lineQuantity,
  newEditorLine,
  roundQuantity,
  usesDecrease,
} from "../lib/lines";
import { LocationPicker } from "./location-picker";
import { PackagingSteppers } from "./packaging-steppers";
import { VariantPicker } from "./variant-picker";

/** Read-only columns a form adds after the quantity, as the transfer's stock at both sites (phase 2 design §7). */
export interface LineEditorColumn {
  key: string;
  header: ReactNode;
  cell: (line: EditorLine) => ReactNode;
}

export interface LineEditorProps {
  kind: EditorKind;
  /** The site whose locations the pickers offer. */
  siteId: number | undefined;
  lines: readonly EditorLine[];
  /**
   * Takes the lines, or a change to make to the lines as they are by then: a
   * scan applies its line once its lookup answers, after any scan before it.
   */
  onChange: Dispatch<SetStateAction<EditorLine[]>>;
  /** Where a scanned or added row is put; the receipt's rows use the receipt's default instead. */
  defaultLocationId: number | null;
  /** What is wrong with a row, by its key: a refusal's failing line, or a field left empty. */
  lineErrors: ReadonlyMap<string, { message: string; detail?: string }>;
  /** Columns drawn after the quantity, read-only. */
  extraColumns?: readonly LineEditorColumn[];
}

/**
 * The line editor the three forms share (design §7): a scan field that looks
 * a code up on Enter — what a keyboard-wedge scanner sends — and adds the
 * variant as a row or adds one to the row that has it; a row per variant with
 * its picker, its location pickers by full code and its quantity, at most
 * three decimals (I11). A variant with packagings gets a stepper per
 * packaging beside its loose quantity (I25), and a scan of a packaging's
 * barcode adds one of that packaging. A refused posting's failing lines are
 * marked on their rows.
 */
export const LineEditor = ({
  kind,
  siteId,
  lines,
  onChange,
  defaultLocationId,
  lineErrors,
  extraColumns = [],
}: LineEditorProps) => {
  const { t, quantity } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState<string>();
  const scanRef = useRef<HTMLInputElement>(null);

  // Editing a line drops the low-stock list's suggestion it was drafted with (I57).
  const update = (key: string, change: Partial<EditorLine>) =>
    onChange((current) =>
      current.map((line) => (line.key === key ? { ...line, ...change, suggestion: undefined } : line)),
    );

  // A receipt line goes to the variant's primary location at the site by default (I55): a picked
  // variant carries it from the search, a scanned one is read from its stock at the site.
  const primaryOf = async (variant: InventoryVariantRef): Promise<number | null> => {
    if (kind !== "receipt" || siteId === undefined) return null;
    try {
      const [here] = await queryClient.fetchQuery(stockAtSitesQueryOptions([variant.id], [siteId]));
      return here?.placement?.primaryLocationId ?? null;
    } catch {
      return null;
    }
  };
  // Picking another variant on a receipt line moves it to that variant's primary location, unless the person chose one.
  const picked = (line: EditorLine, variant: InventoryVariantRef | undefined) => {
    const change: Partial<EditorLine> = { variant, packs: {}, decrease: false };
    if (kind === "receipt" && (line.locationId === null || line.locationId === line.variant?.primaryLocationId))
      change.locationId = variant?.primaryLocationId ?? null;
    update(line.key, change);
  };

  // The field empties as Enter is pressed, so a scanner's next code starts
  // afresh while this one is looked up; each lands on the lines as they are
  // when its answer comes, so a fast second scan is neither lost nor merged.
  const scanned = async (code: string) => {
    if (!code) return;
    setScan("");
    setScanError(undefined);
    // What found nothing goes back in the field to be fixed, unless the next scan is already there.
    const keep = () => setScan((current) => current || code);
    try {
      const matches = await queryClient.fetchQuery(lookupQueryOptions(code));
      const variant = matches.find((match) => match.kind === "variant")?.variant;
      if (!variant) {
        keep();
        setScanError(t("scanNoVariant", { code }));
        return;
      }
      // The lookup answers the variant a packaging's barcode belongs to; its packagings say which one it was.
      const packaging = variant.packagings?.find((one) => one.barcode === code);
      const primary = await primaryOf(variant);
      onChange((current) => {
        const existing = current.find((line) => line.variant?.id === variant.id);
        if (!existing) {
          const line = newEditorLine(kind, defaultLocationId, variant, packaging?.id);
          return [
            ...current,
            primary === null
              ? line
              : { ...line, variant: { ...variant, primaryLocationId: primary }, locationId: primary },
          ];
        }
        return current.map((line) =>
          line.key !== existing.key
            ? line
            : packaging
              ? {
                  ...line,
                  packs: { ...line.packs, [packaging.id]: (line.packs[packaging.id] ?? 0) + 1 },
                  suggestion: undefined,
                }
              : { ...line, quantity: roundQuantity(Number(line.quantity || 0) + 1), suggestion: undefined },
        );
      });
    } catch (error) {
      keep();
      setScanError(refusalMessage(t, error as Error));
    } finally {
      scanRef.current?.focus();
    }
  };

  return (
    <Stack gap="sm">
      <TextInput
        ref={scanRef}
        label={t("scanLabel")}
        description={t("scanHint")}
        placeholder={t("scanPlaceholder")}
        leftSection={<IconScan size={16} />}
        value={scan}
        error={scanError}
        onChange={(event) => {
          setScan(event.currentTarget.value);
          setScanError(undefined);
        }}
        onKeyDown={(event) => {
          if (event.key !== "Enter") return;
          event.preventDefault();
          void scanned(event.currentTarget.value.trim());
        }}
      />
      {lines.length > 0 && (
        <Table.ScrollContainer minWidth={640}>
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("columnItem")}</Table.Th>
                {kind === "adjustment" && <Table.Th>{t("columnMode")}</Table.Th>}
                {kind !== "transfer" && (
                  <Table.Th>
                    {kind === "move"
                      ? t("columnFrom")
                      : kind === "receipt"
                        ? t("columnReceiptLocation")
                        : t("columnLocation")}
                  </Table.Th>
                )}
                {kind === "move" && <Table.Th>{t("columnTo")}</Table.Th>}
                <Table.Th>{t("columnQuantity")}</Table.Th>
                {extraColumns.map((column) => (
                  <Table.Th key={column.key}>{column.header}</Table.Th>
                ))}
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map((line, index) => {
                const n = index + 1;
                const problem = lineErrors.get(line.key);
                const packagings = line.variant?.packagings ?? [];
                const decreasing = usesDecrease(kind, line);
                const total = lineQuantity(kind, line);
                return (
                  <Table.Tr
                    key={line.key}
                    data-refused={problem ? "true" : undefined}
                    bg={problem ? "var(--mantine-color-red-light)" : undefined}
                  >
                    <Table.Td miw={220}>
                      <VariantPicker
                        compact
                        label={t("itemOfLine", { n })}
                        value={line.variant}
                        siteId={kind === "receipt" ? siteId : undefined}
                        onChange={(variant) => picked(line, variant)}
                      />
                      {line.variant?.deleted && (
                        <Text size="xs" c="red" mt={4}>
                          {t("variantDeleted")}
                        </Text>
                      )}
                      {problem && (
                        <Stack gap={0} mt={4}>
                          <Text size="xs" c="red" fw={500}>
                            {problem.message}
                          </Text>
                          {problem.detail && (
                            <Text size="xs" c="dimmed">
                              {problem.detail}
                            </Text>
                          )}
                        </Stack>
                      )}
                    </Table.Td>
                    {kind === "adjustment" && (
                      <Table.Td>
                        <SegmentedControl
                          size="xs"
                          aria-label={t("modeOfLine", { n })}
                          value={line.mode}
                          onChange={(mode) => update(line.key, { mode: mode as InventoryAdjustmentMode })}
                          data={[
                            { value: "change", label: t("mode_change") },
                            { value: "set", label: t("mode_set") },
                          ]}
                        />
                      </Table.Td>
                    )}
                    {kind !== "transfer" && (
                      <Table.Td miw={180}>
                        <LocationPicker
                          compact
                          siteId={siteId}
                          label={kind === "move" ? t("fromOfLine", { n }) : t("locationOfLine", { n })}
                          placeholder={kind === "receipt" ? t("receiptLineLocationPlaceholder") : undefined}
                          required={kind !== "receipt"}
                          value={line.locationId}
                          onChange={(locationId) => update(line.key, { locationId })}
                        />
                      </Table.Td>
                    )}
                    {kind === "move" && (
                      <Table.Td miw={180}>
                        <LocationPicker
                          compact
                          required
                          siteId={siteId}
                          label={t("toOfLine", { n })}
                          value={line.toLocationId}
                          onChange={(toLocationId) => update(line.key, { toLocationId })}
                        />
                      </Table.Td>
                    )}
                    <Table.Td w={packagings.length > 0 ? 200 : 150}>
                      <Stack gap={4}>
                        {decreasing && (
                          <SegmentedControl
                            size="xs"
                            aria-label={t("directionOfLine", { n })}
                            value={line.decrease ? "remove" : "add"}
                            onChange={(value) => update(line.key, { decrease: value === "remove" })}
                            data={[
                              { value: "add", label: t("direction_add") },
                              { value: "remove", label: t("direction_remove") },
                            ]}
                          />
                        )}
                        {line.variant && packagings.length > 0 && (
                          <PackagingSteppers
                            packagings={packagings}
                            unit={line.variant.unit}
                            counts={line.packs}
                            onChange={(packs) => update(line.key, { packs })}
                            label={(packaging) => t("packagingOfLine", { name: packaging.name, n })}
                          />
                        )}
                        <NumberInput
                          size={packagings.length > 0 ? "xs" : undefined}
                          aria-label={t(packagings.length > 0 ? "looseOfLine" : "quantityOfLine", { n })}
                          decimalScale={QUANTITY_DECIMALS}
                          // Only a change may be negative: it takes stock away — unless the
                          // variant has packagings, whose entries say the direction apart.
                          allowNegative={kind === "adjustment" && line.mode === "change" && !decreasing}
                          min={kind === "adjustment" && line.mode === "change" && !decreasing ? undefined : 0}
                          rightSection={
                            line.variant ? (
                              <Text size="xs" c="dimmed" pr="xs">
                                {line.variant.unit}
                              </Text>
                            ) : undefined
                          }
                          rightSectionWidth={line.variant ? 48 : undefined}
                          value={line.quantity}
                          onChange={(value) =>
                            update(line.key, { quantity: value === "" ? "" : roundQuantity(Number(value)) })
                          }
                        />
                        {line.variant && isPacked(line) && total !== "" && (
                          <Text size="xs" c="dimmed" aria-label={t("totalOfLine", { n })}>
                            = {quantity(total, line.variant.unit)}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    {extraColumns.map((column) => (
                      <Table.Td key={column.key}>{column.cell(line)}</Table.Td>
                    ))}
                    <Table.Td w={40}>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("removeLine", { n })}
                        onClick={() => onChange((current) => current.filter((one) => one.key !== line.key))}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      <Group>
        <Button
          variant="light"
          size="xs"
          leftSection={<IconPlus size={14} />}
          onClick={() => onChange((current) => [...current, newEditorLine(kind, defaultLocationId)])}
        >
          {t("addLine")}
        </Button>
      </Group>
    </Stack>
  );
};
