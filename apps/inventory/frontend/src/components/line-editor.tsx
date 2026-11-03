import { ActionIcon, Button, Group, NumberInput, SegmentedControl, Stack, Table, Text, TextInput } from "@mantine/core";
import { IconPlus, IconScan, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { type Dispatch, type SetStateAction, useRef, useState } from "react";
import { lookupQueryOptions } from "../api/lookup";
import type { InventoryAdjustmentMode } from "../api/movements";
import { QUANTITY_DECIMALS, refusalMessage, useInventoryFormat } from "../lib/format";
import { type EditorLine, type LineEditorKind, newEditorLine, roundQuantity } from "../lib/lines";
import { LocationPicker } from "./location-picker";
import { VariantPicker } from "./variant-picker";

export interface LineEditorProps {
  kind: LineEditorKind;
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
}

/**
 * The line editor the three forms share (design §7): a scan field that looks
 * a code up on Enter — what a keyboard-wedge scanner sends — and adds the
 * variant as a row or adds one to the row that has it; a row per variant with
 * its picker, its location pickers by full code and its quantity, at most
 * three decimals (I11). A refused posting's failing lines are marked on
 * their rows.
 */
export const LineEditor = ({ kind, siteId, lines, onChange, defaultLocationId, lineErrors }: LineEditorProps) => {
  const { t } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState<string>();
  const scanRef = useRef<HTMLInputElement>(null);

  const update = (key: string, change: Partial<EditorLine>) =>
    onChange((current) => current.map((line) => (line.key === key ? { ...line, ...change } : line)));

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
      onChange((current) => {
        const existing = current.find((line) => line.variant?.id === variant.id);
        return existing
          ? current.map((line) =>
              line.key === existing.key ? { ...line, quantity: roundQuantity(Number(line.quantity || 0) + 1) } : line,
            )
          : [...current, newEditorLine(kind, defaultLocationId, variant)];
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
                <Table.Th>
                  {kind === "move"
                    ? t("columnFrom")
                    : kind === "receipt"
                      ? t("columnReceiptLocation")
                      : t("columnLocation")}
                </Table.Th>
                {kind === "move" && <Table.Th>{t("columnTo")}</Table.Th>}
                <Table.Th>{t("columnQuantity")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map((line, index) => {
                const n = index + 1;
                const problem = lineErrors.get(line.key);
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
                        onChange={(variant) => update(line.key, { variant })}
                      />
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
                    <Table.Td w={150}>
                      <NumberInput
                        aria-label={t("quantityOfLine", { n })}
                        decimalScale={QUANTITY_DECIMALS}
                        // Only a change may be negative: it takes stock away.
                        allowNegative={kind === "adjustment" && line.mode === "change"}
                        min={kind === "adjustment" && line.mode === "change" ? undefined : 0}
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
                    </Table.Td>
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
