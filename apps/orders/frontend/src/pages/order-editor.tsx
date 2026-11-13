import { Alert, Anchor, Button, Card, Group, Menu, Stack, Text, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconArrowBackUp,
  IconChevronDown,
  IconCircleCheck,
  IconDeviceFloppy,
  IconDots,
  IconFileInvoice,
  IconFileText,
  IconLock,
  IconLockOpen,
  IconPackage,
  IconPlayerPause,
  IconPlayerPlay,
  IconSend,
  IconTruckDelivery,
  IconX,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { metaQueryOptions } from "../api/meta";
import {
  deleteOrder,
  holdOrder,
  markDelivered,
  type OrderTransition,
  orderChangesQueryOptions,
  orderDocumentsQueryOptions,
  orderMailingsQueryOptions,
  orderPdfUrl,
  orderProformaUrl,
  orderQueryOptions,
  releaseDraftAllocations,
  reserveOrder,
  sendOrder,
  transitionOrder,
  undoDelivered,
  updateOrder,
} from "../api/orders";
import { isVersionConflict, NotFoundError, ORDERS_QUERY_KEY, refusalCode, refusedLines } from "../api/request";
import { estimateShipping } from "../api/shipping";
import type { DeliveredLine, Order } from "../api/types";
import { overrideOf } from "../api/warnings";
import { ChangeLog } from "../components/change-log";
import { ConfirmDialog } from "../components/confirm-dialog";
import { ConflictAlert } from "../components/conflict-alert";
import { DeliveredDialog } from "../components/delivered-dialog";
import { DeliveriesCard } from "../components/deliveries-card";
import { DocumentsCard } from "../components/documents-card";
import { HeaderCard } from "../components/header-card";
import { InvoicesCard } from "../components/invoices-card";
import { Lifecycle, type LifecycleStep } from "../components/lifecycle";
import { LineGrid } from "../components/line-grid";
import { NewDocument } from "../components/new-document";
import { PdfButton } from "../components/pdf-button";
import { ReasonDialog } from "../components/reason-dialog";
import { SendDialog } from "../components/send-dialog";
import { OrderShipmentsCard } from "../components/shipments-card";
import { DeliveryBadge, InvoiceBadge, OrderFlags, OrderStatusBadge } from "../components/status-badges";
import { TotalsCard } from "../components/totals-card";
import { WarningBanners } from "../components/warning-banners";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { headerInput } from "../lib/header";
import { kitChildrenOf, remainingToDeliver, toLineInput } from "../lib/lines";
import { invoiceFromOrderLinkOptions, quoteLinkOptions, SALES_ORDERS_ROUTE_PATH } from "../lib/routes";
import { useDocumentWrite, useEditorState } from "../lib/use-editor";

export interface OrderEditorPageProps {
  /** The order; absent on the new order's page. */
  orderId?: number;
  /** A new order's customer, when it was opened from the customer page (O31a). */
  customerId?: number;
  /** A new order's first line: the product's variant, picked when it has several (Messenger design N17). */
  productId?: number;
  access?: OrdersAccess;
}

/**
 * An order's editor, new or existing (O18, O26, O32): the header, the lines
 * with each one's delivered, invoiced and cancelled quantities, the server's
 * totals, and the state changes the order and the caller's keys allow —
 * confirm, hold and release, lock and unlock, cancel or cancel the rest,
 * register delivered and undo it, send the confirmation, invoice it in
 * Invoices' *From orders* wizard and release what draft invoices hold (O36) —
 * with the invoices issued from it (O41), the change log, the stored
 * confirmations and the sends.
 */
export const OrderEditorPage = ({ orderId, customerId, productId, access = noOrdersAccess }: OrderEditorPageProps) => {
  const { t } = useOrdersFormat();
  const order = useQuery({
    ...orderQueryOptions(orderId ?? 0),
    enabled: orderId !== undefined,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (orderId === undefined)
    return <NewDocument kind="order" customerId={customerId} productId={productId} access={access} />;
  if (order.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (order.isError)
    return order.error instanceof NotFoundError ? (
      <EmptyState title={t("orderNotFound")} description={t("orderNotFoundDescription")} />
    ) : (
      <Alert color="red" title={t("failedToLoadOrder")}>
        {refusalMessage(order.error, t)}
      </Alert>
    );
  return (
    <OrderEditor
      key={`${order.data.id}:${order.data.version}:${order.data.updatedAt}`}
      order={order.data}
      access={access}
    />
  );
};

const lifecycleOf = (order: Order, t: (key: string) => string): LifecycleStep[] => {
  if (order.status === "cancelled")
    return [
      { key: "draft", label: t("orderStatus.draft"), state: "done" },
      { key: "cancelled", label: t("orderStatus.cancelled"), state: "done", tone: "red" },
    ];
  const confirmed = order.status === "confirmed";
  const delivered = order.deliveryStatus === "delivered";
  const invoiced = order.invoiceStatus === "invoiced";
  const step = (key: string, label: string, done: boolean, current: boolean): LifecycleStep => ({
    key,
    label,
    state: done ? "done" : current ? "current" : "ahead",
  });
  return [
    step("draft", t("orderStatus.draft"), confirmed, !confirmed),
    step("confirmed", t("orderStatus.confirmed"), confirmed, false),
    step("delivered", t("deliveryStatus.delivered"), delivered, confirmed && !delivered),
    step("invoiced", t("invoiceStatus.invoiced"), invoiced, confirmed && delivered && !invoiced),
    step("done", t("done"), order.done, false),
  ];
};

type Dialog =
  | "send"
  | "hold"
  | "cancel"
  | "cancelRemaining"
  | "delete"
  | "unlock"
  | "markDelivered"
  | "undoDelivered"
  | "releaseDrafts"
  | "shortfall"
  | null;

const OrderEditor = ({ order, access }: { order: Order; access: OrdersAccess }) => {
  const { t, money } = useOrdersFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const editor = useEditorState(order);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [dialogError, setDialogError] = useState<string>();
  const detailKey = orderQueryOptions(order.id).queryKey;
  const changes = useQuery(orderChangesQueryOptions(order.id));
  const documents = useQuery(orderDocumentsQueryOptions(order.id));
  const mailings = useQuery(orderMailingsQueryOptions(order.id));
  // Whether Send works now (O25), read under orders:access alone.
  const meta = useQuery(metaQueryOptions());
  const sendingOff = meta.data?.sendingAvailable === false;
  // Inventory keeps stock and the order names a site (O46): it reserves, and
  // its goods lines are delivered by delivery documents (O47).
  const stockKept = meta.data?.stockKept === true;
  const atSite = stockKept && order.siteId != null;
  // The lines short at confirm (insufficient_available), by their index.
  const [shortfall, setShortfall] = useState<readonly number[]>([]);

  const manage = access.canManageOrders;
  const confirmAllowed = access.canConfirmOrders;
  const draft = order.status === "draft";
  const confirmed = order.status === "confirmed";
  const cancelled = order.status === "cancelled";
  const editable = manage && (draft || (confirmed && !order.locked));
  const progress = order.lines.some((line) => line.qtyDelivered > 0 || line.qtyInvoiced > 0);
  const anythingDelivered = order.lines.some((line) => line.qtyDelivered > 0);
  const openToDeliver = order.lines.some(
    (line) => line.kind === "product" && (line.quantity ?? 0) - line.qtyCancelled - line.qtyDelivered > 0,
  );
  // A goods line at the site holds less than it has open: Reserve now (O46).
  // A kit line reserves nothing — its children do (manufacturing phase 2 M145).
  const kits = new Set(order.lines.flatMap((line) => (line.parentLineId == null ? [] : [line.parentLineId])));
  const unreserved = order.lines.some(
    (line) =>
      line.deliveryMethod === "delivery" && !kits.has(line.id) && remainingToDeliver(line) > (line.qtyReserved ?? 0),
  );
  const openToShip = order.lines.some((line) => line.deliveryMethod === "delivery" && remainingToDeliver(line) > 0);
  // What draft invoices hold of the order, and what is left to invoice on
  // order (O36): ordered − cancelled − invoiced − draft-allocated. A kit's
  // child line is invoiced as its kit (manufacturing phase 2 M145).
  const inDraftInvoice = order.lines.some((line) => line.qtyDraftAllocated > 0);
  const openToInvoice = order.lines.some(
    (line) =>
      line.kind === "product" &&
      line.parentLineId == null &&
      line.invoicePolicy === "order" &&
      (line.quantity ?? 0) - line.qtyCancelled - line.qtyInvoiced - line.qtyDraftAllocated > 0,
  );
  // The reason a settings holder overrides the order's block warnings with (O33).
  const [override, setOverride] = useState<string | null>(null);
  const close = () => {
    setDialog(null);
    setDialogError(undefined);
  };
  const common = { detailKey, showRefusal: editor.showRefusal, onDone: close };

  const save = useDocumentWrite({
    ...common,
    write: () =>
      updateOrder(order.id, {
        version: order.version,
        expectedUpdatedAt: order.updatedAt,
        ...headerInput(editor.header),
        siteId: editor.header.siteId,
        lines: editor.lines.map((line) => toLineInput(line, { quote: false })),
      }),
    done: t("orderSaved"),
    errorTitle: t("couldNotSave"),
  });
  const transition = useDocumentWrite({
    ...common,
    write: (name: OrderTransition) =>
      transitionOrder(order.id, name, order, name === "confirm" ? overrideOf(override) : {}),
    // An override is for the one confirm it was given with (O33).
    onDone: () => {
      setOverride(null);
      close();
    },
    errorTitle: t("couldNotChangeOrder"),
  });
  // Confirm without reserving (O46), offered when the confirm was short.
  const confirmUnreserved = useDocumentWrite({
    ...common,
    write: () => transitionOrder(order.id, "confirm", order, { ...overrideOf(override), reserve: false }),
    done: t("orderConfirmedUnreserved"),
    onDone: () => {
      setOverride(null);
      close();
    },
    errorTitle: t("couldNotChangeOrder"),
  });
  const reserve = useDocumentWrite({
    ...common,
    write: () => reserveOrder(order.id, order),
    done: t("orderReserved"),
    errorTitle: t("couldNotReserve"),
  });
  const releaseDrafts = useDocumentWrite({
    ...common,
    write: () => releaseDraftAllocations(order.id, order),
    done: t("draftAllocationsReleased"),
    errorTitle: t("couldNotChangeOrder"),
  });
  const hold = useDocumentWrite({
    ...common,
    write: (reason: string) => holdOrder(order.id, order, reason),
    done: t("orderHeld"),
    errorTitle: t("couldNotChangeOrder"),
  });
  // The quantities dialog says its own refusal and stays open to be corrected.
  const delivered = useMutation({
    mutationFn: ({ mode, lines }: { mode: "mark" | "undo"; lines: DeliveredLine[] }) =>
      (mode === "mark" ? markDelivered : undoDelivered)(order.id, order, lines),
    onSuccess: async (next, { mode }) => {
      queryClient.setQueryData(detailKey, next);
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (q) => q.queryKey[2] !== "detail",
      });
      notifications.show({
        color: "green",
        message: mode === "mark" ? t("deliveredRegistered") : t("deliveredUndone"),
      });
      close();
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        close();
        editor.showRefusal(error, t("couldNotChangeOrder"));
      } else setDialogError(refusalMessage(error, t));
    },
  });
  const send = useMutation({
    mutationFn: (input: Omit<Parameters<typeof sendOrder>[1], "expectedUpdatedAt">) =>
      sendOrder(order.id, { ...input, version: order.version, expectedUpdatedAt: order.updatedAt }),
    onSuccess: async (sent) => {
      queryClient.setQueryData(detailKey, sent);
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (q) => q.queryKey[2] !== "detail",
      });
      notifications.show({ color: "green", message: t("confirmationSent") });
      close();
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        close();
        editor.showRefusal(error, t("couldNotSend"));
      } else setDialogError(refusalMessage(error, t));
    },
  });
  const remove = useMutation({
    mutationFn: () => deleteOrder(order.id, order.updatedAt),
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: detailKey });
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "green", message: t("orderDeleted", { number: order.number }) });
      navigate({ to: SALES_ORDERS_ROUTE_PATH });
    },
    onError: (error) => editor.showRefusal(error, t("couldNotDelete")),
  });
  const run = (name: OrderTransition, done: string) =>
    transition.mutate(name, {
      onSuccess: () => notifications.show({ color: "green", message: done }),
      // Not enough at the site: say which lines, and offer to confirm without reserving (O46).
      onError: (error) => {
        if (name !== "confirm" || refusalCode(error) !== "insufficient_available") return;
        // A row is the editor's: a kit's short child is named on its kit (M145).
        setShortfall([...new Set(refusedLines(error).map((line) => line.index))]);
        setDialog("shortfall");
      },
    });

  const busy =
    save.isPending ||
    transition.isPending ||
    hold.isPending ||
    releaseDrafts.isPending ||
    reserve.isPending ||
    confirmUnreserved.isPending;
  const needsSave = editor.dirty ? t("saveFirst") : undefined;
  const invoiceBlocked = order.onHold ? t("heldTakesNoInvoice") : !openToInvoice ? t("nothingToInvoice") : needsSave;
  const title = t("orderNumber", { number: order.number });
  const stateButton = (
    label: string,
    onClick: () => void,
    options: { icon?: ReactNode; color?: string; variant?: string; disabledReason?: string } = {},
  ) => {
    const reason = options.disabledReason ?? needsSave;
    return (
      <Tooltip label={reason} disabled={!reason} withArrow>
        <Button
          leftSection={options.icon}
          color={options.color}
          variant={options.variant ?? "light"}
          disabled={Boolean(reason) || busy}
          onClick={onClick}
        >
          {label}
        </Button>
      </Tooltip>
    );
  };
  // The quote an accepted order came from (O20).
  const sourceQuoteId = order.sourceQuoteId;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("salesOrdersTitle"), to: SALES_ORDERS_ROUTE_PATH }, { label: title }]}
        title={
          <Group gap="sm">
            <span>{title}</span>
            {order.version > 1 && confirmed && (
              <Text span size="sm" c="dimmed" fw={500}>
                {t("versionNumber", { version: order.version })}
              </Text>
            )}
            <OrderStatusBadge status={order.status} />
            <OrderFlags done={order.done} onHold={order.onHold} locked={order.locked} />
          </Group>
        }
        description={
          <Group gap="xs" component="span">
            <span>{order.customer.name}</span>
            {confirmed && <DeliveryBadge status={order.deliveryStatus} />}
            {confirmed && <InvoiceBadge status={order.invoiceStatus} />}
          </Group>
        }
        actions={
          <Group gap="xs">
            {access.canCreateInvoices && confirmed && (
              <Tooltip label={invoiceBlocked} disabled={!invoiceBlocked} withArrow>
                <Button
                  variant="default"
                  leftSection={<IconFileInvoice size={16} />}
                  disabled={Boolean(invoiceBlocked)}
                  onClick={() => {
                    // The wizard allocates: the order is read again on return.
                    void queryClient.invalidateQueries({ queryKey: detailKey, refetchType: "none" });
                    navigate(invoiceFromOrderLinkOptions(order.id));
                  }}
                >
                  {t("invoiceOrder")}
                </Button>
              </Tooltip>
            )}
            {editable && (
              <Button
                leftSection={<IconDeviceFloppy size={16} />}
                disabled={!editor.dirty}
                loading={save.isPending}
                onClick={() => editor.checkLines() && save.mutate()}
                data-assist="orders.order.save"
              >
                {t("save")}
              </Button>
            )}
            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <Button
                  variant="default"
                  rightSection={<IconChevronDown size={14} />}
                  leftSection={<IconFileText size={16} />}
                >
                  {t("pdf")}
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item component="div" p={0} closeMenuOnClick={false}>
                  <PdfButton
                    url={orderPdfUrl(order.id)}
                    variant="subtle"
                    color="gray"
                    fullWidth
                    justify="flex-start"
                    size="sm"
                  >
                    {t("orderConfirmationPdf")}
                  </PdfButton>
                </Menu.Item>
                <Menu.Item component="div" p={0} closeMenuOnClick={false}>
                  <PdfButton
                    url={orderProformaUrl(order.id)}
                    variant="subtle"
                    color="gray"
                    fullWidth
                    justify="flex-start"
                    size="sm"
                  >
                    {t("proforma")}
                  </PdfButton>
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
            {(manage || confirmAllowed) && !cancelled && (
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <Button variant="default" aria-label={t("moreActions")} px="xs">
                    <IconDots size={16} />
                  </Button>
                </Menu.Target>
                <Menu.Dropdown>
                  {confirmAllowed && confirmed && !order.locked && (
                    <Menu.Item
                      leftSection={<IconLock size={14} />}
                      disabled={editor.dirty}
                      onClick={() => run("lock", t("orderLocked"))}
                    >
                      {t("lock")}
                    </Menu.Item>
                  )}
                  {confirmAllowed && order.locked && (
                    <Menu.Item leftSection={<IconLockOpen size={14} />} onClick={() => setDialog("unlock")}>
                      {t("unlock")}
                    </Menu.Item>
                  )}
                  {manage && confirmed && anythingDelivered && (
                    <Menu.Item
                      leftSection={<IconArrowBackUp size={14} />}
                      disabled={editor.dirty}
                      onClick={() => setDialog("undoDelivered")}
                    >
                      {t("undoDelivered")}
                    </Menu.Item>
                  )}
                  {manage && inDraftInvoice && (
                    <Menu.Item
                      leftSection={<IconArrowBackUp size={14} />}
                      disabled={editor.dirty}
                      onClick={() => setDialog("releaseDrafts")}
                    >
                      {t("releaseDraftAllocations")}
                    </Menu.Item>
                  )}
                  {manage && !progress && (
                    <Menu.Item
                      color="red"
                      leftSection={<IconX size={14} />}
                      disabled={editor.dirty}
                      onClick={() => setDialog("cancel")}
                    >
                      {t("cancelOrder")}
                    </Menu.Item>
                  )}
                  {manage && confirmed && progress && openToDeliver && (
                    <Menu.Item
                      color="red"
                      leftSection={<IconX size={14} />}
                      disabled={editor.dirty}
                      onClick={() => setDialog("cancelRemaining")}
                    >
                      {t("cancelRemaining")}
                    </Menu.Item>
                  )}
                  {manage && draft && (
                    <Menu.Item color="red" onClick={() => setDialog("delete")}>
                      {t("deleteOrder")}
                    </Menu.Item>
                  )}
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        }
      />
      <Lifecycle steps={lifecycleOf(order, t)} label={t("orderLifecycle")} />
      <WarningBanners
        warnings={order.warnings}
        customer={order.customer}
        access={access}
        blocking={confirmAllowed && draft}
        override={override}
        onOverride={setOverride}
      />
      {editor.conflict && <ConflictAlert onReload={() => void queryClient.refetchQueries({ queryKey: detailKey })} />}
      {order.onHold && (
        <Alert color="orange" icon={<IconPlayerPause size={16} />} title={t("onHoldTitle")}>
          {order.holdReason || t("onHoldNoReason")}
        </Alert>
      )}
      {order.locked && (
        <Alert color="grape" icon={<IconLock size={16} />}>
          {t("lockedHint")}
        </Alert>
      )}
      {sourceQuoteId != null && (
        <Text size="sm" c="dimmed">
          {t("fromQuote")}{" "}
          <Anchor size="sm" renderRoot={(props) => <Link {...quoteLinkOptions(sourceQuoteId)} {...props} />}>
            {t("openQuote")}
          </Anchor>
        </Text>
      )}
      {(manage || confirmAllowed) && !cancelled && (
        <Card withBorder padding="sm" data-testid="order-actions">
          <Group gap="xs" wrap="wrap">
            {draft && confirmAllowed && (
              <span data-assist="orders.order.confirm">
                {stateButton(t("confirm"), () => run("confirm", t("orderConfirmed")), {
                  icon: <IconCircleCheck size={16} />,
                  variant: "filled",
                })}
              </span>
            )}
            {manage &&
              confirmed &&
              openToDeliver &&
              stateButton(t("registerDelivered"), () => setDialog("markDelivered"), {
                icon: <IconTruckDelivery size={16} />,
                variant: "filled",
                color: "teal",
                disabledReason: order.onHold ? t("heldTakesNoDelivery") : undefined,
              })}
            {manage &&
              confirmed &&
              atSite &&
              unreserved &&
              stateButton(t("reserveNow"), () => reserve.mutate(), {
                icon: <IconPackage size={16} />,
              })}
            {manage &&
              confirmed &&
              stateButton(t("sendConfirmation"), () => setDialog("send"), {
                icon: <IconSend size={16} />,
                disabledReason: sendingOff ? t("sendingOff") : undefined,
              })}
            {manage &&
              (order.onHold
                ? stateButton(t("release"), () => run("release", t("orderReleased")), {
                    icon: <IconPlayerPlay size={16} />,
                  })
                : stateButton(t("hold"), () => setDialog("hold"), {
                    icon: <IconPlayerPause size={16} />,
                    color: "orange",
                  }))}
            {draft && !confirmAllowed && (
              <Text size="xs" c="dimmed">
                {t("confirmNeedsKey")}
              </Text>
            )}
          </Group>
        </Card>
      )}
      <HeaderCard
        kind="order"
        header={editor.header}
        onChange={editor.patchHeader}
        snapshot={order.customer}
        salesperson={order.salesperson}
        editable={{ draft: manage && draft, delivery: editable }}
        access={access}
        errors={editor.fieldErrors}
      />
      <PageSection>
        <Stack gap="md">
          <Group justify="space-between">
            <Title order={4}>{t("lines")}</Title>
            {confirmed && editable && (
              <Text size="xs" c="dimmed">
                {t("confirmedEditHint")}
              </Text>
            )}
          </Group>
          <LineGrid
            lines={editor.lines}
            onChange={editor.changeLines}
            mode="order"
            currency={editor.header.currency}
            pricesIncludeVat={editor.header.pricesIncludeVat}
            editable={editable}
            confirmed={confirmed}
            access={access}
            refused={editor.refused}
            onEstimate={editable ? () => estimateShipping({ orderId: order.id }) : undefined}
            kitChildren={kitChildrenOf(order.lines)}
          />
          <TotalsCard totals={order.totals} currency={order.currency} stale={editor.stale} />
          {order.origin.prepaidAmount != null && (
            <Text size="sm" ta="right" c="dimmed">
              {t("prepaid", { amount: money(order.origin.prepaidAmount, order.currency) })}
            </Text>
          )}
        </Stack>
      </PageSection>
      {confirmed && atSite && (
        <DeliveriesCard
          orderId={order.id}
          canCreate={access.canManageDeliveries}
          blocked={order.onHold ? t("heldTakesNoDelivery") : !openToShip ? t("nothingToShip") : needsSave}
        />
      )}
      {confirmed && atSite && <OrderShipmentsCard orderId={order.id} />}
      {!draft && <InvoicesCard invoices={order.invoices} canViewInvoices={access.canViewInvoices} />}
      {!draft && <ChangeLog changes={changes.data?.items ?? []} lines={order.lines} />}
      <DocumentsCard
        kind="order"
        documents={documents.data?.items}
        mailings={mailings.data?.items}
        pdfUrl={(version) => orderPdfUrl(order.id, version)}
      />

      {dialog === "send" && (
        <SendDialog
          kind="order"
          defaultRecipient={order.defaultRecipient}
          loading={send.isPending}
          error={dialogError}
          onSend={(input) => send.mutate(input)}
          onClose={close}
        />
      )}
      {dialog === "hold" && (
        <ReasonDialog
          title={t("holdOrder")}
          description={t("holdHint")}
          label={t("holdReason")}
          submitLabel={t("hold")}
          maxLength={200}
          loading={hold.isPending}
          onSubmit={(reason) => hold.mutate(reason)}
          onClose={close}
        />
      )}
      {dialog === "cancel" && (
        <ConfirmDialog
          title={t("cancelOrder")}
          confirmLabel={t("cancelOrder")}
          danger
          loading={transition.isPending}
          onConfirm={() => run("cancel", t("orderCancelled"))}
          onClose={close}
        >
          {t("cancelOrderHint")}
        </ConfirmDialog>
      )}
      {dialog === "cancelRemaining" && (
        <ConfirmDialog
          title={t("cancelRemaining")}
          confirmLabel={t("cancelRemaining")}
          danger
          loading={transition.isPending}
          onConfirm={() => run("cancel-remaining", t("remainingCancelled"))}
          onClose={close}
        >
          {t("cancelRemainingHint")}
        </ConfirmDialog>
      )}
      {dialog === "shortfall" && (
        <ConfirmDialog
          title={t("shortfallTitle")}
          confirmLabel={t("confirmWithoutReserving")}
          loading={confirmUnreserved.isPending}
          onConfirm={() => confirmUnreserved.mutate()}
          onClose={close}
        >
          <Stack gap="xs">
            <Text size="sm">{t("shortfallHint")}</Text>
            {shortfall.map((index) => {
              const shown = order.lines.filter((line) => line.parentLineId == null)[index];
              return (
                <Text size="sm" key={index} data-testid="shortfall-line">
                  {t("shortfallLine", {
                    position: index + 1,
                    name: shown?.name ?? shown?.sku ?? "",
                    available: shown?.available == null ? "—" : String(shown.available),
                  })}
                </Text>
              );
            })}
          </Stack>
        </ConfirmDialog>
      )}
      {dialog === "releaseDrafts" && (
        <ConfirmDialog
          title={t("releaseDraftAllocations")}
          confirmLabel={t("releaseDraftAllocations")}
          danger
          loading={releaseDrafts.isPending}
          onConfirm={() => releaseDrafts.mutate()}
          onClose={close}
        >
          {t("releaseDraftAllocationsHint")}
        </ConfirmDialog>
      )}
      {dialog === "unlock" && (
        <ConfirmDialog
          title={t("unlock")}
          confirmLabel={t("unlock")}
          loading={transition.isPending}
          onConfirm={() => run("unlock", t("orderUnlocked"))}
          onClose={close}
        >
          {t("unlockHint")}
        </ConfirmDialog>
      )}
      {dialog === "delete" && (
        <ConfirmDialog
          title={t("deleteOrder")}
          confirmLabel={t("deleteOrder")}
          danger
          loading={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={close}
        >
          {t("deleteOrderHint", { number: order.number })}
        </ConfirmDialog>
      )}
      {(dialog === "markDelivered" || dialog === "undoDelivered") && (
        <DeliveredDialog
          mode={dialog === "markDelivered" ? "mark" : "undo"}
          lines={order.lines}
          loading={delivered.isPending}
          error={dialogError}
          onSubmit={(lines) => delivered.mutate({ mode: dialog === "markDelivered" ? "mark" : "undo", lines })}
          onClose={close}
        />
      )}
    </Stack>
  );
};
