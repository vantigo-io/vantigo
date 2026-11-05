import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  NumberInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconCoin, IconPlus, IconTrash, IconTruckDelivery } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ORDERS_QUERY_KEY } from "../api/request";
import {
  bookShipment,
  cancelShipment,
  carrierAccountsQueryOptions,
  carrierServicesQueryOptions,
  createShipment,
  deliveryShipmentsQueryOptions,
  orderShipmentsQueryOptions,
  shipmentLabelUrl,
  shipmentRates,
  shipmentWaybillUrl,
  updateShipment,
} from "../api/shipping";
import type { Shipment, ShipmentPackageInput, ShippingOption } from "../api/types";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { ConfirmDialog } from "./confirm-dialog";
import { PdfButton } from "./pdf-button";

const statusColours: Record<string, string> = {
  draft: "gray",
  booked: "blue",
  in_transit: "cyan",
  delivered: "teal",
  cancelled: "red",
};

/** A shipment's state, and its exception flag beside it (O50). */
export const ShipmentStatusBadge = ({ shipment }: { shipment: Shipment }) => {
  const { t } = useOrdersFormat();
  return (
    <Group gap={4}>
      <Badge variant="light" color={statusColours[shipment.status] ?? "gray"} data-testid="shipment-status">
        {t(`shipmentStatus.${shipment.status}`)}
      </Badge>
      {shipment.exception && (
        <Badge variant="filled" color="orange" leftSection={<IconAlertTriangle size={10} />}>
          {t("exception")}
        </Badge>
      )}
      {shipment.test && (
        <Badge variant="outline" color="yellow">
          {t("testBadge")}
        </Badge>
      )}
    </Group>
  );
};

export interface ShipmentsCardProps {
  deliveryId: number;
  /** A posted delivery is shipped (O50). */
  posted: boolean;
  /** `orders:deliveries-manage`: create, book and cancel shipments and print their labels. */
  canManage: boolean;
}

/**
 * A posted delivery's shipments (frakt, orders design O50): *Ship* proposes
 * one package weighing the delivered goods, to the order's delivery address,
 * on the site's default carrier account; a draft's packages and service are
 * edited, its prices shown and it is booked; a booked one's label and waybill
 * print, its tracking shows, and it is cancelled — voided in Vantigo when the
 * carrier has no cancel.
 */
export const ShipmentsCard = ({ deliveryId, posted, canManage }: ShipmentsCardProps) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const shipments = useQuery({ ...deliveryShipmentsQueryOptions(deliveryId), enabled: posted });
  const create = useMutation({
    mutationFn: () => createShipment(deliveryId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "green", message: t("shipmentCreated") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreateShipment"), message: refusalMessage(error, t) }),
  });
  if (!posted) return null;
  const items = shipments.data?.items ?? [];
  return (
    <PageSection data-testid="shipments-card">
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t("shipments")}</Title>
          {canManage && (
            <Button
              size="xs"
              leftSection={<IconTruckDelivery size={14} />}
              loading={create.isPending}
              onClick={() => create.mutate()}
              data-assist="orders.delivery.ship"
            >
              {t("createShipment")}
            </Button>
          )}
        </Group>
        {items.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noShipments")}
          </Text>
        )}
        {items.map((shipment) => (
          <ShipmentPanel
            key={`${shipment.id}:${shipment.version}:${shipment.updatedAt}`}
            shipment={shipment}
            canManage={canManage}
          />
        ))}
      </Stack>
    </PageSection>
  );
};

interface PackageRow {
  weightGrams: number | "";
  lengthMm: number | "";
  widthMm: number | "";
  heightMm: number | "";
  content: string;
}

const ShipmentPanel = ({ shipment, canManage }: { shipment: Shipment; canManage: boolean }) => {
  const { t, money, dateTime } = useOrdersFormat();
  const queryClient = useQueryClient();
  const draft = shipment.status === "draft";
  // A draft whose booking's outcome is unknown is booked again as it was, never changed.
  const bookable = draft && canManage;
  const editable = bookable && !shipment.bookAttempted;
  const accounts = useQuery({ ...carrierAccountsQueryOptions(), enabled: editable });
  const [accountId, setAccountId] = useState(shipment.carrierAccountId);
  const services = useQuery({ ...carrierServicesQueryOptions(accountId), enabled: editable });
  const [service, setService] = useState(shipment.service);
  const [phone, setPhone] = useState(shipment.recipient.phone);
  const [email, setEmail] = useState(shipment.recipient.email);
  const [packages, setPackages] = useState<PackageRow[]>(() =>
    shipment.packages.map((p) => ({
      weightGrams: p.weightGrams,
      lengthMm: p.lengthMm,
      widthMm: p.widthMm,
      heightMm: p.heightMm,
      content: p.content,
    })),
  );
  const [dirty, setDirty] = useState(false);
  const [rates, setRates] = useState<ShippingOption[] | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const touched =
    <T,>(set: (value: T) => void) =>
    (value: T) => {
      set(value);
      setDirty(true);
      setRates(null);
    };
  const done = async (message: string) => {
    await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
    notifications.show({ color: "green", message });
  };
  const save = useMutation({
    mutationFn: () =>
      updateShipment(shipment.id, {
        version: shipment.version,
        expectedUpdatedAt: shipment.updatedAt,
        carrierAccountId: accountId,
        service,
        vas: shipment.vas,
        recipientPhone: phone,
        recipientEmail: email,
        recipientContact: shipment.recipient.contact,
        pickupPointId: shipment.pickupPointId ?? null,
        packages: packages.map(
          (p): ShipmentPackageInput => ({
            weightGrams: p.weightGrams === "" ? 0 : p.weightGrams,
            lengthMm: p.lengthMm === "" ? 0 : p.lengthMm,
            widthMm: p.widthMm === "" ? 0 : p.widthMm,
            heightMm: p.heightMm === "" ? 0 : p.heightMm,
            content: p.content,
          }),
        ),
      }),
    onSuccess: () => done(t("shipmentSaved")),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t) }),
  });
  const price = useMutation({
    mutationFn: () => shipmentRates(shipment.id),
    onSuccess: (answer) => setRates(answer.items),
    onError: (error) => notifications.show({ color: "red", title: t("rates"), message: refusalMessage(error, t) }),
  });
  const book = useMutation({
    mutationFn: () => bookShipment(shipment),
    onSuccess: (booked) => done(t("shipmentBooked", { consignment: booked.consignmentNumber ?? "" })),
    onError: async (error) => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "red", title: t("couldNotBookShipment"), message: refusalMessage(error, t) });
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelShipment(shipment),
    onSuccess: () => done(t("shipmentCancelled")),
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCancelShipment"), message: refusalMessage(error, t) }),
  });
  const busy = save.isPending || book.isPending || cancel.isPending;
  const setPackage = (index: number, patch: Partial<PackageRow>) =>
    touched(setPackages)(packages.map((p, i) => (i === index ? { ...p, ...patch } : p)));
  const dimension = (index: number, field: "lengthMm" | "widthMm" | "heightMm", label: string) => (
    <NumberInput
      size="xs"
      aria-label={`${label} ${index + 1}`}
      min={0}
      max={10000}
      hideControls
      value={packages[index][field]}
      onChange={(value) => setPackage(index, { [field]: value === "" ? "" : Number(value) })}
    />
  );

  return (
    <Card withBorder padding="sm" radius="sm" data-testid="shipment">
      <Stack gap="xs">
        <Group justify="space-between" wrap="wrap">
          <Group gap="xs">
            <Text fw={600}>
              {shipment.carrierAccountName} · {shipment.serviceName}
            </Text>
            <ShipmentStatusBadge shipment={shipment} />
          </Group>
          <Group gap="xs">
            {shipment.consignmentNumber && (
              <Text size="sm">
                {t("consignmentNumber")}:{" "}
                <Text span ff="monospace">
                  {shipment.consignmentNumber}
                </Text>
              </Text>
            )}
            {shipment.trackingUrl && (
              <Anchor size="sm" href={shipment.trackingUrl} target="_blank" rel="noreferrer">
                {t("trackShipment")}
              </Anchor>
            )}
          </Group>
        </Group>
        {shipment.voided && (
          <Alert color="red" p="xs">
            {t("voidedHint")}
          </Alert>
        )}
        {bookable && shipment.bookAttempted && (
          <Alert color="yellow" p="xs">
            {t("bookAttemptedHint")}
          </Alert>
        )}
        {shipment.labelInMybringOnly && !shipment.voided && (
          <Alert color="yellow" p="xs" data-testid="label-in-mybring">
            {t("labelInMybringOnly")}
          </Alert>
        )}
        {bookable && shipment.test && (
          <Text size="xs" c="yellow.8">
            {t("testBookingWarning")}
          </Text>
        )}
        {editable && (
          <Group grow align="flex-start">
            <Select
              size="xs"
              label={t("carrierAccounts")}
              data={(accounts.data?.items ?? [])
                .filter((a) => a.status === "active" || a.id === shipment.carrierAccountId)
                .map((a) => ({ value: String(a.id), label: a.name }))}
              value={String(accountId)}
              allowDeselect={false}
              onChange={(value) => value && touched(setAccountId)(Number(value))}
            />
            <Select
              size="xs"
              label={t("service")}
              data={(services.data?.items ?? []).map((s) => ({ value: s.code, label: `${s.code} ${s.name}` }))}
              value={service}
              allowDeselect={false}
              onChange={(value) => value && touched(setService)(value)}
              data-assist="orders.shipment.service"
            />
            <TextInput
              size="xs"
              label={t("recipientPhone")}
              value={phone}
              onChange={(e) => touched(setPhone)(e.currentTarget.value)}
            />
            <TextInput
              size="xs"
              label={t("recipientEmail")}
              value={email}
              onChange={(e) => touched(setEmail)(e.currentTarget.value)}
            />
          </Group>
        )}
        <Table verticalSpacing={4} data-testid="shipment-packages">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("packages")}</Table.Th>
              <Table.Th>{t("packageWeight")}</Table.Th>
              <Table.Th>{t("packageLength")}</Table.Th>
              <Table.Th>{t("packageWidth")}</Table.Th>
              <Table.Th>{t("packageHeight")}</Table.Th>
              <Table.Th>{t("packageContent")}</Table.Th>
              {editable && <Table.Th w={40} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {editable
              ? packages.map((p, index) => (
                  <Table.Tr key={index}>
                    <Table.Td>{index + 1}</Table.Td>
                    <Table.Td>
                      <NumberInput
                        size="xs"
                        aria-label={`${t("packageWeight")} ${index + 1}`}
                        min={1}
                        max={1000000}
                        hideControls
                        value={p.weightGrams}
                        onChange={(value) => setPackage(index, { weightGrams: value === "" ? "" : Number(value) })}
                      />
                    </Table.Td>
                    <Table.Td>{dimension(index, "lengthMm", t("packageLength"))}</Table.Td>
                    <Table.Td>{dimension(index, "widthMm", t("packageWidth"))}</Table.Td>
                    <Table.Td>{dimension(index, "heightMm", t("packageHeight"))}</Table.Td>
                    <Table.Td>
                      <TextInput
                        size="xs"
                        aria-label={`${t("packageContent")} ${index + 1}`}
                        maxLength={200}
                        value={p.content}
                        onChange={(e) => setPackage(index, { content: e.currentTarget.value })}
                      />
                    </Table.Td>
                    <Table.Td>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("removePackage", { position: index + 1 })}
                        disabled={packages.length <= 1}
                        onClick={() => touched(setPackages)(packages.filter((_, i) => i !== index))}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                ))
              : shipment.packages.map((p) => (
                  <Table.Tr key={p.position}>
                    <Table.Td>
                      {p.position}
                      {p.packageNumber && (
                        <Text span size="xs" c="dimmed" ff="monospace" ml={6}>
                          {p.packageNumber}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>{p.weightGrams}</Table.Td>
                    <Table.Td>{p.lengthMm || "—"}</Table.Td>
                    <Table.Td>{p.widthMm || "—"}</Table.Td>
                    <Table.Td>{p.heightMm || "—"}</Table.Td>
                    <Table.Td>{p.content || "—"}</Table.Td>
                  </Table.Tr>
                ))}
          </Table.Tbody>
        </Table>
        {bookable && (
          <Group gap="xs">
            {editable && (
              <>
                <Button
                  size="xs"
                  variant="subtle"
                  leftSection={<IconPlus size={14} />}
                  disabled={packages.length >= 20}
                  onClick={() =>
                    touched(setPackages)([
                      ...packages,
                      { weightGrams: 1000, lengthMm: 0, widthMm: 0, heightMm: 0, content: "" },
                    ])
                  }
                >
                  {t("addPackage")}
                </Button>
                <Button
                  size="xs"
                  variant="default"
                  disabled={!dirty || busy}
                  loading={save.isPending}
                  onClick={() => save.mutate()}
                >
                  {t("saveShipment")}
                </Button>
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconCoin size={14} />}
                  disabled={dirty || busy}
                  loading={price.isPending}
                  onClick={() => price.mutate()}
                >
                  {t("getRates")}
                </Button>
              </>
            )}
            <Button
              size="xs"
              color="teal"
              leftSection={<IconTruckDelivery size={14} />}
              disabled={dirty || busy}
              title={dirty ? t("saveFirst") : undefined}
              loading={book.isPending}
              onClick={() => book.mutate()}
              data-assist="orders.shipment.book"
            >
              {t("bookShipment")}
            </Button>
          </Group>
        )}
        {rates && (
          <Table verticalSpacing={2} data-testid="shipment-rates">
            <Table.Tbody>
              {rates.map((option) => (
                <Table.Tr key={option.service} fw={option.service === service ? 600 : undefined}>
                  <Table.Td>
                    {option.service} {option.name}
                  </Table.Td>
                  <Table.Td ta="right">
                    {money(option.price, option.currency)}
                    {option.estimate && (
                      <Text span size="xs" c="dimmed" ml={4}>
                        ({t("priceEstimate")})
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>{option.expectedDate ?? ""}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {!draft && (
          <Group gap="xs">
            {shipment.label && (
              <PdfButton url={shipmentLabelUrl(shipment.id)} size="xs" variant="default" disabled={shipment.voided}>
                {t("label")}
              </PdfButton>
            )}
            {shipment.waybill && (
              <PdfButton url={shipmentWaybillUrl(shipment.id)} size="xs" variant="default">
                {t("waybill")}
              </PdfButton>
            )}
            {!shipment.waybillNeeded && shipment.label && (
              <Text size="xs" c="dimmed">
                {t("labelServesAsWaybill")}
              </Text>
            )}
            {shipment.bookedCost != null && shipment.bookedCostCurrency && (
              <Text size="xs" c="dimmed">
                {t("bookedCost")}:{" "}
                {shipment.bookedCostEstimate
                  ? t("bookedCostEstimate", { amount: money(shipment.bookedCost, shipment.bookedCostCurrency) })
                  : money(shipment.bookedCost, shipment.bookedCostCurrency)}
              </Text>
            )}
          </Group>
        )}
        {shipment.events.length > 0 && (
          <Stack gap={2} data-testid="shipment-events">
            <Text size="xs" fw={600}>
              {t("trackingEvents")}
            </Text>
            {shipment.events.map((event) => (
              <Text
                key={`${event.occurredAt}:${event.code}`}
                size="xs"
                c={event.state === "exception" ? "orange" : "dimmed"}
              >
                {dateTime(event.occurredAt)} · {event.text || event.code}
                {event.place && ` · ${event.place}`}
              </Text>
            ))}
          </Stack>
        )}
        {canManage && (shipment.status === "draft" || shipment.status === "booked") && (
          <Group>
            <Button size="xs" variant="subtle" color="red" disabled={busy} onClick={() => setCancelling(true)}>
              {t("cancelShipment")}
            </Button>
          </Group>
        )}
      </Stack>
      {cancelling && (
        <ConfirmDialog
          title={t("cancelShipment")}
          confirmLabel={t("cancelShipment")}
          danger
          loading={cancel.isPending}
          onConfirm={() => cancel.mutate(undefined, { onSettled: () => setCancelling(false) })}
          onClose={() => setCancelling(false)}
        >
          {t("cancelShipmentHint")}
        </ConfirmDialog>
      )}
    </Card>
  );
};

/** An order's shipments with their tracking links (O52), on the order's page. */
export const OrderShipmentsCard = ({ orderId }: { orderId: number }) => {
  const { t } = useOrdersFormat();
  const shipments = useQuery(orderShipmentsQueryOptions(orderId));
  const items = (shipments.data?.items ?? []).filter((s) => s.status !== "draft");
  if (items.length === 0) return null;
  return (
    <PageSection data-testid="order-shipments">
      <Stack gap="xs">
        <Title order={4}>{t("orderShipments")}</Title>
        {items.map((shipment) => (
          <Group key={shipment.id} gap="sm">
            <Text size="sm">{t("deliveryNumber", { number: shipment.deliveryNumber })}</Text>
            <Text size="sm">
              {shipment.carrierAccountName} · {shipment.serviceName}
            </Text>
            {shipment.consignmentNumber && (
              <Text size="sm" ff="monospace">
                {shipment.consignmentNumber}
              </Text>
            )}
            <ShipmentStatusBadge shipment={shipment} />
            {shipment.trackingUrl && !shipment.voided && (
              <Anchor size="sm" href={shipment.trackingUrl} target="_blank" rel="noreferrer">
                {t("tracking")}
              </Anchor>
            )}
          </Group>
        ))}
      </Stack>
    </PageSection>
  );
};
