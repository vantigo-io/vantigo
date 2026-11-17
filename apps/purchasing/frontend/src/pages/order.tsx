import {
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Tabs,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useEffect, useRef, useState } from "react";
import {
  cancelPurchaseOrder,
  closePurchaseOrder,
  closePurchaseOrderLine,
  confirmPurchaseOrder,
  copyPurchaseOrder,
  deletePurchaseOrder,
  markPurchaseOrderSent,
  type PurchaseOrder,
  type PurchaseOrderLine,
  type PurchasingAddress,
  purchaseOrderDocumentUrl,
  purchaseOrderHistoryQueryOptions,
  purchaseOrderPdfUrl,
  purchaseOrderQueryOptions,
  purchasingMetaQueryOptions,
  reopenPurchaseOrder,
  sendPurchaseOrder,
  sendPurchaseOrderAgain,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY, refusalCode } from "../api/request";
import { ConfirmDialog, OrderStatusBadges, PdfButton } from "../components/common";
import { OrderEditor } from "../components/order-editor";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { hasWords, refusalMessage } from "../lib/errors";
import { newRequestId, usePurchasingFormat } from "../lib/format";
import { PURCHASE_ORDERS_ROUTE_PATH, purchaseOrderLinkOptions, supplierLinkOptions } from "../lib/routes";

const openStates = new Set(["sent", "partially_received"]);

/** A day picked in the calendar as the contract writes one. */
const dayOf = (value: string | Date | null): string | null =>
  value === null ? null : typeof value === "string" ? value.slice(0, 10) : value.toISOString().slice(0, 10);

/**
 * One purchase order (P15–P23): its header, its lines, the supplier's
 * confirmation and its history — the changes, the stored PDFs and the
 * mailings. Each action is offered by the order's state and the caller's
 * permission: *Send* (orders-send, and only when sending is set up) and
 * *Mark as sent* (orders-send) on a draft, *Delete draft* (orders-manage);
 * *Send again* and *Cancel order* (orders-send) on a sent order, *Cancel*
 * only while nothing is received; *Edit*, *Record confirmation*, *Close* and
 * *Close line* (orders-manage) on a sent or partially received one;
 * *Reopen* (orders-manage) on a closed one; *Copy* (orders-manage) on any;
 * and the PDF for everyone who reads the order.
 */
export const PurchaseOrderPage = ({
  orderId,
  access = noPurchasingAccess,
}: {
  orderId: number;
  access?: PurchasingAccess;
}) => {
  const { t } = usePurchasingFormat();
  const order = useQuery(purchaseOrderQueryOptions(orderId));
  const [editing, setEditing] = useState(false);
  if (order.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (order.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(order.error, t)}
      </Alert>
    );
  const o = order.data;
  const title = o.number ? t("orderTitle", { number: o.number }) : t("draftTitle");
  if (editing)
    return (
      <Stack gap="lg">
        <PageHeader title={title} description={o.supplierName} />
        <OrderEditor key={o.updatedAt} order={o} access={access} onDone={() => setEditing(false)} />
      </Stack>
    );
  return <OrderView order={o} title={title} access={access} onEdit={() => setEditing(true)} />;
};

/** A new draft (P15), with the supplier filled in when it was opened from a supplier's page. */
export const NewPurchaseOrderPage = ({
  supplierId,
  access = noPurchasingAccess,
}: {
  supplierId?: number;
  access?: PurchasingAccess;
}) => {
  const { t } = usePurchasingFormat();
  return (
    <Stack gap="lg">
      <PageHeader title={t("newOrderTitle")} />
      <OrderEditor supplierId={supplierId} access={access} />
    </Stack>
  );
};

type Dialog = "send" | "markSent" | "sendAgain" | "cancel" | "close" | "reopen" | "delete" | "confirm" | null;

const OrderView = ({
  order,
  title,
  access,
  onEdit,
}: {
  order: PurchaseOrder;
  title: string;
  access: PurchasingAccess;
  onEdit: () => void;
}) => {
  const { t, money, date, dateTime } = usePurchasingFormat();
  const meta = useQuery(purchasingMetaQueryOptions());
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [dialog, setDialog] = useState<Dialog>(null);
  const [closingLine, setClosingLine] = useState<PurchaseOrderLine | null>(null);
  const draft = order.status === "draft";
  const open = openStates.has(order.status);
  const nothingReceived = order.lines.every((l) => Number(l.received) === 0);
  const sendingAvailable = meta.data?.sendingAvailable === true;
  const done = async (message: string) => {
    await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
    setDialog(null);
    setClosingLine(null);
    notifications.show({ color: "green", message });
  };
  const failed = (error: unknown) => notifications.show({ color: "red", message: refusalMessage(error, t) });
  // A Send's or a Mark as sent's clientRequestId (A11) belongs to one
  // attempt: made when the dialog opens or its body changes, and the same on
  // a retry, so a retry after a lost answer replays the send, never repeats it.
  const attempt = useRef<{ key: string; id: string } | null>(null);
  useEffect(() => {
    if (dialog === "send" || dialog === "markSent") attempt.current = null;
  }, [dialog]);
  const requestIdFor = (body: unknown) => {
    const key = JSON.stringify(body);
    if (attempt.current?.key !== key) attempt.current = { key, id: newRequestId() };
    return attempt.current.id;
  };
  // The change is made and its mail queued (A19): the dialog closes and the
  // order shows as it now is, with a warning rather than an error.
  const mailQueued = async () => {
    await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
    setDialog(null);
    notifications.show({ color: "yellow", message: t("refusal.mail_failed") });
  };
  const action = useMutation({
    mutationFn: (run: () => Promise<unknown>) => run(),
    onError: async (error) => {
      if (refusalCode(error) === "mail_failed") return mailQueued();
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      failed(error);
    },
  });
  const run = (call: () => Promise<unknown>, message: string) =>
    action.mutate(call, {
      onSuccess: (result) => {
        const mailing = (result as { mailing?: { status?: string } | null } | undefined)?.mailing;
        if (mailing && mailing.status !== "sent") return mailQueued();
        return done(message);
      },
    });
  const copy = useMutation({
    mutationFn: () => copyPurchaseOrder(order.id),
    onSuccess: async (copied) => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("copied") });
      await navigate(purchaseOrderLinkOptions(copied.id));
    },
    onError: failed,
  });
  const remove = useMutation({
    mutationFn: () => deletePurchaseOrder(order.id, order.updatedAt),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("deleted") });
      await navigate({ to: PURCHASE_ORDERS_ROUTE_PATH });
    },
    onError: failed,
  });
  const by = (person?: { displayName: string } | null) => person?.displayName ?? t("someone");

  return (
    <Stack gap="lg">
      <PageHeader
        title={title}
        description={
          <Group gap="xs">
            <Anchor renderRoot={(props) => <Link {...supplierLinkOptions(order.supplierId)} {...props} />}>
              {order.supplierName}
            </Anchor>
            <OrderStatusBadges status={order.status} late={order.late} unconfirmed={order.unconfirmed} />
          </Group>
        }
      />
      <Group gap="xs" data-testid="order-actions">
        {(draft || open) && access.canManageOrders && (
          <Button variant="default" onClick={onEdit} data-assist="purchasing.order.edit">
            {t("editOrder")}
          </Button>
        )}
        {draft && access.canSendOrders && sendingAvailable && (
          <Button onClick={() => setDialog("send")} data-assist="purchasing.order.send">
            {t("send")}
          </Button>
        )}
        {draft && access.canSendOrders && (
          <Button variant={sendingAvailable ? "default" : "filled"} onClick={() => setDialog("markSent")}>
            {t("markSent")}
          </Button>
        )}
        {open && access.canManageOrders && (
          <Button variant="default" onClick={() => setDialog("confirm")}>
            {t("recordConfirmation")}
          </Button>
        )}
        {open && access.canSendOrders && (
          <Button variant="default" onClick={() => setDialog("sendAgain")}>
            {t("sendAgain")}
          </Button>
        )}
        {open && access.canManageOrders && (
          <Button variant="default" onClick={() => setDialog("close")}>
            {t("closeOrder")}
          </Button>
        )}
        {order.status === "sent" && nothingReceived && access.canSendOrders && (
          <Button variant="default" color="red" onClick={() => setDialog("cancel")}>
            {t("cancelOrder")}
          </Button>
        )}
        {order.status === "closed" && access.canManageOrders && (
          <Button variant="default" onClick={() => setDialog("reopen")}>
            {t("reopen")}
          </Button>
        )}
        {access.canManageOrders && (
          <Button variant="default" loading={copy.isPending} onClick={() => copy.mutate()}>
            {t("copy")}
          </Button>
        )}
        <PdfButton variant="default" leftSection={<IconDownload size={16} />} url={purchaseOrderPdfUrl(order.id)}>
          {draft ? t("previewPdf") : t("downloadPdf")}
        </PdfButton>
        {draft && access.canManageOrders && (
          <Button variant="subtle" color="red" onClick={() => setDialog("delete")}>
            {t("deleteDraft")}
          </Button>
        )}
      </Group>
      {!order.supplierActive && (draft || open) && (
        <Alert color="yellow" variant="light">
          {t("supplierInactiveWarning")}
        </Alert>
      )}
      {order.lastMailingStatus === "failed" && (
        <Alert color="orange" variant="light">
          {t("mailFailedWarning")}
        </Alert>
      )}
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
          <Field label={t("orderDate")} value={date(order.orderDate)} />
          <Field label={t("expectedOn")} value={order.expectedOn ? date(order.expectedOn) : "—"} />
          <Field label={t("site")} value={order.siteName} />
          <Field label={t("buyer")} value={order.buyer?.displayName ?? "—"} />
          <Field label={t("supplierContact")} value={order.supplierContact || "—"} />
          <Field label={t("currency")} value={order.currency} />
          <Field label={t("language")} value={t(`languageName.${order.language}`)} />
          <Field label={t("supplierAddress")} value={addressText(order.supplierAddress) || "—"} />
          <Field
            label={t("deliveryAddress")}
            value={addressText(order.deliveryAddress) || "—"}
            hint={draft ? t("deliveryAddressLive") : undefined}
          />
          {order.sentAt && (
            <Field
              label={t("status")}
              value={t(order.sendMethod === "manual" ? "sentManually" : "sentAt", {
                when: dateTime(order.sentAt),
                who: by(order.sentBy),
              })}
              hint={order.version > 0 ? t("revisionVersion", { version: order.version }) : undefined}
            />
          )}
          {order.note && <Field label={t("note")} value={order.note} />}
          {order.internalNote && <Field label={t("internalNote")} value={order.internalNote} />}
        </SimpleGrid>
      </PageSection>
      {!draft && (
        <PageSection>
          <Stack gap={4}>
            <Text fw={600}>{t("confirmation")}</Text>
            {order.supplierReference || order.confirmedOn ? (
              <Text size="sm" data-testid="confirmation">
                {[order.supplierReference, order.confirmedOn ? date(order.confirmedOn) : null]
                  .filter(Boolean)
                  .join(" · ")}
              </Text>
            ) : (
              <Text size="sm" c="dimmed">
                {t("noConfirmation")}
              </Text>
            )}
          </Stack>
        </PageSection>
      )}
      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("lines")}</Title>
          {order.lines.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noLines")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={900}>
              <Table striped data-testid="order-lines">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("product")}</Table.Th>
                    <Table.Th>{t("supplierItemNumber")}</Table.Th>
                    <Table.Th ta="right">{t("quantity")}</Table.Th>
                    <Table.Th ta="right">{t("unitPrice")}</Table.Th>
                    <Table.Th ta="right">{t("discount")}</Table.Th>
                    <Table.Th>{t("expectedOn")}</Table.Th>
                    <Table.Th ta="right">{t("received")}</Table.Th>
                    <Table.Th ta="right">{t("lineNet")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {order.lines.map((line) => (
                    <LineRow
                      key={line.id}
                      line={line}
                      currency={order.currency}
                      canClose={open && access.canManageOrders && !line.closedAt}
                      onClose={() => setClosingLine(line)}
                    />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
          <Group justify="space-between">
            <Text size="xs" c="dimmed">
              {t("amountsExcludeVat")}
            </Text>
            <Text fw={600}>
              {t("netTotal")}: {money(order.netTotal, order.currency)}
            </Text>
          </Group>
        </Stack>
      </PageSection>
      {!draft && <HistoryCard order={order} />}

      {dialog === "send" && (
        <SendDialog
          order={order}
          again={false}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onSend={(input) => {
            const clientRequestId = requestIdFor({ send: input, expectedUpdatedAt: order.updatedAt });
            run(
              () => sendPurchaseOrder(order.id, { ...input, expectedUpdatedAt: order.updatedAt, clientRequestId }),
              t("orderSent"),
            );
          }}
        />
      )}
      {dialog === "sendAgain" && (
        <SendDialog
          order={order}
          again
          sendingAvailable={sendingAvailable}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onSend={(input, mail) =>
            run(
              () =>
                sendPurchaseOrderAgain(order.id, {
                  ...(mail ? input : {}),
                  mail,
                  expectedUpdatedAt: order.updatedAt,
                }),
              t("orderSentAgain"),
            )
          }
        />
      )}
      {dialog === "markSent" && (
        <ConfirmDialog
          title={t("markSent")}
          body={t("markSentConfirm")}
          confirm={t("markSent")}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onConfirm={() => {
            const clientRequestId = requestIdFor({ markSent: true, expectedUpdatedAt: order.updatedAt });
            run(() => markPurchaseOrderSent(order.id, order.updatedAt, clientRequestId), t("orderMarkedSent"));
          }}
        />
      )}
      {dialog === "cancel" && (
        <CancelDialog
          order={order}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onCancel={(notifySupplier, message) =>
            run(
              () =>
                cancelPurchaseOrder(order.id, {
                  expectedUpdatedAt: order.updatedAt,
                  notifySupplier,
                  ...(message.trim() ? { message: message.trim() } : {}),
                }),
              t("orderCancelled"),
            )
          }
        />
      )}
      {dialog === "close" && (
        <ConfirmDialog
          title={t("closeOrder")}
          body={t("closeOrderConfirm")}
          confirm={t("closeOrder")}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onConfirm={() => run(() => closePurchaseOrder(order.id, order.updatedAt), t("orderClosed"))}
        />
      )}
      {dialog === "reopen" && (
        <ConfirmDialog
          title={t("reopen")}
          confirm={t("reopen")}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onConfirm={() => run(() => reopenPurchaseOrder(order.id, order.updatedAt), t("orderReopened"))}
        />
      )}
      {dialog === "delete" && (
        <ConfirmDialog
          title={t("deleteDraft")}
          body={t("deleteDraftConfirm")}
          confirm={t("deleteDraft")}
          color="red"
          loading={remove.isPending}
          onClose={() => setDialog(null)}
          onConfirm={() => remove.mutate()}
        />
      )}
      {dialog === "confirm" && (
        <ConfirmationDialog
          order={order}
          loading={action.isPending}
          onClose={() => setDialog(null)}
          onSave={(input) =>
            run(
              () => confirmPurchaseOrder(order.id, { ...input, expectedUpdatedAt: order.updatedAt }),
              t("confirmationRecorded"),
            )
          }
        />
      )}
      {closingLine && (
        <ConfirmDialog
          title={t("closeLine")}
          body={t("closeLineConfirm")}
          confirm={t("closeLine")}
          loading={action.isPending}
          onClose={() => setClosingLine(null)}
          onConfirm={() =>
            run(() => closePurchaseOrderLine(order.id, closingLine.id, order.updatedAt), t("lineWasClosed"))
          }
        />
      )}
    </Stack>
  );
};

const addressText = (a: PurchasingAddress) =>
  [a.name, a.line1, a.line2, [a.postalCode, a.city].filter(Boolean).join(" "), a.country].filter(Boolean).join(", ");

const Field = ({ label, value, hint }: { label: string; value: string; hint?: string }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm" style={{ whiteSpace: "pre-line" }}>
      {value}
    </Text>
    {hint && (
      <Text size="xs" c="dimmed">
        {hint}
      </Text>
    )}
  </Stack>
);

const LineRow = ({
  line,
  currency,
  canClose,
  onClose,
}: {
  line: PurchaseOrderLine;
  currency: string;
  canClose: boolean;
  onClose: () => void;
}) => {
  const { t, money, unitPrice, quantity, date } = usePurchasingFormat();
  const unit = line.packagingName ?? line.unit;
  return (
    <Table.Tr>
      <Table.Td>
        <Text size="sm">{line.kind === "text" ? line.description : `${line.sku} · ${line.name}`}</Text>
        {line.warnings.map((w) => (
          <Text key={w} size="xs" c="orange" data-testid="line-warning">
            {t(`warning.${w}.short`)}
          </Text>
        ))}
        {line.closedAt && (
          <Badge size="xs" variant="light" color="gray">
            {t("lineClosed")}
          </Badge>
        )}
      </Table.Td>
      <Table.Td>{line.supplierItemNumber ?? ""}</Table.Td>
      <Table.Td ta="right">{`${quantity(line.orderQuantity)} ${unit}`}</Table.Td>
      <Table.Td ta="right">{unitPrice(line.unitPrice, currency)}</Table.Td>
      <Table.Td ta="right">{Number(line.discountPercent) ? `${quantity(line.discountPercent)} %` : ""}</Table.Td>
      <Table.Td>
        {line.expectedOn ? date(line.expectedOn) : ""}
        {line.confirmedOn && (
          <Text size="xs" c="dimmed">
            {`${t("confirmedOn")}: ${date(line.confirmedOn)}`}
          </Text>
        )}
      </Table.Td>
      <Table.Td ta="right">
        {line.kind === "variant" && line.productType === "Goods"
          ? `${quantity(line.received)} / ${quantity(line.quantity)}`
          : ""}
      </Table.Td>
      <Table.Td ta="right">{money(line.lineNet, currency)}</Table.Td>
      <Table.Td>
        {canClose && (
          <Button size="compact-xs" variant="subtle" onClick={onClose}>
            {t("closeLine")}
          </Button>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

/** The addresses typed into a field, comma or space separated. */
const addresses = (text: string) =>
  text
    .split(/[,;\s]+/)
    .map((a) => a.trim())
    .filter(Boolean);

const SendDialog = ({
  order,
  again,
  sendingAvailable = true,
  loading,
  onClose,
  onSend,
}: {
  order: PurchaseOrder;
  again: boolean;
  sendingAvailable?: boolean;
  loading: boolean;
  onClose: () => void;
  onSend: (input: { recipient?: string; cc?: string[]; subject?: string; message?: string }, mail: boolean) => void;
}) => {
  const { t } = usePurchasingFormat();
  const [recipient, setRecipient] = useState(order.supplierEmail ?? "");
  const [cc, setCc] = useState("");
  const [subject, setSubject] = useState("");
  const [message, setMessage] = useState("");
  const [mail, setMail] = useState(sendingAvailable);
  const showMail = !again || mail;
  return (
    <Modal opened onClose={onClose} title={again ? t("sendAgainTitle") : t("sendTitle")} size="lg">
      <Stack>
        {again && (
          <Checkbox
            label={t("mailRevised")}
            checked={mail}
            disabled={!sendingAvailable}
            onChange={(e) => setMail(e.currentTarget.checked)}
          />
        )}
        {again && !sendingAvailable && (
          <Text size="sm" c="dimmed">
            {t("sendingUnavailable")}
          </Text>
        )}
        {showMail && (
          <>
            <TextInput
              label={t("recipient")}
              description={t("recipientHint")}
              type="email"
              value={recipient}
              onChange={(e) => setRecipient(e.currentTarget.value)}
            />
            <TextInput
              label={t("cc")}
              description={t("ccHint")}
              value={cc}
              onChange={(e) => setCc(e.currentTarget.value)}
            />
            <TextInput
              label={t("subject")}
              maxLength={300}
              value={subject}
              onChange={(e) => setSubject(e.currentTarget.value)}
            />
            <Textarea
              label={t("message")}
              autosize
              minRows={3}
              maxLength={4000}
              value={message}
              onChange={(e) => setMessage(e.currentTarget.value)}
            />
          </>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            loading={loading}
            onClick={() =>
              onSend(
                {
                  ...(recipient.trim() ? { recipient: recipient.trim() } : {}),
                  ...(addresses(cc).length ? { cc: addresses(cc) } : {}),
                  ...(subject.trim() ? { subject: subject.trim() } : {}),
                  ...(message.trim() ? { message: message.trim() } : {}),
                },
                mail,
              )
            }
          >
            {again ? t("sendAgain") : t("send")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

const CancelDialog = ({
  order,
  loading,
  onClose,
  onCancel,
}: {
  order: PurchaseOrder;
  loading: boolean;
  onClose: () => void;
  onCancel: (notifySupplier: boolean, message: string) => void;
}) => {
  const { t } = usePurchasingFormat();
  const mailed = order.sendMethod === "mail";
  const [notify, setNotify] = useState(mailed);
  const [message, setMessage] = useState("");
  return (
    <Modal opened onClose={onClose} title={t("cancelTitle")}>
      <Stack>
        <Text size="sm">{t("cancelHint")}</Text>
        {mailed && (
          <Checkbox label={t("notifySupplier")} checked={notify} onChange={(e) => setNotify(e.currentTarget.checked)} />
        )}
        {mailed && notify && (
          <Textarea
            label={t("message")}
            autosize
            minRows={2}
            maxLength={4000}
            value={message}
            onChange={(e) => setMessage(e.currentTarget.value)}
          />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("keepOrder")}
          </Button>
          <Button color="red" loading={loading} onClick={() => onCancel(mailed && notify, message)}>
            {t("cancelOrder")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

const ConfirmationDialog = ({
  order,
  loading,
  onClose,
  onSave,
}: {
  order: PurchaseOrder;
  loading: boolean;
  onClose: () => void;
  onSave: (input: {
    supplierReference: string | null;
    confirmedOn: string | null;
    lines: { lineId: number; confirmedOn: string | null }[];
  }) => void;
}) => {
  const { t } = usePurchasingFormat();
  const [reference, setReference] = useState(order.supplierReference ?? "");
  const [confirmedOn, setConfirmedOn] = useState<string | null>(order.confirmedOn ?? null);
  const [lineDays, setLineDays] = useState<Record<number, string | null>>(() =>
    Object.fromEntries(order.lines.map((l) => [l.id, l.confirmedOn ?? null])),
  );
  const openLines = order.lines.filter((l) => !l.closedAt);
  return (
    <Modal opened onClose={onClose} title={t("recordConfirmation")} size="lg">
      <Stack>
        <SimpleGrid cols={2}>
          <TextInput
            label={t("supplierReference")}
            maxLength={100}
            value={reference}
            onChange={(e) => setReference(e.currentTarget.value)}
          />
          <DateInput
            label={t("confirmedOn")}
            valueFormat="YYYY-MM-DD"
            clearable
            value={confirmedOn}
            onChange={(v) => setConfirmedOn(dayOf(v))}
          />
        </SimpleGrid>
        {openLines.length > 0 && (
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              {t("confirmationLines")}
            </Text>
            {openLines.map((l) => (
              <Group key={l.id} justify="space-between" wrap="nowrap">
                <Text size="sm">{l.kind === "text" ? l.description : `${l.sku} · ${l.name}`}</Text>
                <DateInput
                  size="xs"
                  w={140}
                  aria-label={`${t("confirmedOn")} ${l.position}`}
                  valueFormat="YYYY-MM-DD"
                  clearable
                  value={lineDays[l.id] ?? null}
                  onChange={(v) => setLineDays((d) => ({ ...d, [l.id]: dayOf(v) }))}
                />
              </Group>
            ))}
          </Stack>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            loading={loading}
            onClick={() =>
              onSave({
                supplierReference: reference.trim() || null,
                confirmedOn,
                lines: openLines.map((l) => ({ lineId: l.id, confirmedOn: lineDays[l.id] ?? null })),
              })
            }
          >
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** The order's history (P18, P21): its changes, its stored PDFs and its mailings, newest first. */
const HistoryCard = ({ order }: { order: PurchaseOrder }) => {
  const { t, dateTime } = usePurchasingFormat();
  const history = useQuery(purchaseOrderHistoryQueryOptions(order.id));
  const fieldName = (field: string) => (hasWords(`field.${field}`) ? t(`field.${field}`) : field);
  return (
    <PageSection>
      <Stack gap="sm">
        <Title order={4}>{t("history")}</Title>
        {history.isPending && <ContentSkeleton rows={3} rowHeight={32} />}
        {history.isError && (
          <Alert color="red" title={t("couldNotLoad")}>
            {refusalMessage(history.error, t)}
          </Alert>
        )}
        {history.data && (
          <Tabs defaultValue="changes">
            <Tabs.List>
              <Tabs.Tab value="changes">{t("changes")}</Tabs.Tab>
              <Tabs.Tab value="documents">{t("documents")}</Tabs.Tab>
              <Tabs.Tab value="mailings">{t("mailings")}</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="changes" pt="sm">
              {history.data.changes.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noChanges")}
                </Text>
              ) : (
                <Stack gap="xs" data-testid="order-changes">
                  {history.data.changes.map((change) => (
                    <Stack key={change.id} gap={0}>
                      <Text size="xs" c="dimmed">
                        {t("changeBy", {
                          who: change.actor?.displayName ?? t("someone"),
                          when: dateTime(change.at),
                        })}
                      </Text>
                      {change.actions.map((a, i) => (
                        <Text key={`${change.id}-${i}`} size="sm">
                          {hasWords(`changeAction.${a.action}`) ? t(`changeAction.${a.action}`) : a.action}
                          {a.field &&
                            ` — ${t("changeFrom", { field: fieldName(a.field), from: a.from || "—", to: a.to || "—" })}`}
                        </Text>
                      ))}
                    </Stack>
                  ))}
                </Stack>
              )}
            </Tabs.Panel>
            <Tabs.Panel value="documents" pt="sm">
              {history.data.documents.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noDocuments")}
                </Text>
              ) : (
                <Table data-testid="order-documents">
                  <Table.Tbody>
                    {history.data.documents.map((d) => (
                      <Table.Tr key={d.id}>
                        <Table.Td>{t(`documentKind.${d.kind}`)}</Table.Td>
                        <Table.Td>{t("revisionVersion", { version: d.version })}</Table.Td>
                        <Table.Td>{dateTime(d.createdAt)}</Table.Td>
                        <Table.Td>
                          {d.erased ? (
                            <Text size="sm" c="dimmed">
                              {t("erased")}
                            </Text>
                          ) : (
                            <PdfButton
                              size="compact-xs"
                              variant="subtle"
                              url={purchaseOrderDocumentUrl(order.id, d.id)}
                            >
                              {t("pdf")}
                            </PdfButton>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Tabs.Panel>
            <Tabs.Panel value="mailings" pt="sm">
              {history.data.mailings.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noMailings")}
                </Text>
              ) : (
                <Table data-testid="order-mailings">
                  <Table.Tbody>
                    {history.data.mailings.map((m) => (
                      <Table.Tr key={m.id}>
                        <Table.Td>{t(`documentKind.${m.kind}`)}</Table.Td>
                        <Table.Td>{m.recipient}</Table.Td>
                        <Table.Td>
                          <Badge
                            variant="light"
                            color={m.status === "sent" ? "green" : m.status === "failed" ? "red" : "gray"}
                          >
                            {t(`mailingStatus.${m.status}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>{dateTime(m.sentAt ?? m.createdAt)}</Table.Td>
                        <Table.Td>
                          {m.lastError && m.status !== "sent" ? (
                            <Stack gap={0}>
                              <Text size="xs">
                                {m.lastError.startsWith("Superseded")
                                  ? t("mailingSuperseded")
                                  : m.status === "failed"
                                    ? t("mailingFailedGeneric")
                                    : t("mailingQueuedGeneric")}
                              </Text>
                              <Text size="xs" c="dimmed">
                                {m.lastError}
                              </Text>
                            </Stack>
                          ) : (
                            <Text size="xs" c="dimmed">
                              {t("attempts", { count: m.attempts })}
                            </Text>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Tabs.Panel>
          </Tabs>
        )}
      </Stack>
    </PageSection>
  );
};
