import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Progress,
  SegmentedControl,
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
  IconGridDots,
  IconHeading,
  IconNote,
  IconPackage,
  IconPencil,
  IconTrash,
  IconTruckDelivery,
} from "@tabler/icons-react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import type { Line, ShippingEstimate } from "../api/types";
import { variantSearchQueryOptions } from "../api/variants";
import { currentRate, vatCodesQueryOptions } from "../api/vat-codes";
import type { OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import {
  applyGrid,
  type EditableLine,
  editLine,
  moveLine,
  newLine,
  packagingOf,
  withCurrentPackagings,
} from "../lib/lines";
import { GridDialog } from "./grid-dialog";
import { LineProduction } from "./line-production";
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
  /**
   * A confirmed order: a line with progress — a draft invoice's part too — is
   * never removed, and a line with anything invoiced, or on a draft invoice,
   * keeps its price (O26).
   */
  confirmed?: boolean;
  access: OrdersAccess;
  /** The refused save's failing lines, by their index in the request, in words. */
  refused?: ReadonlyMap<number, string>;
  /**
   * Asks the carrier for a shipping line's price (O53); absent while the
   * document is not saved, or on a template, and then *Estimate* is not offered.
   */
  onEstimate?: () => Promise<ShippingEstimate>;
  /**
   * An order's kit lines' child lines, by the kit line's id (manufacturing
   * phase 2 M145): shown under their kit, never edited — the server writes
   * them from the kit, rescaled to its quantity.
   */
  kitChildren?: ReadonlyMap<number, readonly Line[]>;
}

const hasProgress = (line?: Line) =>
  line ? line.qtyDelivered > 0 || line.qtyInvoiced > 0 || line.qtyDraftAllocated > 0 : false;

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
  onEstimate,
  kitChildren,
}: LineGridProps) => {
  const { t, money, unitPrice, quantity, percent } = useOrdersFormat();
  const [gridOpen, setGridOpen] = useState(false);
  const [estimating, setEstimating] = useState<number | null>(null);
  const [estimates, setEstimates] = useState<ReadonlyMap<string, string>>(new Map());
  const vatCodes = useQuery({ ...vatCodesQueryOptions(), enabled: editable && access.canReadVatCodes });
  // The saved catalog lines' variants as the catalog has them now, for the
  // packagings they may be entered in (O15): found by their SKU.
  const savedSkus = editable
    ? [...new Set(lines.flatMap((line) => (line.saved && line.catalog && line.saved.sku ? [line.saved.sku] : [])))]
    : [];
  const currentVariants = useQueries({
    queries: savedSkus.map((sku) => variantSearchQueryOptions(sku, currency)),
  });
  const currentPackagings = new Map(
    currentVariants.flatMap((query) => (query.data ?? []).map((v) => [v.variantId, v.packagings] as const)),
  );
  const amounts = mode !== "template";
  const showProgress = mode === "order" && confirmed;
  const set = (index: number, patch: Partial<EditableLine>) =>
    onChange(lines.map((line, i) => (i === index ? editLine(line, patch) : line)));
  const remove = (index: number) => onChange(lines.filter((_, i) => i !== index));
  const add = (kind: EditableLine["kind"], catalog = false) =>
    onChange([...lines, newLine(kind, catalog, kind === "shipping" ? t("shippingLineName") : "")]);
  // A shipping line's estimate (O53): the carrier's price with the markup, as
  // the line enters it, and the freight product's name, variant and VAT code.
  const estimate = async (index: number) => {
    if (!onEstimate) return;
    setEstimating(index);
    try {
      const e = await onEstimate();
      const row = lines[index];
      set(index, {
        unitPrice: e.unitPrice,
        priceTouched: true,
        carrierPriced: true,
        variantId: e.variantId ?? null,
        name: e.name,
        vatCodeId: e.vatCodeId ?? row.vatCodeId,
      });
      setEstimates(
        new Map(estimates).set(
          row.key,
          t("estimated", {
            price: unitPrice(e.unitPrice, e.currency),
            service: e.serviceName,
            weight: quantity(e.weightGrams / 1000),
          }),
        ),
      );
    } catch (error) {
      setEstimates(new Map(estimates).set(lines[index].key, `${t("couldNotEstimate")}: ${refusalMessage(error, t)}`));
    } finally {
      setEstimating(null);
    }
  };
  const vatOptions = (vatCodes.data ?? []).map((code) => {
    const rate = currentRate(code);
    return { value: String(code.id), label: rate === undefined ? code.name : `${code.name} (${percent(rate)})` };
  });
  // Position, line, quantity, unit and price; then what the mode adds.
  const columns = 5 + (amounts ? 3 : 0) + (mode !== "order" ? 1 : 0) + (showProgress ? 1 : 0) + (editable ? 1 : 0);

  const productRow = (row: EditableLine, index: number) => {
    const shipping = row.kind === "shipping";
    const current = row.variantId === null ? undefined : currentPackagings.get(row.variantId);
    const line = current ? withCurrentPackagings(row, current) : row;
    const saved = line.saved;
    const invoiced = confirmed && (saved?.qtyInvoiced ?? 0) > 0;
    // A draft invoice holds part of the line: its price and discount wait (O26).
    const priceHeld = invoiced || (confirmed && (saved?.qtyDraftAllocated ?? 0) > 0);
    const pack = packagingOf(line);
    const position = index + 1;
    const vatText = saved?.vatCode
      ? `${saved.vatCode}${saved.vatRatePercent == null ? "" : ` · ${percent(saved.vatRatePercent)}`}`
      : t("vatFromCategory");
    return (
      <>
        <Table.Td miw={260}>
          <Stack gap={4}>
            {line.catalog ? (
              editable && (!confirmed || line.id === undefined) ? (
                <VariantPicker
                  label={t("lineProduct", { position })}
                  currency={currency}
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
                            packagings: variant.packagings,
                            packagingId: null,
                            packagingQuantity: "",
                          }
                        : {
                            variantId: null,
                            sku: null,
                            name: "",
                            packagings: [],
                            packagingId: null,
                            packagingQuantity: "",
                          },
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
            {shipping && (
              <Group gap={6}>
                <Badge size="xs" variant="light" color="indigo" leftSection={<IconTruckDelivery size={10} />}>
                  {t("shippingLine")}
                </Badge>
                {editable && !priceHeld && (
                  <Tooltip label={onEstimate ? t("estimateHint") : t("estimateNeedsSave")} withArrow>
                    <Button
                      size="compact-xs"
                      variant="light"
                      disabled={!onEstimate}
                      loading={estimating === index}
                      onClick={() => void estimate(index)}
                      data-assist="orders.lines.estimate-shipping"
                    >
                      {t("estimate")}
                    </Button>
                  </Tooltip>
                )}
              </Group>
            )}
            {shipping && estimates.get(row.key) && (
              <Text size="xs" c="dimmed" data-testid={`line-estimate-${position}`}>
                {estimates.get(row.key)}
              </Text>
            )}
            {mode === "order" && saved && <LineStock line={saved} />}
            {mode === "order" && saved && <LineProduction line={saved} access={access} />}
            {mode === "order" &&
              saved &&
              (editable && !priceHeld ? (
                <SegmentedControl
                  size="xs"
                  w="fit-content"
                  aria-label={t("lineInvoicePolicy", { position })}
                  data={[
                    { value: "order", label: t("invoicePolicy.order") },
                    { value: "delivery", label: t("invoicePolicy.delivery") },
                  ]}
                  value={line.invoicePolicy ?? saved.invoicePolicy}
                  onChange={(value) =>
                    set(index, { invoicePolicy: value as EditableLine["invoicePolicy"], policyTouched: true })
                  }
                />
              ) : (
                saved.invoicePolicy === "delivery" && (
                  <Badge size="xs" variant="outline" color="cyan" w="fit-content">
                    {t("invoicePolicy.delivery")}
                  </Badge>
                )
              ))}
          </Stack>
        </Table.Td>
        <Table.Td w={150}>
          {shipping ? (
            <Text size="sm" ta="right">
              {quantity(1)}
            </Text>
          ) : editable ? (
            <Stack gap={4}>
              {pack ? (
                <NumberInput
                  size="xs"
                  aria-label={t("linePackagingQuantity", { position })}
                  value={line.packagingQuantity}
                  min={0}
                  decimalScale={3}
                  hideControls
                  onChange={(value) => set(index, { packagingQuantity: value === "" ? "" : Number(value) })}
                />
              ) : (
                <NumberInput
                  size="xs"
                  aria-label={t("lineQuantity", { position })}
                  value={line.quantity}
                  min={0}
                  decimalScale={3}
                  hideControls
                  onChange={(value) => set(index, { quantity: value === "" ? "" : Number(value) })}
                />
              )}
              {line.packagings.length > 0 && (
                <Select
                  size="xs"
                  aria-label={t("linePackaging", { position })}
                  data={[
                    { value: "", label: line.unit || t("packagingUnit") },
                    ...line.packagings.map((p) => ({
                      value: String(p.id),
                      label: t("packagingOption", { name: p.name, size: quantity(p.quantity) }),
                    })),
                  ]}
                  value={line.packagingId === null ? "" : String(line.packagingId)}
                  allowDeselect={false}
                  onChange={(value) =>
                    set(
                      index,
                      value
                        ? { packagingId: Number(value), packagingQuantity: line.packagingQuantity || 1 }
                        : { packagingId: null, packagingQuantity: "" },
                    )
                  }
                />
              )}
              {pack && line.packagingQuantity !== "" && (
                <Text size="xs" c="dimmed" data-testid={`line-packaging-${position}`}>
                  {t("packagingTotal", {
                    total: quantity(Math.round(line.packagingQuantity * pack.quantity * 1000) / 1000),
                    unit: line.unit,
                  })}
                </Text>
              )}
            </Stack>
          ) : (
            <>
              <Text size="sm" ta="right">
                {line.quantity === "" ? "—" : quantity(line.quantity)}
              </Text>
              {pack && line.packagingQuantity !== "" && (
                <Text size="xs" c="dimmed" ta="right" data-testid={`line-packaging-${position}`}>
                  {t("packagingText", {
                    count: quantity(line.packagingQuantity),
                    name: pack.name,
                    size: quantity(pack.quantity),
                  })}
                </Text>
              )}
            </>
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
              disabled={priceHeld}
              onChange={(value) =>
                set(index, {
                  unitPrice: value === "" ? "" : Number(value),
                  priceTouched: value !== "",
                  carrierPriced: false,
                })
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
          {((saved?.priceSource === "carrier" && !line.changed) || line.carrierPriced) && (
            <Text size="10px" c="indigo" tt="uppercase">
              {t("priceCarrier")}
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
                disabled={priceHeld}
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
              disabled={!editable || shipping}
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
                    {line.kind === "product" || line.kind === "shipping"
                      ? productRow(line, index)
                      : textRow(line, index)}
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
                  {line.saved &&
                    kitChildren
                      ?.get(line.saved.id)
                      ?.map((child) => (
                        <KitChildRow key={child.id} child={child} columns={columns} progress={showProgress} />
                      ))}
                </RowPair>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {editable && (
        <Group gap="xs">
          <Button
            size="xs"
            variant="light"
            leftSection={<IconPackage size={14} />}
            onClick={() => add("product", true)}
          >
            {t("addProductLine")}
          </Button>
          {mode !== "template" && (
            <Button
              size="xs"
              variant="light"
              leftSection={<IconGridDots size={14} />}
              onClick={() => setGridOpen(true)}
              data-assist="orders.lines.add-from-grid"
            >
              {t("addFromGrid")}
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
          {mode !== "template" && (
            <Button
              size="xs"
              variant="light"
              color="indigo"
              leftSection={<IconTruckDelivery size={14} />}
              onClick={() => add("shipping")}
              disabled={lines.length >= 500}
              data-assist="orders.lines.add-shipping"
            >
              {t("addShipping")}
            </Button>
          )}
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
      {gridOpen && (
        <GridDialog
          currency={currency}
          onClose={() => setGridOpen(false)}
          onAdd={(picks) => {
            onChange(applyGrid(lines, picks));
            setGridOpen(false);
          }}
        />
      )}
    </Stack>
  );
};

/**
 * A kit's child line under its kit (manufacturing phase 2 M145): the
 * component, how many the kit's quantity takes and how many one kit holds,
 * what Inventory holds of it and, on a confirmed order, its progress. It
 * carries no price: the kit does.
 */
const KitChildRow = ({ child, columns, progress }: { child: Line; columns: number; progress: boolean }) => {
  const { t, quantity } = useOrdersFormat();
  return (
    <Table.Tr data-testid="kit-child-row">
      <Table.Td />
      <Table.Td colSpan={columns - 1}>
        <Group gap="sm" pl="md" wrap="nowrap" align="flex-start">
          <Text size="sm" c="dimmed">
            ↳
          </Text>
          <Stack gap={2}>
            <Text size="sm">{[child.sku, child.name].filter(Boolean).join(" · ")}</Text>
            <Text size="xs" c="dimmed">
              {t("kitChildQuantity", {
                quantity: quantity(child.quantity ?? 0),
                unit: child.unit,
                perKit: quantity(child.perKit ?? 0),
              })}
            </Text>
            <LineStock line={child} />
            {progress && <LineProgress line={child} />}
          </Stack>
        </Group>
      </Table.Td>
    </Table.Tr>
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
 * An order line's progress (O18, O32, O36): delivered and cancelled of what
 * was ordered as one bar, and the invoiced quantity beside it, with what
 * draft invoices hold. The quantities are the server's buckets; the bar only
 * draws them.
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
        {line.qtyDraftAllocated > 0 &&
          ` · ${t("progressDraftAllocated", { drafted: quantity(line.qtyDraftAllocated) })}`}
        {line.qtyCancelled > 0 && ` · ${t("progressCancelled", { cancelled: quantity(line.qtyCancelled) })}`}
      </Text>
    </Box>
  );
};

/**
 * What Inventory holds of an order line (O45, O46): what is reserved for it
 * at the order's site, and what is available of its variant there — on hand
 * less every reservation, negative after Inventory's own corrections.
 */
export const LineStock = ({ line }: { line: Line }) => {
  const { t, quantity } = useOrdersFormat();
  const reserved = line.qtyReserved ?? 0;
  if (line.deliveryMethod !== "delivery" || (reserved === 0 && line.available == null)) return null;
  return (
    <Group gap={4} data-testid="line-stock">
      {reserved > 0 && (
        <Badge size="xs" variant="light" color="teal">
          {t("reservedQuantity", { quantity: quantity(reserved) })}
        </Badge>
      )}
      {line.available != null && (
        <Badge size="xs" variant="light" color={line.available < 0 ? "red" : "gray"}>
          {t("availableQuantity", { quantity: quantity(line.available) })}
        </Badge>
      )}
    </Group>
  );
};
