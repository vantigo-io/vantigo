import { Alert, Anchor, Button, Card, Group, NumberInput, Select, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCircleCheck, IconDeviceFloppy, IconPlayerPause, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { Fragment, useRef, useState } from "react";
import {
  cancelDelivery,
  deliveryQueryOptions,
  packingSlipUrl,
  pickListUrl,
  postDelivery,
  updateDelivery,
} from "../api/deliveries";
import { metaQueryOptions } from "../api/meta";
import { orderQueryOptions } from "../api/orders";
import { NotFoundError, ORDERS_QUERY_KEY, refusedLines } from "../api/request";
import type { Delivery, DeliveryLineInput } from "../api/types";
import { ConfirmDialog } from "../components/confirm-dialog";
import { DeliveryLots } from "../components/delivery-lots";
import { PdfButton } from "../components/pdf-button";
import { ShipmentsCard } from "../components/shipments-card";
import { DeliveryDocumentBadge } from "../components/status-badges";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { type LotPickRow, lotsTotal } from "../lib/delivery-lots";
import { lineRefusalWords, refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { addressLine } from "../lib/header";
import { kitNamesOf } from "../lib/lines";
import { DELIVERIES_ROUTE_PATH, salesOrderLinkOptions } from "../lib/routes";

export interface DeliveryEditorPageProps {
  deliveryId: number;
  access?: OrdersAccess;
}

/**
 * One delivery (leveranse, orders design O47): its order, the site's
 * delivery address, and its lines — a draft's quantities and the location
 * each is picked from, among the site's locations holding it — with the pick
 * list, the packing slip, *Post* and *Cancel*. A posted delivery is shown as
 * it was posted and no longer changes, and is shipped from its Shipments card
 * (O50). A line of an item tracked by lot or serial number is picked by lot
 * instead of by location (manufacturing phase 2 M77): its lots, proposed
 * FEFO, are shown under it and changed there. A kit's component (manufacturing
 * phase 2 M145) is shown under its kit's name, read from the order, so it is
 * told apart from a line of the same product on its own.
 */
export const DeliveryEditorPage = ({ deliveryId, access = noOrdersAccess }: DeliveryEditorPageProps) => {
  const { t } = useOrdersFormat();
  const delivery = useQuery({ ...deliveryQueryOptions(deliveryId), refetchOnWindowFocus: false });
  if (delivery.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (delivery.isError)
    return delivery.error instanceof NotFoundError ? (
      <EmptyState title={t("deliveryNotFound")} description={t("deliveryNotFoundDescription")} />
    ) : (
      <Alert color="red" title={t("failedToLoadDeliveries")}>
        {refusalMessage(delivery.error, t)}
      </Alert>
    );
  return (
    <DeliveryEditor
      key={`${delivery.data.id}:${delivery.data.version}:${delivery.data.updatedAt}`}
      delivery={delivery.data}
      access={access}
    />
  );
};

interface Row {
  orderLineId: number;
  quantity: number | "";
  fromLocationId: number | null;
  /** A tracked line's lots; sent only once the person changed them. */
  lots: LotPickRow[];
  lotsEdited: boolean;
}

/** Where a sent line stands among the editor's rows, and each of its sent lot rows among the row's lots. */
interface Sent {
  row: number;
  lots: number[];
}

/**
 * The lines a save sends — a line at 0 left out, a lot at 0 too — and where
 * each stands in the editor, so a refusal's indexes find their rows again.
 * The line at `proposeAgain` is sent with *Propose again* and no lots.
 */
const deliveryInputs = (rows: readonly Row[], proposeAgain: number | null) => {
  const lines: DeliveryLineInput[] = [];
  const sent: Sent[] = [];
  rows.forEach((row, index) => {
    if (row.quantity === "" || row.quantity <= 0) return;
    const input: DeliveryLineInput = {
      orderLineId: row.orderLineId,
      quantity: row.quantity,
      fromLocationId: row.fromLocationId,
    };
    if (index === proposeAgain) {
      lines.push({ ...input, proposeAgain: true });
      sent.push({ row: index, lots: [] });
      return;
    }
    if (!row.lotsEdited) {
      lines.push(input);
      sent.push({ row: index, lots: [] });
      return;
    }
    const kept = row.lots.flatMap((lot, at) => (lot.quantity !== "" && lot.quantity > 0 ? [{ lot, at }] : []));
    lines.push({
      ...input,
      lots: kept.map(({ lot }) => ({
        lotId: lot.lotId,
        locationId: lot.locationId,
        quantity: lot.quantity as number,
        ...(lot.allowExpired ? { allowExpired: true } : {}),
      })),
    });
    sent.push({ row: index, lots: kept.map(({ at }) => at) });
  });
  return { lines, sent };
};

const DeliveryEditor = ({ delivery, access }: { delivery: Delivery; access: OrdersAccess }) => {
  const { t, quantity } = useOrdersFormat();
  const queryClient = useQueryClient();
  const detailKey = deliveryQueryOptions(delivery.id).queryKey;
  const draft = delivery.status === "draft";
  const editable = draft && access.canManageDeliveries;
  const meta = useQuery({ ...metaQueryOptions(), enabled: editable });
  const order = useQuery(orderQueryOptions(delivery.orderId));
  const kitNames = kitNamesOf(order.data?.lines ?? []);
  const [rows, setRows] = useState<Row[]>(() =>
    delivery.lines.map((line) => ({
      orderLineId: line.orderLineId,
      quantity: line.quantity,
      fromLocationId: line.fromLocationId ?? null,
      lots: line.lots.map((lot) => ({
        lotId: lot.lotId,
        locationId: lot.locationId,
        quantity: lot.quantity,
        allowExpired: lot.allowExpired,
      })),
      lotsEdited: false,
    })),
  );
  const [dirty, setDirty] = useState(false);
  const [refused, setRefused] = useState<ReadonlyMap<number, string>>(new Map());
  // A refusal naming a tracked line's lot row, by "line:lot".
  const [refusedLots, setRefusedLots] = useState<ReadonlyMap<string, string>>(new Map());
  const [dialog, setDialog] = useState<"cancel" | null>(null);
  // Where the last save's lines stood, to map its refusal back to the rows.
  const sentRef = useRef<Sent[]>([]);

  const done = async (next: Delivery, message: string) => {
    queryClient.setQueryData(detailKey, next);
    await queryClient.invalidateQueries({
      queryKey: [ORDERS_QUERY_KEY],
      predicate: (query) => query.queryKey[2] !== "detail" || query.queryKey[1] === "orders",
    });
    notifications.show({ color: "green", message });
    setDialog(null);
  };
  // A post's and a cancel's indexes are the delivery's lines; a save's, the lines it sent.
  const refuse = (title: string, sent?: readonly Sent[]) => (error: unknown) => {
    const failing = refusedLines(error);
    const rowOf = (index: number) => (sent ? (sent[index]?.row ?? index) : index);
    const lotOf = (index: number, lot: number) => (sent ? (sent[index]?.lots[lot] ?? lot) : lot);
    setRefused(
      new Map(
        failing
          .filter((line) => line.lotIndex === undefined)
          .map((line) => [rowOf(line.index), lineRefusalWords(line.code, line.detail, t)]),
      ),
    );
    setRefusedLots(
      new Map(
        failing.flatMap((line) =>
          line.lotIndex === undefined
            ? []
            : [
                [
                  `${rowOf(line.index)}:${lotOf(line.index, line.lotIndex)}`,
                  lineRefusalWords(line.code, line.detail, t),
                ] as const,
              ],
        ),
      ),
    );
    notifications.show({ color: "red", title, message: refusalMessage(error, t) });
  };
  const save = useMutation({
    mutationFn: (proposeAgain: number | null) => {
      const { lines, sent } = deliveryInputs(rows, proposeAgain);
      sentRef.current = sent;
      return updateDelivery(delivery.id, delivery, lines);
    },
    onSuccess: (next) => done(next, t("deliverySaved")),
    onError: (error) => refuse(t("couldNotSave"), sentRef.current)(error),
  });
  const post = useMutation({
    mutationFn: () => postDelivery(delivery.id, delivery),
    onSuccess: (next) => done(next, t("deliveryPosted", { number: delivery.number })),
    onError: refuse(t("couldNotPostDelivery")),
  });
  const cancel = useMutation({
    mutationFn: () => cancelDelivery(delivery.id, delivery),
    onSuccess: (next) => done(next, t("deliveryCancelled", { number: delivery.number })),
    onError: refuse(t("couldNotChangeDelivery")),
  });
  const set = (index: number, patch: Partial<Row>) => {
    setRows((current) => current.map((row, i) => (i === index ? { ...row, ...patch } : row)));
    setDirty(true);
    setRefused(new Map());
    setRefusedLots(new Map());
  };
  const busy = save.isPending || post.isPending || cancel.isPending;
  const unpicked = rows.some((row, index) => !delivery.lines[index]?.tracked && row.fromLocationId === null);
  const lotsOff = rows.some(
    (row, index) => delivery.lines[index]?.tracked && lotsTotal(row.lots) !== (row.quantity === "" ? 0 : row.quantity),
  );
  const postBlocked = delivery.orderOnHold
    ? t("heldTakesNoDelivery")
    : dirty
      ? t("saveFirst")
      : unpicked
        ? t("pickEveryLine")
        : lotsOff
          ? t("lotsMustAddUp")
          : undefined;
  const title = t("deliveryNumber", { number: delivery.number });
  const address = addressLine(delivery.deliveryAddress);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("deliveriesTitle"), to: DELIVERIES_ROUTE_PATH }, { label: title }]}
        title={
          <Group gap="sm">
            <span>{title}</span>
            <DeliveryDocumentBadge status={delivery.status} />
          </Group>
        }
        description={
          <Group gap="xs" component="span">
            <Anchor size="sm" renderRoot={(props) => <Link {...salesOrderLinkOptions(delivery.orderId)} {...props} />}>
              {t("orderNumber", { number: delivery.orderNumber })}
            </Anchor>
            <span>{delivery.customerName}</span>
          </Group>
        }
        actions={
          <Group gap="xs">
            <PdfButton url={pickListUrl(delivery.id)} variant="default" data-assist="orders.delivery.pick-list">
              {t("pickList")}
            </PdfButton>
            <PdfButton url={packingSlipUrl(delivery.id)} variant="default" mode="download">
              {t("packingSlip")}
            </PdfButton>
            {editable && (
              <Button
                leftSection={<IconDeviceFloppy size={16} />}
                disabled={!dirty || busy}
                loading={save.isPending}
                onClick={() => save.mutate(null)}
              >
                {t("save")}
              </Button>
            )}
          </Group>
        }
      />
      {delivery.orderOnHold && draft && (
        <Alert color="orange" icon={<IconPlayerPause size={16} />}>
          {t("heldTakesNoDelivery")}
        </Alert>
      )}
      {editable && (
        <Card withBorder padding="sm">
          <Group gap="xs">
            <Button
              color="teal"
              leftSection={<IconCircleCheck size={16} />}
              disabled={Boolean(postBlocked) || busy}
              title={postBlocked}
              loading={post.isPending}
              onClick={() => post.mutate()}
              data-assist="orders.delivery.post"
            >
              {t("postDelivery")}
            </Button>
            <Button
              variant="light"
              color="red"
              leftSection={<IconX size={16} />}
              disabled={busy}
              onClick={() => setDialog("cancel")}
            >
              {t("cancelDelivery")}
            </Button>
            {postBlocked && (
              <Text size="xs" c="dimmed">
                {postBlocked}
              </Text>
            )}
          </Group>
        </Card>
      )}
      <PageSection>
        <Stack gap="xs">
          <Title order={5}>{t("deliveryAddress")}</Title>
          <Text size="sm">{address || "—"}</Text>
        </Stack>
      </PageSection>
      <PageSection>
        <Stack gap="md">
          <Title order={4}>{t("lines")}</Title>
          <Table.ScrollContainer minWidth={720}>
            <Table verticalSpacing="xs" data-testid="delivery-lines">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("lineColumn")}</Table.Th>
                  <Table.Th ta="right">{t("ordered")}</Table.Th>
                  <Table.Th ta="right">{t("openToDeliverColumn")}</Table.Th>
                  <Table.Th w={140}>{t("quantity")}</Table.Th>
                  <Table.Th w={240}>{t("fromLocation")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {delivery.lines.map((line, index) => {
                  const row = rows[index];
                  const options = line.locations.map((loc) => ({
                    value: String(loc.locationId),
                    label: `${loc.fullCode} (${quantity(loc.quantity)})`,
                  }));
                  if (line.fromLocationId != null && !options.some((o) => o.value === String(line.fromLocationId)))
                    options.unshift({ value: String(line.fromLocationId), label: line.fromLocationCode ?? "" });
                  const problem = refused.get(index);
                  const kit = kitNames.get(line.orderLineId);
                  const lotProblems = new Map<number, string>();
                  for (const [key, words] of refusedLots) {
                    const [lineIndex, lotIndex] = key.split(":").map(Number);
                    if (lineIndex === index && lotIndex !== undefined) lotProblems.set(lotIndex, words);
                  }
                  return (
                    <Fragment key={line.id}>
                      <Table.Tr>
                        <Table.Td pl={kit ? "xl" : undefined}>
                          <Text size="sm">
                            {kit && (
                              <Text span c="dimmed">
                                {"↳ "}
                              </Text>
                            )}
                            {[line.sku, line.name].filter(Boolean).join(" ")}
                          </Text>
                          {kit && (
                            <Text size="xs" c="dimmed" data-testid="kit-child-of">
                              {t("inKit", { kit })}
                            </Text>
                          )}
                          {problem && (
                            <Text size="xs" c="red" role="alert">
                              {problem}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td ta="right">
                          <Text size="sm">{quantity(line.ordered)}</Text>
                        </Table.Td>
                        <Table.Td ta="right">
                          <Text size="sm">{quantity(line.open)}</Text>
                        </Table.Td>
                        <Table.Td>
                          {editable ? (
                            <NumberInput
                              size="xs"
                              aria-label={t("deliveryQuantity", { position: index + 1 })}
                              min={0}
                              max={line.open}
                              decimalScale={3}
                              value={row.quantity}
                              onChange={(value) => set(index, { quantity: typeof value === "number" ? value : "" })}
                              rightSection={<Text size="xs">{line.unit}</Text>}
                            />
                          ) : (
                            <Text size="sm">
                              {quantity(line.quantity)} {line.unit}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {line.tracked ? (
                            <Text size="sm" c="dimmed">
                              {t("pickedByLot")}
                            </Text>
                          ) : editable ? (
                            <Select
                              size="xs"
                              aria-label={t("deliveryLocation", { position: index + 1 })}
                              placeholder={options.length === 0 ? t("noStockAtSite") : t("chooseLocation")}
                              data={options}
                              value={row.fromLocationId === null ? null : String(row.fromLocationId)}
                              onChange={(value) => set(index, { fromLocationId: value ? Number(value) : null })}
                            />
                          ) : (
                            <Text size="sm">{line.fromLocationCode ?? "—"}</Text>
                          )}
                        </Table.Td>
                      </Table.Tr>
                      {line.tracked && (
                        <Table.Tr>
                          <Table.Td colSpan={5} pl="xl">
                            <DeliveryLots
                              line={line}
                              position={index + 1}
                              picks={row.lots}
                              quantity={row.quantity === "" ? 0 : row.quantity}
                              editable={editable}
                              canOverrideExpired={meta.data?.canOverrideExpired ?? false}
                              canTraceLots={meta.data?.canTraceLots ?? false}
                              onChange={(lots) => set(index, { lots, lotsEdited: true })}
                              onProposeAgain={() => save.mutate(index)}
                              busy={busy}
                              refused={lotProblems}
                            />
                          </Table.Td>
                        </Table.Tr>
                      )}
                    </Fragment>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          {editable && (
            <Text size="xs" c="dimmed">
              {t("deliveryEditHint")}
            </Text>
          )}
        </Stack>
      </PageSection>
      <ShipmentsCard
        deliveryId={delivery.id}
        posted={delivery.status === "posted"}
        canManage={access.canManageDeliveries}
      />
      {dialog === "cancel" && (
        <ConfirmDialog
          title={t("cancelDelivery")}
          confirmLabel={t("cancelDelivery")}
          danger
          loading={cancel.isPending}
          onConfirm={() => cancel.mutate()}
          onClose={() => setDialog(null)}
        >
          {t("cancelDeliveryHint")}
        </ConfirmDialog>
      )}
    </Stack>
  );
};
