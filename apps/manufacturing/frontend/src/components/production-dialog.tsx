import { Alert, Button, Group, List, Modal, SimpleGrid, Stack, Table, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle } from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  type ManufacturingOrder,
  type ManufacturingProductionInput,
  postProduction,
  productionDraftQueryOptions,
} from "../api/orders";
import { type Deviation, refusalCode, refusedDeviations } from "../api/request";
import { isDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { LocationSelect } from "./location-select";

const PREFILL_DEBOUNCE_MS = 300;

/** What one component line of the dialog holds, as typed. */
interface LineValues {
  consumed: string;
  scrap: string;
  fromLocationId: number | null;
}

/** A field's text when it is a decimal of 0 or more with at most three decimals, else undefined. */
const decimalOrUndefined = (text: string) =>
  text.trim() !== "" && isDecimal(text) ? normalizeDecimal(text) : undefined;

export interface ProductionDialogProps {
  order: ManufacturingOrder;
  opened: boolean;
  onClose: () => void;
}

/**
 * Register production (M22, M22a): the good quantity and the output scrap,
 * each component line's consumption prefilled in proportion — read again
 * from `production-draft` whenever either changes — and editable with its
 * scrap and the location it is taken from, and where the output goes.
 *
 * One `clientRequestId` per opening makes the post idempotent: a retry and
 * the consumption warning's *Post anyway* resend the same body under it, the
 * latter with `acceptDeviation`.
 */
export const ProductionDialog = ({ order, opened, onClose }: ProductionDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("registerProductionTitle", { number: order.number })} size="xl">
      {opened && <ProductionForm order={order} onClose={onClose} />}
    </Modal>
  );
};

const ProductionForm = ({ order, onClose }: { order: ManufacturingOrder; onClose: () => void }) => {
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
  };

  const invalid =
    decimalOrUndefined(shownQuantity) === undefined ||
    wantedScrap === undefined ||
    lines.some(
      ({ values }) =>
        (values.consumed.trim() !== "" && !isDecimal(values.consumed)) ||
        (values.scrap.trim() !== "" && !isDecimal(values.scrap)),
    );

  const bodyOf = (): ManufacturingProductionInput => ({
    clientRequestId,
    quantity: normalizeDecimal(shownQuantity || "0"),
    outputScrap: normalizeDecimal(outputScrap || "0"),
    toLocationId: shownToLocationId,
    lines: lines.flatMap(({ line, values }) => {
      const consumed = decimalOrUndefined(values.consumed);
      const scrap = decimalOrUndefined(values.scrap);
      // A line with neither is omitted: not judged and consuming nothing (M22a).
      if (consumed === undefined && scrap === undefined) return [];
      return [
        {
          componentId: line.componentId,
          ...(consumed !== undefined ? { consumed } : {}),
          ...(scrap !== undefined ? { scrap } : {}),
          ...(values.fromLocationId !== null ? { fromLocationId: values.fromLocationId } : {}),
        },
      ];
    }),
  });

  const post = async (body: ManufacturingProductionInput) => {
    setSentBody(body);
    setPosting(true);
    setProblem(undefined);
    try {
      const production = await postProduction(order.id, body);
      notifications.show({
        color: "teal",
        title: t("productionPosted"),
        message: t("productionPostedBody", { quantity: production.quantity, number: production.orderNumber }),
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
                return (
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
                  </Table.Tr>
                );
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
