import { Alert, Anchor, Button, Card, Group, NumberInput, Select, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCircleCheck, IconDeviceFloppy, IconPlayerPause, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  cancelDelivery,
  deliveryQueryOptions,
  packingSlipUrl,
  pickListUrl,
  postDelivery,
  updateDelivery,
} from "../api/deliveries";
import { NotFoundError, ORDERS_QUERY_KEY, refusedLines } from "../api/request";
import type { Delivery } from "../api/types";
import { ConfirmDialog } from "../components/confirm-dialog";
import { PdfButton } from "../components/pdf-button";
import { DeliveryDocumentBadge } from "../components/status-badges";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { lineRefusalWords, refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { addressLine } from "../lib/header";
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
 * it was posted and no longer changes.
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
}

const DeliveryEditor = ({ delivery, access }: { delivery: Delivery; access: OrdersAccess }) => {
  const { t, quantity } = useOrdersFormat();
  const queryClient = useQueryClient();
  const detailKey = deliveryQueryOptions(delivery.id).queryKey;
  const draft = delivery.status === "draft";
  const editable = draft && access.canManageDeliveries;
  const [rows, setRows] = useState<Row[]>(() =>
    delivery.lines.map((line) => ({
      orderLineId: line.orderLineId,
      quantity: line.quantity,
      fromLocationId: line.fromLocationId ?? null,
    })),
  );
  const [dirty, setDirty] = useState(false);
  const [refused, setRefused] = useState<ReadonlyMap<number, string>>(new Map());
  const [dialog, setDialog] = useState<"cancel" | null>(null);

  const done = async (next: Delivery, message: string) => {
    queryClient.setQueryData(detailKey, next);
    await queryClient.invalidateQueries({
      queryKey: [ORDERS_QUERY_KEY],
      predicate: (query) => query.queryKey[2] !== "detail" || query.queryKey[1] === "orders",
    });
    notifications.show({ color: "green", message });
    setDialog(null);
  };
  const refuse = (title: string) => (error: unknown) => {
    const failing = refusedLines(error);
    setRefused(new Map(failing.map((line) => [line.index, lineRefusalWords(line.code, line.detail, t)])));
    notifications.show({ color: "red", title, message: refusalMessage(error, t) });
  };
  const save = useMutation({
    mutationFn: () =>
      updateDelivery(
        delivery.id,
        delivery,
        rows
          .filter((row) => row.quantity !== "" && row.quantity > 0)
          .map((row) => ({
            orderLineId: row.orderLineId,
            quantity: row.quantity as number,
            fromLocationId: row.fromLocationId,
          })),
      ),
    onSuccess: (next) => done(next, t("deliverySaved")),
    onError: refuse(t("couldNotSave")),
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
  };
  const busy = save.isPending || post.isPending || cancel.isPending;
  const unpicked = rows.some((row) => row.fromLocationId === null);
  const postBlocked = delivery.orderOnHold
    ? t("heldTakesNoDelivery")
    : dirty
      ? t("saveFirst")
      : unpicked
        ? t("pickEveryLine")
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
                onClick={() => save.mutate()}
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
      <Card withBorder padding="lg">
        <Stack gap="xs">
          <Title order={5}>{t("deliveryAddress")}</Title>
          <Text size="sm">{address || "—"}</Text>
        </Stack>
      </Card>
      <Card withBorder padding="lg">
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
                  return (
                    <Table.Tr key={line.id}>
                      <Table.Td>
                        <Text size="sm">{[line.sku, line.name].filter(Boolean).join(" ")}</Text>
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
                        {editable ? (
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
      </Card>
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
