import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Progress,
  Select,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
import {
  IconArrowDown,
  IconArrowUp,
  IconHeading,
  IconNote,
  IconPackage,
  IconPencil,
  IconTrash,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { Line } from "../api/types";
import { currentRate, vatCodesQueryOptions } from "../api/vat-codes";
import type { OrdersAccess } from "../lib/access";
import { useOrdersFormat } from "../lib/format";
import { type EditableLine, editLine, moveLine, newLine } from "../lib/lines";
import { VariantPicker } from "./variant-picker";

export interface LineGridProps {
  lines: readonly EditableLine[];
  onChange: (lines: EditableLine[]) => void;
  /** A quote's lines may be optional (O21); a template's carry no amounts; an order's carry progress (O32). */
  mode: "quote" | "order" | "template";
  currency: string;
  pricesIncludeVat?: boolean;
  /** Whether the lines take edits at all: a draft, or a confirmed order not locked (O26). */
  editable: boolean;
  /** A confirmed order: a line with progress is never removed, a line with anything invoiced keeps its price (O26). */
  confirmed?: boolean;
  access: OrdersAccess;
  /** The refused save's failing lines, by their index in the request, in words. */
  refused?: ReadonlyMap<number, string>;
}

const hasProgress = (line?: Line) => (line ? line.qtyDelivered > 0 || line.qtyInvoiced > 0 : false);

/**
 * The line grid (O15): product lines from the catalog or as free text,
 * section headings and notes, in the order they print. Quantity, unit price
 * — entered including VAT when the document's prices include VAT (O19) —,
 * discount, VAT and a quote's optional flag are edited in place; each row
 * moves up or down. The amount shown on a row is the server's from the last
 * save (O17): a row changed since shows a dash until it is saved.
 */
export const LineGrid = ({
  lines,
  onChange,
  mode,
  currency,
  pricesIncludeVat = false,
  editable,
  confirmed = false,
  access,
  refused,
}: LineGridProps) => {
  const { t, money, unitPrice, quantity, percent } = useOrdersFormat();
  const vatCodes = useQuery({ ...vatCodesQueryOptions(), enabled: editable && access.canReadVatCodes });
  const amounts = mode !== "template";
  const showProgress = mode === "order" && confirmed;
  const set = (index: number, patch: Partial<EditableLine>) =>
    onChange(lines.map((line, i) => (i === index ? editLine(line, patch) : line)));
  const remove = (index: number) => onChange(lines.filter((_, i) => i !== index));
  const add = (kind: EditableLine["kind"], catalog = false) => onChange([...lines, newLine(kind, catalog)]);
  const vatOptions = (vatCodes.data ?? []).map((code) => {
    const rate = currentRate(code);
    return { value: String(code.id), label: rate === undefined ? code.name : `${code.name} (${percent(rate)})` };
  });
  // Position, line, quantity, unit and price; then what the mode adds.
  const columns = 5 + (amounts ? 3 : 0) + (mode !== "order" ? 1 : 0) + (showProgress ? 1 : 0) + (editable ? 1 : 0);

  const productRow = (line: EditableLine, index: number) => {
    const saved = line.saved;
    const invoiced = confirmed && (saved?.qtyInvoiced ?? 0) > 0;
    const position = index + 1;
    const vatText = saved?.vatCode
      ? `${saved.vatCode}${saved.vatRatePercent == null ? "" : ` · ${percent(saved.vatRatePercent)}`}`
      : t("vatFromCategory");
    return (
      <>
        <Table.Td miw={260}>
          <Stack gap={4}>
            {line.catalog ? (
              editable && !confirmed && access.canSearchProducts ? (
                <VariantPicker
                  label={t("lineProduct", { position })}
                  value={line.variantId === null ? null : { variantId: line.variantId, sku: line.sku, name: line.name }}
                  onChange={(variant) =>
                    set(
                      index,
                      variant
                        ? {
                            variantId: variant.variantId,
                            sku: variant.sku,
                            name: variant.name,
                            unit: variant.unit,
                            // A catalog line is priced by the server unless a price is typed (O16).
                            unitPrice: "",
                            priceTouched: false,
                          }
                        : { variantId: null, sku: null, name: "" },
                    )
                  }
                />
              ) : (
                <Text size="sm" fw={600}>
                  {line.sku && (
                    <Text span size="xs" c="dimmed" ff="monospace" mr={6}>
                      {line.sku}
                    </Text>
                  )}
                  {line.name || "—"}
                </Text>
              )
            ) : editable ? (
              <TextInput
                size="xs"
                aria-label={t("lineName", { position })}
                placeholder={t("freeTextName")}
                value={line.name}
                onChange={(e) => set(index, { name: e.currentTarget.value })}
                maxLength={255}
              />
            ) : (
              <Text size="sm" fw={600}>
                {line.name || "—"}
              </Text>
            )}
            {editable ? (
              <Textarea
                size="xs"
                autosize
                minRows={1}
                maxRows={4}
                aria-label={t("lineDescription", { position })}
                placeholder={t("lineDescriptionPlaceholder")}
                value={line.description}
                disabled={invoiced}
                onChange={(e) => set(index, { description: e.currentTarget.value })}
                maxLength={2000}
              />
            ) : (
              line.description && (
                <Text size="xs" c="dimmed" style={{ whiteSpace: "pre-wrap" }}>
                  {line.description}
                </Text>
              )
            )}
            {line.optional && (
              <Badge size="xs" variant="outline" color="violet" w="fit-content">
                {t("optional")}
              </Badge>
            )}
          </Stack>
        </Table.Td>
        <Table.Td w={110}>
          {editable ? (
            <NumberInput
              size="xs"
              aria-label={t("lineQuantity", { position })}
              value={line.quantity}
              min={0}
              decimalScale={3}
              hideControls
              onChange={(value) => set(index, { quantity: value === "" ? "" : Number(value) })}
            />
          ) : (
            <Text size="sm" ta="right">
              {line.quantity === "" ? "—" : quantity(line.quantity)}
            </Text>
          )}
        </Table.Td>
        <Table.Td w={80}>
          {editable && !line.catalog && !confirmed ? (
            <TextInput
              size="xs"
              aria-label={t("lineUnit", { position })}
              value={line.unit}
              maxLength={20}
              onChange={(e) => set(index, { unit: e.currentTarget.value })}
            />
          ) : (
            <Text size="sm" c="dimmed">
              {line.unit}
            </Text>
          )}
        </Table.Td>
        <Table.Td w={130}>
          {editable ? (
            <NumberInput
              size="xs"
              aria-label={t("lineUnitPrice", { position })}
              placeholder={line.catalog ? t("catalogPrice") : undefined}
              value={line.unitPrice}
              min={0}
              decimalScale={4}
              hideControls
              disabled={invoiced}
              onChange={(value) =>
                set(index, { unitPrice: value === "" ? "" : Number(value), priceTouched: value !== "" })
              }
            />
          ) : (
            <Text size="sm" ta="right">
              {line.unitPrice === "" ? "—" : unitPrice(line.unitPrice, currency)}
            </Text>
          )}
          {saved?.priceSource === "manual" && !line.changed && (
            <Text size="10px" c="dimmed" tt="uppercase">
              {t("priceManual")}
            </Text>
          )}
          {saved && saved.unitPrice == null && line.catalog && (
            <Text size="10px" c="red" tt="uppercase">
              {t("priceMissing")}
            </Text>
          )}
        </Table.Td>
        {amounts && (
          <Table.Td w={90}>
            {editable ? (
              <NumberInput
                size="xs"
                aria-label={t("lineDiscount", { position })}
                value={line.discountPercent}
                min={0}
                max={100}
                decimalScale={2}
                suffix=" %"
                hideControls
                disabled={invoiced}
                onChange={(value) => set(index, { discountPercent: value === "" ? "" : Number(value) })}
              />
            ) : (
              <Text size="sm" ta="right">
                {line.discountPercent ? `${quantity(Number(line.discountPercent))} %` : "—"}
              </Text>
            )}
          </Table.Td>
        )}
        {amounts && (
          <Table.Td w={150}>
            {editable && !confirmed && !line.catalog && access.canReadVatCodes ? (
              <Select
                size="xs"
                aria-label={t("lineVatCode", { position })}
                placeholder={t("vatStandard")}
                data={vatOptions}
                value={line.vatCodeId === null ? null : String(line.vatCodeId)}
                onChange={(value) => set(index, { vatCodeId: value === null ? null : Number(value) })}
                clearable
              />
            ) : (
              <Text size="xs" c="dimmed">
                {!line.catalog && !saved?.vatCode ? t("vatStandard") : vatText}
              </Text>
            )}
          </Table.Td>
        )}
        {mode !== "order" && (
          <Table.Td w={70} ta="center">
            <Checkbox
              aria-label={t("lineOptional", { position })}
              checked={line.optional}
              disabled={!editable}
              onChange={(e) => set(index, { optional: e.currentTarget.checked })}
            />
          </Table.Td>
        )}
        {amounts && (
          <Table.Td w={130} ta="right">
            <Text
              size="sm"
              fw={600}
              c={line.optional ? "dimmed" : undefined}
              data-testid={`line-amount-${position}`}
              title={line.changed ? t("amountAfterSave") : undefined}
            >
              {saved && !line.changed ? money(saved.lineNet, currency) : "—"}
            </Text>
            {saved && !line.changed && saved.lineAllowance > 0 && (
              <Text size="xs" c="dimmed">
                {t("discountAmount", { amount: money(saved.lineAllowance, currency) })}
              </Text>
            )}
          </Table.Td>
        )}
        {showProgress && <Table.Td w={170}>{saved && <LineProgress line={saved} />}</Table.Td>}
      </>
    );
  };

  const textRow = (line: EditableLine, index: number) => {
    const position = index + 1;
    const span = columns - 1 - (editable ? 1 : 0);
    const section = line.kind === "section";
    return (
      <Table.Td colSpan={span}>
        {editable ? (
          section ? (
            <TextInput
              size="xs"
              aria-label={t("lineSection", { position })}
              placeholder={t("sectionPlaceholder")}
              value={line.description}
              maxLength={2000}
              fw={700}
              onChange={(e) => set(index, { description: e.currentTarget.value })}
            />
          ) : (
            <Textarea
              size="xs"
              autosize
              minRows={1}
              aria-label={t("lineNote", { position })}
              placeholder={t("notePlaceholder")}
              value={line.description}
              maxLength={2000}
              onChange={(e) => set(index, { description: e.currentTarget.value })}
            />
          )
        ) : section ? (
          <Text fw={700} size="sm" tt="uppercase" style={{ letterSpacing: "0.04em" }}>
            {line.description}
          </Text>
        ) : (
          <Text size="sm" fs="italic" c="dimmed" style={{ whiteSpace: "pre-wrap" }}>
            {line.description}
          </Text>
        )}
      </Table.Td>
    );
  };

  return (
    <Stack gap="sm">
      <Table.ScrollContainer minWidth={mode === "template" ? 640 : 960}>
        <Table verticalSpacing={6} highlightOnHover={false} data-testid="line-grid">
          <Table.Thead>
            <Table.Tr>
              <Table.Th w={editable ? 64 : 32} aria-label={t("linePosition")} />
              <Table.Th>{t("lineColumn")}</Table.Th>
              <Table.Th ta="right">{t("quantity")}</Table.Th>
              <Table.Th>{t("unit")}</Table.Th>
              <Table.Th ta="right">{pricesIncludeVat ? t("unitPriceInclVat") : t("unitPriceExclVat")}</Table.Th>
              {amounts && <Table.Th ta="right">{t("discount")}</Table.Th>}
              {amounts && <Table.Th>{t("vat")}</Table.Th>}
              {mode !== "order" && <Table.Th ta="center">{t("optional")}</Table.Th>}
              {amounts && <Table.Th ta="right">{t("lineAmount")}</Table.Th>}
              {showProgress && <Table.Th>{t("progress")}</Table.Th>}
              {editable && <Table.Th w={40} aria-label={t("remove")} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {lines.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={columns}>
                  <Text size="sm" c="dimmed" ta="center" py="md">
                    {t("noLines")}
                  </Text>
                </Table.Td>
              </Table.Tr>
            )}
            {lines.map((line, index) => {
              const problem = refused?.get(index);
              const locked = confirmed && hasProgress(line.saved);
              const rowStyle =
                line.kind === "section"
                  ? { background: "var(--mantine-color-default-hover)" }
                  : line.optional
                    ? { boxShadow: "inset 3px 0 0 var(--mantine-color-violet-4)" }
                    : undefined;
              return (
                <RowPair key={line.key} problem={problem} columns={columns}>
                  <Table.Tr style={rowStyle} data-testid="line-row" data-kind={line.kind}>
                    <Table.Td>
                      {editable ? (
                        <Group gap={2} wrap="nowrap">
                          <ActionIcon
                            size="sm"
                            variant="subtle"
                            color="gray"
                            aria-label={t("moveUp", { position: index + 1 })}
                            disabled={index === 0}
                            onClick={() => onChange(moveLine(lines, index, -1))}
                          >
                            <IconArrowUp size={14} />
                          </ActionIcon>
                          <ActionIcon
                            size="sm"
                            variant="subtle"
                            color="gray"
                            aria-label={t("moveDown", { position: index + 1 })}
                            disabled={index === lines.length - 1}
                            onClick={() => onChange(moveLine(lines, index, 1))}
                          >
                            <IconArrowDown size={14} />
                          </ActionIcon>
                        </Group>
                      ) : (
                        <Text size="xs" c="dimmed" ff="monospace">
                          {index + 1}
                        </Text>
                      )}
                    </Table.Td>
                    {line.kind === "product" ? productRow(line, index) : textRow(line, index)}
                    {editable && (
                      <Table.Td>
                        <Tooltip label={locked ? t("lineHasProgress") : t("removeLine")} withArrow>
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            aria-label={t("removeLineNumber", { position: index + 1 })}
                            disabled={locked}
                            onClick={() => remove(index)}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Tooltip>
                      </Table.Td>
                    )}
                  </Table.Tr>
                </RowPair>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {editable && (
        <Group gap="xs">
          {access.canSearchProducts && (
            <Button
              size="xs"
              variant="light"
              leftSection={<IconPackage size={14} />}
              onClick={() => add("product", true)}
            >
              {t("addProductLine")}
            </Button>
          )}
          <Button
            size="xs"
            variant="light"
            color="gray"
            leftSection={<IconPencil size={14} />}
            onClick={() => add("product")}
            disabled={lines.length >= 500}
          >
            {t("addFreeTextLine")}
          </Button>
          <Button
            size="xs"
            variant="subtle"
            color="gray"
            leftSection={<IconHeading size={14} />}
            onClick={() => add("section")}
            disabled={lines.length >= 500}
          >
            {t("addSection")}
          </Button>
          <Button
            size="xs"
            variant="subtle"
            color="gray"
            leftSection={<IconNote size={14} />}
            onClick={() => add("note")}
            disabled={lines.length >= 500}
          >
            {t("addNote")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

/** A row, and under it the refusal the server gave it, if any. */
const RowPair = ({ children, problem, columns }: { children: ReactNode; problem?: string; columns: number }) => (
  <>
    {children}
    {problem && (
      <Table.Tr>
        <Table.Td colSpan={columns} pt={0}>
          <Text size="xs" c="red" role="alert">
            {problem}
          </Text>
        </Table.Td>
      </Table.Tr>
    )}
  </>
);

/**
 * An order line's progress (O18, O32): delivered and cancelled of what was
 * ordered as one bar, and the invoiced quantity beside it. The quantities
 * are the server's buckets; the bar only draws them.
 */
export const LineProgress = ({ line }: { line: Line }) => {
  const { t, quantity } = useOrdersFormat();
  const ordered = line.quantity ?? 0;
  const share = (value: number) => (ordered > 0 ? Math.min(100, (value / ordered) * 100) : 0);
  return (
    <Box data-testid="line-progress">
      <Progress.Root size="sm" aria-hidden>
        <Progress.Section value={share(line.qtyDelivered)} color="teal" />
        <Progress.Section value={share(line.qtyCancelled)} color="gray" />
      </Progress.Root>
      <Text size="xs" c="dimmed" mt={4}>
        {t("progressDelivered", { delivered: quantity(line.qtyDelivered), ordered: quantity(ordered) })}
      </Text>
      <Text size="xs" c="dimmed">
        {t("progressInvoiced", { invoiced: quantity(line.qtyInvoiced) })}
        {line.qtyCancelled > 0 && ` · ${t("progressCancelled", { cancelled: quantity(line.qtyCancelled) })}`}
      </Text>
    </Box>
  );
};
