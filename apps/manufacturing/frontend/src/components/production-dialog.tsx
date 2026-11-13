import {
  ActionIcon,
  Alert,
  Button,
  Checkbox,
  Group,
  List,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconPlus, IconX } from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  type ManufacturingLotChoice,
  type ManufacturingOrder,
  type ManufacturingProduction,
  type ManufacturingProductionDraftLine,
  type ManufacturingProductionInput,
  postProduction,
  productionDraftQueryOptions,
} from "../api/orders";
import { type Deviation, refusalCode, refusedDeviations } from "../api/request";
import { addDecimal, compareDecimal, isDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { type LotRowValues, lotRowProblems, lotRowsBody, proposeAt, sumLotRows } from "../lib/lots";
import { LocationSelect } from "./location-select";

const PREFILL_DEBOUNCE_MS = 300;

/** What one component line of the dialog holds, as typed. */
interface LineValues {
  consumed: string;
  scrap: string;
  fromLocationId: number | null;
}

/** The output lot's fields as typed; undefined is untouched, the server's default. */
interface OutputValues {
  number?: string;
  productionDate?: string;
  expiry?: string;
  bestBefore?: string;
  serialFirst?: string;
}

/** A field's text when it is a decimal of 0 or more with at most three decimals, else undefined. */
const decimalOrUndefined = (text: string) =>
  text.trim() !== "" && isDecimal(text) ? normalizeDecimal(text) : undefined;

/** The order fields the dialog reads: its id, number and site. */
export type ProductionOrder = Pick<ManufacturingOrder, "id" | "number" | "siteId">;

export interface ProductionDialogProps {
  order: ProductionOrder;
  opened: boolean;
  onClose: () => void;
  /**
   * Given, the dialog offers to finish the operation too: checked, it sends
   * the production this way instead of `POST /orders/{id}/productions` — the
   * shop floor finishes the last work order with it, in one transaction
   * (M29), which stops every operator's time on it. Answers the production
   * posted, or null when none is answered. Unchecked, the default, the
   * production is posted alone and the work order runs on.
   */
  finish?: (body: ManufacturingProductionInput) => Promise<ManufacturingProduction | null>;
}

/**
 * Register production (M22, M22a): the good quantity and the output scrap,
 * each component line's consumption prefilled in proportion — read again
 * from `production-draft` whenever either changes — and editable with its
 * scrap and the location it is taken from, and where the output goes.
 *
 * A tracked component (manufacturing phase 2 M82, M83) shows the lots the
 * draft proposes, first-expiry-first, which Inventory takes under its own
 * locks when the line is sent without lots; *Change lot* edits them from the
 * site's lots the draft lists, and they are then sent as the person chose
 * them, consumed first, then scrapped. A tracked product shows its lot number
 * from the order's pattern, its production date and dates, or the first
 * serial number of a serial product.
 *
 * One `clientRequestId` per opening makes the post idempotent: a retry and
 * the consumption warning's *Post anyway* resend the same body under it, the
 * latter with `acceptDeviation`. `finish` offers to send it with the work
 * order's finish instead.
 */
export const ProductionDialog = ({ order, opened, onClose, finish }: ProductionDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("registerProductionTitle", { number: order.number })} size="xl">
      {opened && <ProductionForm order={order} onClose={onClose} finish={finish} />}
    </Modal>
  );
};

const ProductionForm = ({ order, onClose, finish }: Omit<ProductionDialogProps, "opened">) => {
  const { t, quantity: showQuantity } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [clientRequestId] = useState(() => crypto.randomUUID());
  // Untouched, the quantity and the output location are the prefill's; a line's figures are the prefill's until typed.
  const [quantity, setQuantity] = useState<string>();
  const [outputScrap, setOutputScrap] = useState("0");
  const [edits, setEdits] = useState<Record<number, Partial<LineValues>>>({});
  const [toLocationId, setToLocationId] = useState<number | null>();
  const [deviations, setDeviations] = useState<readonly Deviation[]>([]);
  const [problem, setProblem] = useState<string>();
  const [posting, setPosting] = useState(false);
  const [sentBody, setSentBody] = useState<ManufacturingProductionInput>();
  const [finishing, setFinishing] = useState(false);
  // A tracked component's lots as the person chose them; absent, the draft's proposal, taken by Inventory (AutoLot).
  const [lotEdits, setLotEdits] = useState<Record<number, LotRowValues[]>>({});
  const [output, setOutput] = useState<OutputValues>({});
  // The by-products as typed (manufacturing phase 2 M121); untouched, the draft's proportional proposal.
  const [byEdits, setByEdits] = useState<Record<number, { quantity?: string; number?: string }>>({});

  // The prefill follows the quantities once they are decimals; the first read takes the server's defaults.
  const wantedQuantity = quantity === undefined ? undefined : decimalOrUndefined(quantity);
  const wantedScrap = decimalOrUndefined(outputScrap || "0");
  const [askedQuantity] = useDebouncedValue(wantedQuantity, PREFILL_DEBOUNCE_MS);
  const [askedScrap] = useDebouncedValue(wantedScrap, PREFILL_DEBOUNCE_MS);
  const draft = useQuery({
    ...productionDraftQueryOptions(order.id, askedQuantity, askedScrap === "0" ? undefined : askedScrap),
    placeholderData: keepPreviousData,
  });
  // Posting waits for the prefill of the quantities shown.
  const settled =
    draft.data !== undefined &&
    !draft.isPlaceholderData &&
    askedQuantity === wantedQuantity &&
    askedScrap === wantedScrap;
  const shownQuantity = quantity ?? draft.data?.quantity ?? "";
  const shownToLocationId = toLocationId === undefined ? (draft.data?.toLocationId ?? null) : toLocationId;

  const valuesOf = (componentId: number, proposed: string): LineValues => ({
    consumed: edits[componentId]?.consumed ?? proposed,
    scrap: edits[componentId]?.scrap ?? "",
    fromLocationId: edits[componentId]?.fromLocationId ?? null,
  });
  const lines = (draft.data?.lines ?? []).map((line) => ({ line, values: valuesOf(line.componentId, line.proposed) }));

  // A change after the consumption warning is a new body, judged again.
  const setLine = (componentId: number, patch: Partial<LineValues>) => {
    setDeviations([]);
    setEdits((current) => ({ ...current, [componentId]: { ...current[componentId], ...patch } }));
  };
  // A new quantity is a new prefill: the consumed figures typed give way to it; the scrap and the locations stay.
  const setQuantities = (patch: { quantity?: string; outputScrap?: string }) => {
    setDeviations([]);
    if (patch.quantity !== undefined) setQuantity(patch.quantity);
    if (patch.outputScrap !== undefined) setOutputScrap(patch.outputScrap);
    setEdits((current) =>
      Object.fromEntries(Object.entries(current).map(([id, edit]) => [id, { ...edit, consumed: undefined }])),
    );
    setByEdits((current) =>
      Object.fromEntries(Object.entries(current).map(([id, edit]) => [id, { ...edit, quantity: undefined }])),
    );
  };
  const byProducts = (draft.data?.byProducts ?? []).map((line) => ({
    line,
    quantity: byEdits[line.outputId]?.quantity ?? line.proposed,
    number: byEdits[line.outputId]?.number,
  }));
  const setByproduct = (outputId: number, patch: { quantity?: string; number?: string }) =>
    setByEdits((current) => ({ ...current, [outputId]: { ...current[outputId], ...patch } }));

  const setLots = (componentId: number, rows: LotRowValues[] | undefined) => {
    setDeviations([]);
    setLotEdits((current) => {
      const next = { ...current };
      if (rows === undefined) delete next[componentId];
      else next[componentId] = rows;
      return next;
    });
  };
  // What a line takes off the shelf: its consumed and its scrap together.
  const totalOf = (values: LineValues) =>
    addDecimal(decimalOrUndefined(values.consumed) ?? "0", decimalOrUndefined(values.scrap) ?? "0");
  const lotsMismatch = (componentId: number, values: LineValues) => {
    const rows = lotEdits[componentId];
    if (rows === undefined) return false;
    const sum = sumLotRows(rows, isDecimal);
    return (
      sum === undefined ||
      compareDecimal(sum, totalOf(values)) !== 0 ||
      rows.some((row) => row.lotId === null || row.locationId === null) ||
      lotRowProblems(rows, isDecimal).some((problem) => problem.twice || problem.zero)
    );
  };

  const invalid =
    decimalOrUndefined(shownQuantity) === undefined ||
    wantedScrap === undefined ||
    lines.some(
      ({ line, values }) =>
        (values.consumed.trim() !== "" && !isDecimal(values.consumed)) ||
        (values.scrap.trim() !== "" && !isDecimal(values.scrap)) ||
        lotsMismatch(line.componentId, values),
    ) ||
    byProducts.some((entry) => decimalOrUndefined(entry.quantity) === undefined);

  const draftOutput = draft.data?.output;
  const outputBody = (): Pick<ManufacturingProductionInput, "outputLot" | "outputSerials"> => {
    if (!draftOutput || draftOutput.tracking === "none") return {};
    const dates = {
      ...(output.productionDate ? { productionDate: output.productionDate } : {}),
      ...(output.expiry ? { expiry: output.expiry } : {}),
      ...(output.bestBefore ? { bestBefore: output.bestBefore } : {}),
    };
    if (draftOutput.tracking === "serial") {
      const first = output.serialFirst?.trim();
      return {
        ...(Object.keys(dates).length > 0 ? { outputLot: dates } : {}),
        ...(first ? { outputSerials: { first } } : {}),
      };
    }
    const number = output.number?.trim();
    const lot = { ...dates, ...(number ? { number } : {}) };
    return Object.keys(lot).length > 0 ? { outputLot: lot } : {};
  };

  const bodyOf = (): ManufacturingProductionInput => ({
    clientRequestId,
    quantity: normalizeDecimal(shownQuantity || "0"),
    outputScrap: normalizeDecimal(outputScrap || "0"),
    toLocationId: shownToLocationId,
    ...outputBody(),
    ...(byProducts.length > 0
      ? {
          outputs: byProducts.map((entry) => {
            const number = entry.number?.trim();
            return {
              outputId: entry.line.outputId,
              quantity: normalizeDecimal(entry.quantity || "0"),
              ...(number ? { lot: { number } } : {}),
            };
          }),
        }
      : {}),
    lines: lines.flatMap(({ line, values }) => {
      const consumed = decimalOrUndefined(values.consumed);
      const scrap = decimalOrUndefined(values.scrap);
      // A line with neither is omitted: not judged and consuming nothing (M22a).
      if (consumed === undefined && scrap === undefined) return [];
      const rows = lotEdits[line.componentId];
      return [
        {
          componentId: line.componentId,
          ...(consumed !== undefined ? { consumed } : {}),
          ...(scrap !== undefined ? { scrap } : {}),
          ...(values.fromLocationId !== null ? { fromLocationId: values.fromLocationId } : {}),
          // Chosen lots go as chosen; otherwise Inventory takes them first-expiry-first under its locks.
          ...(rows !== undefined ? { lots: lotRowsBody(rows, consumed ?? "0") } : {}),
        },
      ];
    }),
  });

  const post = async (body: ManufacturingProductionInput) => {
    setSentBody(body);
    setPosting(true);
    setProblem(undefined);
    try {
      const production = await (finish && finishing ? finish(body) : postProduction(order.id, body));
      notifications.show({
        color: "teal",
        title: t("productionPosted"),
        message: t("productionPostedBody", {
          quantity: production?.quantity ?? body.quantity,
          number: production?.orderNumber ?? order.number,
        }),
      });
      await invalidate();
      onClose();
    } catch (error) {
      if (refusalCode(error) === "consumption_differs") {
        setDeviations(refusedDeviations(error));
        return;
      }
      setDeviations([]);
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setPosting(false);
    }
  };

  const lineOf = new Map((draft.data?.lines ?? []).map((line) => [line.componentId, line]));

  return (
    <Stack gap="md">
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        <TextInput
          label={t("producedQuantity")}
          description={t("producedQuantityHint")}
          inputMode="decimal"
          value={shownQuantity}
          onChange={(event) => setQuantities({ quantity: event.currentTarget.value })}
          error={
            quantity !== undefined && decimalOrUndefined(quantity) === undefined ? t("problem_notNegative") : undefined
          }
        />
        <TextInput
          label={t("outputScrap")}
          description={t("outputScrapHint")}
          inputMode="decimal"
          value={outputScrap}
          onChange={(event) => setQuantities({ outputScrap: event.currentTarget.value })}
          error={wantedScrap === undefined ? t("problem_notNegative") : undefined}
        />
        <LocationSelect
          label={t("toLocation")}
          description={t("toLocationHint")}
          siteId={order.siteId}
          value={shownToLocationId}
          onChange={setToLocationId}
        />
      </SimpleGrid>
      {draftOutput && draftOutput.tracking !== "none" && (
        <SimpleGrid cols={{ base: 1, sm: 4 }}>
          {draftOutput.tracking === "lot" ? (
            <TextInput
              label={t("outputLotNumber")}
              description={t("outputLotNumberHint", { pattern: draftOutput.lotPattern })}
              value={output.number ?? draftOutput.lotNumber ?? ""}
              maxLength={20}
              onChange={(event) => {
                const number = event.currentTarget.value;
                setOutput((current) => ({ ...current, number }));
              }}
            />
          ) : (
            <TextInput
              label={t("firstSerial")}
              description={t("firstSerialHint")}
              value={output.serialFirst ?? draftOutput.serialFirst ?? ""}
              maxLength={20}
              onChange={(event) => {
                const serialFirst = event.currentTarget.value;
                setOutput((current) => ({ ...current, serialFirst }));
              }}
            />
          )}
          <TextInput
            type="date"
            label={t("productionDate")}
            value={output.productionDate ?? draftOutput.productionDate}
            onChange={(event) => {
              const productionDate = event.currentTarget.value;
              setOutput((current) => ({ ...current, productionDate }));
            }}
          />
          <TextInput
            type="date"
            label={t("lotExpiry")}
            description={t("lotDatesHint")}
            value={output.expiry ?? ""}
            placeholder={draftOutput.expiry ?? undefined}
            onChange={(event) => {
              const expiry = event.currentTarget.value;
              setOutput((current) => ({ ...current, expiry }));
            }}
          />
          <TextInput
            type="date"
            label={t("lotBestBefore")}
            value={output.bestBefore ?? ""}
            placeholder={draftOutput.bestBefore ?? undefined}
            onChange={(event) => {
              const bestBefore = event.currentTarget.value;
              setOutput((current) => ({ ...current, bestBefore }));
            }}
          />
        </SimpleGrid>
      )}
      {byProducts.length > 0 && (
        <Stack gap={4} data-assist="manufacturing.production.byproducts">
          <Text size="sm" fw={500}>
            {t("byProducts")}
          </Text>
          <Table.ScrollContainer minWidth={560}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("byProduct")}</Table.Th>
                  <Table.Th>{t("expected")}</Table.Th>
                  <Table.Th>{t("producedQuantity")}</Table.Th>
                  <Table.Th>{t("outputLotNumber")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {byProducts.map(({ line, quantity: typed, number }) => (
                  <Table.Tr key={line.outputId}>
                    <Table.Td>
                      <Text size="sm">{line.variant.sku}</Text>
                      <Text size="xs" c="dimmed">
                        {line.variant.productName}
                      </Text>
                    </Table.Td>
                    <Table.Td>{showQuantity(line.expected, line.variant.unit)}</Table.Td>
                    <Table.Td>
                      <TextInput
                        aria-label={t("byProductQuantityOf", { sku: line.variant.sku })}
                        inputMode="decimal"
                        value={typed}
                        onChange={(event) => setByproduct(line.outputId, { quantity: event.currentTarget.value })}
                        error={decimalOrUndefined(typed) === undefined}
                      />
                    </Table.Td>
                    <Table.Td>
                      {line.tracking === "lot" ? (
                        <TextInput
                          aria-label={t("byProductLotOf", { sku: line.variant.sku })}
                          maxLength={20}
                          value={number ?? line.lotNumber ?? ""}
                          onChange={(event) => setByproduct(line.outputId, { number: event.currentTarget.value })}
                        />
                      ) : (
                        "—"
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
      )}
      {draft.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {refusalMessage(t, draft.error)}
        </Alert>
      ) : (
        <Table.ScrollContainer minWidth={760}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("component")}</Table.Th>
                <Table.Th>{t("remainingColumn")}</Table.Th>
                <Table.Th>{t("consumedColumn")}</Table.Th>
                <Table.Th>{t("scrapColumn")}</Table.Th>
                <Table.Th>{t("fromLocation")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map(({ line, values }) => {
                return [
                  <Table.Tr key={line.componentId}>
                    <Table.Td>
                      <Text size="sm">{line.variant.sku}</Text>
                      <Text size="xs" c="dimmed">
                        {line.variant.productName}
                      </Text>
                    </Table.Td>
                    <Table.Td>{showQuantity(line.remaining, line.variant.unit)}</Table.Td>
                    <Table.Td>
                      <TextInput
                        aria-label={t("consumedOf", { sku: line.variant.sku })}
                        inputMode="decimal"
                        value={values.consumed}
                        onChange={(event) => setLine(line.componentId, { consumed: event.currentTarget.value })}
                        error={values.consumed.trim() !== "" && !isDecimal(values.consumed)}
                      />
                    </Table.Td>
                    <Table.Td>
                      <TextInput
                        aria-label={t("scrapOf", { sku: line.variant.sku })}
                        inputMode="decimal"
                        placeholder={t("noScrap")}
                        value={values.scrap}
                        onChange={(event) => setLine(line.componentId, { scrap: event.currentTarget.value })}
                        error={values.scrap.trim() !== "" && !isDecimal(values.scrap)}
                      />
                    </Table.Td>
                    <Table.Td>
                      <LocationSelect
                        compact
                        label={t("fromLocationOf", { sku: line.variant.sku })}
                        placeholder={t("fromLocationDefault")}
                        clearable
                        siteId={order.siteId}
                        value={values.fromLocationId}
                        onChange={(next) => setLine(line.componentId, { fromLocationId: next })}
                      />
                    </Table.Td>
                  </Table.Tr>,
                  line.tracking !== "none" && (
                    <Table.Tr key={`${line.componentId}-lots`}>
                      <Table.Td colSpan={5}>
                        <LineLots
                          line={line}
                          rows={lotEdits[line.componentId]}
                          total={totalOf(values)}
                          fromLocationId={values.fromLocationId}
                          onChange={(rows) => setLots(line.componentId, rows)}
                        />
                      </Table.Td>
                    </Table.Tr>
                  ),
                ];
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {deviations.length > 0 && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("consumptionDiffers")}>
          <Stack gap="xs">
            <Text size="sm">{t("consumptionDiffersHint")}</Text>
            <List size="sm">
              {deviations.map((deviation) => {
                const unit = lineOf.get(deviation.componentId)?.variant.unit;
                return (
                  <List.Item key={deviation.componentId}>
                    {t("deviationLine", {
                      sku: deviation.sku,
                      consumed: showQuantity(deviation.consumed, unit),
                      expected: showQuantity(deviation.expected, unit),
                    })}
                  </List.Item>
                );
              })}
            </List>
          </Stack>
        </Alert>
      )}
      {finish && (
        <Checkbox
          size="md"
          label={t("alsoFinishOperation")}
          description={t("alsoFinishOperationHint")}
          checked={finishing}
          onChange={(event) => {
            setDeviations([]);
            setFinishing(event.currentTarget.checked);
          }}
        />
      )}
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        {deviations.length > 0 && sentBody ? (
          <Button color="yellow" loading={posting} onClick={() => void post({ ...sentBody, acceptDeviation: true })}>
            {t("postAnyway")}
          </Button>
        ) : (
          <Button loading={posting} disabled={invalid || !settled} onClick={() => void post(bodyOf())}>
            {t("postProduction")}
          </Button>
        )}
      </Group>
    </Stack>
  );
};

/** A lot as Change lot offers it: its number, location, what lies there, its expiry and whether it is held or expired. */
const useLotLabel = () => {
  const { t, decimal, day } = useManufacturingFormat();
  return (choice: ManufacturingLotChoice) => {
    const base = choice.expiry
      ? t("lotChoiceExpiry", {
          number: choice.lotNumber,
          location: choice.fullCode,
          quantity: decimal(choice.quantity),
          expiry: day(choice.expiry),
        })
      : t("lotChoice", { number: choice.lotNumber, location: choice.fullCode, quantity: decimal(choice.quantity) });
    const marks = [
      ...(choice.status !== "available" ? [t(`lotStatus_${choice.status}`)] : []),
      ...(choice.expired ? [t("lotExpired")] : []),
    ];
    return marks.length > 0 ? `${base} (${marks.join(", ")})` : base;
  };
};

interface LineLotsProps {
  line: ManufacturingProductionDraftLine;
  /** The lots chosen; undefined, the draft's proposal. */
  rows: LotRowValues[] | undefined;
  /** What the line consumes and scraps together. */
  total: string;
  /** Where the line is taken from, when the person chose; null, the order's component location. */
  fromLocationId: number | null;
  onChange: (rows: LotRowValues[] | undefined) => void;
}

/**
 * A tracked component's lots (M82, M83): the draft's proposal with Change
 * lot, or the lots chosen, each from the site's lots the draft lists. With a
 * location chosen for the line, the proposal is made again there, as
 * Inventory will take them (AutoLot at that location), not the draft's at the
 * order's component location.
 */
const LineLots = ({ line, rows, total, fromLocationId, onChange }: LineLotsProps) => {
  const { t, decimal, day } = useManufacturingFormat();
  const lotLabel = useLotLabel();
  const sku = line.variant.sku;
  const keyOf = (lotId: number, locationId: number) => `${lotId}:${locationId}`;
  const choiceOf = new Map(line.lotChoices.map((choice) => [keyOf(choice.lotId, choice.locationId), choice]));
  const proposed =
    fromLocationId === null || compareDecimal(total, "0") <= 0
      ? { lots: line.lots, short: line.short }
      : proposeAt(line.lotChoices, total, fromLocationId);
  if (rows === undefined) {
    const proposal = proposed.lots
      .map((lot) =>
        lot.expiry
          ? `${lot.lotNumber} (${day(lot.expiry)}) ${decimal(lot.quantity)}`
          : `${lot.lotNumber} ${decimal(lot.quantity)}`,
      )
      .join(" · ");
    return (
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Stack gap={2}>
          <Text size="xs" fw={500}>
            {t("lotsOf", { sku })}
          </Text>
          <Text size="xs" c="dimmed">
            {proposed.lots.length > 0 ? t("lotsProposed", { lots: proposal }) : t("lotsNone")}
          </Text>
          {compareDecimal(proposed.short, "0") > 0 && (
            <Text size="xs" c="orange">
              {t("lotsShortBy", { short: decimal(proposed.short) })}
            </Text>
          )}
        </Stack>
        <Button
          size="compact-xs"
          variant="light"
          disabled={line.lotChoices.length === 0}
          onClick={() =>
            onChange(
              proposed.lots.length > 0
                ? proposed.lots.map((lot) => ({
                    lotId: lot.lotId,
                    locationId: lot.locationId,
                    quantity: lot.quantity,
                    allowExpired: false,
                  }))
                : [{ lotId: null, locationId: null, quantity: total, allowExpired: false }],
            )
          }
        >
          {t("changeLots")}
        </Button>
      </Group>
    );
  }
  const sum = sumLotRows(rows, isDecimal);
  const problems = lotRowProblems(rows, isDecimal);
  const setRow = (index: number, patch: Partial<LotRowValues>) =>
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <Stack gap={4}>
      <Group justify="space-between">
        <Text size="xs" fw={500}>
          {t("lotsOf", { sku })}
        </Text>
        <Button size="compact-xs" variant="subtle" onClick={() => onChange(undefined)}>
          {t("useProposedLots")}
        </Button>
      </Group>
      {rows.map((row, index) => {
        const chosen =
          row.lotId !== null && row.locationId !== null ? choiceOf.get(keyOf(row.lotId, row.locationId)) : undefined;
        return (
          <Group key={index} gap="xs" wrap="nowrap" align="center">
            <Select
              size="xs"
              style={{ flex: 1 }}
              aria-label={t("lotOf", { index: index + 1, sku })}
              data={line.lotChoices.map((choice) => ({
                value: keyOf(choice.lotId, choice.locationId),
                label: lotLabel(choice),
              }))}
              value={row.lotId !== null && row.locationId !== null ? keyOf(row.lotId, row.locationId) : null}
              error={problems[index]?.twice ? t("lotChosenTwice") : undefined}
              onChange={(value) => {
                const choice = value ? choiceOf.get(value) : undefined;
                setRow(index, {
                  lotId: choice?.lotId ?? null,
                  locationId: choice?.locationId ?? null,
                  allowExpired: false,
                });
              }}
            />
            <TextInput
              size="xs"
              w={100}
              inputMode="decimal"
              aria-label={t("lotQuantityOf", { index: index + 1, sku })}
              value={row.quantity}
              onChange={(event) => setRow(index, { quantity: event.currentTarget.value })}
              error={problems[index]?.zero ? t("lotRowZero") : row.quantity.trim() !== "" && !isDecimal(row.quantity)}
            />
            {chosen?.expired && (
              <Checkbox
                size="xs"
                label={t("lotExpired")}
                aria-label={t("lotExpiredOf", { index: index + 1, sku })}
                checked={row.allowExpired}
                onChange={(event) => setRow(index, { allowExpired: event.currentTarget.checked })}
              />
            )}
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t("removeLot")}
              onClick={() => onChange(rows.filter((_, i) => i !== index))}
            >
              <IconX size={14} />
            </ActionIcon>
          </Group>
        );
      })}
      <Group justify="space-between">
        <Button
          size="compact-xs"
          variant="subtle"
          leftSection={<IconPlus size={12} />}
          onClick={() => onChange([...rows, { lotId: null, locationId: null, quantity: "", allowExpired: false }])}
        >
          {t("addLot")}
        </Button>
        {(sum === undefined || compareDecimal(sum, total) !== 0) && (
          <Text size="xs" c="red">
            {t("lotsAddUp", { sum: decimal(sum ?? "0"), total: decimal(total) })}
          </Text>
        )}
      </Group>
    </Stack>
  );
};
