import {
  ActionIcon,
  Alert,
  Anchor,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Modal,
  NumberInput,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck, IconDownload, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useEffect, useRef, useState } from "react";
import {
  cancelPaymentRequest,
  createPaymentRequest,
  paymentRequestQueryOptions,
  readPaymentRequest,
  recoveryOf,
} from "../api/attempts";
import { type CustomerAddress, createCustomerWithAddress, customerInvoiceAddressQueryOptions } from "../api/customers";
import { type InvoiceDocument, pdfUrl } from "../api/invoices";
import { type InvoicesMeta, invoicesMetaQueryOptions } from "../api/meta";
import { postQuickInvoice, type QuickInvoiceAnswer, type QuickInvoiceInput } from "../api/quick";
import { ApiValidationError } from "../api/request";
import { CustomerPicker } from "../components/customer-picker";
import { PdfButton } from "../components/pdf-button";
import "../i18n";
import { fieldRefusals, refusalCode, refusalMessage, refusalProblem, refusalWords } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";
import { QrScreen } from "./-qr-screen";

export interface QuickInvoicePageProps {
  /** Whether the caller holds `customers:view`, which the customer picker and the address read need. */
  canViewCustomers: boolean;
  /** Whether the caller may make a customer and its address (`customers:create` and `customers:update`). */
  canCreateCustomers?: boolean;
}

/** The most lines a quick invoice takes (D17). */
const MAX_LINES = 10;

/** A line as the screen holds it. */
interface QuickLine {
  key: string;
  description: string;
  quantity: number | string;
  unit: string;
  unitPrice: number | string;
  vatCodeId: number | null;
}

let lineKeys = 0;
const newLine = (vatCodeId: number | null): QuickLine => ({
  key: `quick-line-${++lineKeys}`,
  description: "",
  quantity: 1,
  unit: "",
  unitPrice: 0,
  vatCodeId,
});

const numberOf = (v: number | string): number => {
  if (typeof v === "number") return v;
  const parsed = Number(v.replace(",", ".").trim());
  return Number.isFinite(parsed) ? parsed : 0;
};

/** The fields of the body the screen has an input for; a 400 naming another is said beside the buttons. */
const quickInputs = /^(customerId|yourReference|lines\[\d+\]\.(description|quantity|unit|unitPrice|vatCodeId))$/;

/** Where the payment on site stands once the invoice is issued. */
type PaymentStep =
  | { kind: "asking" }
  | { kind: "qr"; attemptId: number; qrSvg?: string; notice?: string }
  | {
      kind: "refused";
      title: string;
      words: string;
      customerLink?: boolean;
      /** A request still open that may be cancelled and made again. */
      staleAttemptId?: number;
      /** No request is open: a new one may be asked for. */
      offerNew?: boolean;
    };

/** Why the last issue did not land: a refusal in words, or an answer that never came back. */
type Failure = { kind: "lost" } | { kind: "refused"; words: string; customerLink: boolean };

const addressLine = (a: CustomerAddress) =>
  [a.line1, a.line2, [a.postalCode, a.city].filter(Boolean).join(" "), a.country].filter(Boolean).join(", ");

/**
 * The quick invoice (invoices payments and reminders design D17, D22): one
 * screen for a tablet, gated on meta's `canQuickInvoice` (`invoices:create`
 * and `invoices:issue`). The customer — searched, or made here when the caller
 * may — with its address shown; one to ten lines; "Issue", and "Issue and take
 * payment with Vipps" only while payment on site is available, beside the
 * one-line kontantsalg reminder and only for a customer with an address.
 *
 * Every submission carries a request id the screen makes once and keeps until
 * the form changes, so sending again after a lost answer — "Try again" —
 * answers the invoice the first issued and never makes a second. The issued
 * invoice is offered as a PDF download; it is shown on screen ("Show the PDF")
 * only while payment on site is available, since showing a customer on site
 * what they can pay is a cash sale (D15, I21). The screen never makes or shows
 * a pay link. A payment request is shown with the invoice page's QR screen; a
 * replay without its QR, or a request already open, is read first: a waiting
 * one's QR is shown again, an approved one is followed without a cancel, and
 * one whose QR is gone may be cancelled and made again.
 */
export const QuickInvoicePage = ({ canViewCustomers, canCreateCustomers = false }: QuickInvoicePageProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("quick.title")} description={t("quick.description")} />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {meta.data && !meta.data.capabilities.canQuickInvoice && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("quick.notAllowedTitle")}>
          {t("quick.notAllowed")}
        </Alert>
      )}
      {meta.data?.capabilities.canQuickInvoice && !canViewCustomers && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("quick.notAllowedTitle")}>
          {t("quick.needsCustomers")}
        </Alert>
      )}
      {meta.data?.capabilities.canQuickInvoice && canViewCustomers && (
        <QuickInvoice meta={meta.data} canCreateCustomers={canCreateCustomers} />
      )}
    </Stack>
  );
};

const QuickInvoice = ({ meta, canCreateCustomers }: { meta: InvoicesMeta; canCreateCustomers: boolean }) => {
  const { t, date } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const defaultVat = meta.vatCodes[0]?.id ?? null;
  const onSite = meta.capabilities.canTakeOnSitePayment;

  const [customerId, setCustomerId] = useState<number | null>(null);
  const [customerName, setCustomerName] = useState<string | undefined>();
  const [yourReference, setYourReference] = useState("");
  const [lines, setLines] = useState<QuickLine[]>(() => [newLine(defaultVat)]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure | null>(null);
  const [issued, setIssued] = useState<InvoiceDocument | null>(null);
  const [payment, setPayment] = useState<PaymentStep | null>(null);
  const [creatingCustomer, setCreatingCustomer] = useState(false);

  // The request id: made at the first submission and kept for every retry
  // of the same form; any change to the form drops it, so the next
  // submission is a new quick invoice.
  const requestId = useRef<string | null>(null);
  // Whether the screen is still here, for the payment request's async steps.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const step = (next: PaymentStep | null) => {
    if (alive.current) setPayment(next);
  };

  const changed = () => {
    requestId.current = null;
  };

  const address = useQuery({
    ...customerInvoiceAddressQueryOptions(customerId ?? 0),
    enabled: customerId !== null,
  });
  const hasAddress = customerId !== null && Boolean(address.data);

  const body = (payOnSite: boolean): QuickInvoiceInput => ({
    requestId: requestId.current ?? undefined,
    customerId: customerId ?? 0,
    lines: lines.map((l) => ({
      description: l.description,
      quantity: numberOf(l.quantity),
      ...(l.unit.trim() ? { unit: l.unit.trim() } : {}),
      unitPrice: numberOf(l.unitPrice),
      vatCodeId: l.vatCodeId ?? 0,
    })),
    ...(yourReference.trim() ? { yourReference: yourReference.trim() } : {}),
    payOnSite,
  });

  /** A request already open, read: shown again, followed, or offered to be cancelled — never guessed. */
  const recover = async (invoiceId: number, attemptId: number) => {
    const open = await readPaymentRequest(invoiceId, attemptId).catch(() => undefined);
    const recovery = recoveryOf(open);
    if (recovery === "reshow") {
      step({ kind: "qr", attemptId, qrSvg: open?.qrSvg, notice: t("takePayment.activeShownAgain") });
    } else if (recovery === "follow") {
      // Seeded, so the QR screen starts on the state read and never offers a cancel meanwhile.
      queryClient.setQueryData(paymentRequestQueryOptions(invoiceId, attemptId).queryKey, open);
      step({ kind: "qr", attemptId });
    } else if (recovery === "cancel") {
      step({
        kind: "refused",
        title: t("takePayment.couldNot"),
        words: t("takePayment.activeNotShowable"),
        staleAttemptId: attemptId,
      });
    } else {
      step({ kind: "refused", title: t("quick.paymentNotOpen"), words: "", offerNew: true });
    }
  };

  /** Asks Vipps for a new on-site request of the issued invoice. */
  const ask = async (invoice: InvoiceDocument) => {
    step({ kind: "asking" });
    try {
      const made = await createPaymentRequest(invoice.id);
      step({ kind: "qr", attemptId: made.attemptId, qrSvg: made.qrSvg });
    } catch (error) {
      const attemptId = refusalProblem(error).attemptId;
      if (refusalCode(error) === "payment_request_active" && typeof attemptId === "number") {
        await recover(invoice.id, attemptId);
        return;
      }
      step({
        kind: "refused",
        title: t("takePayment.couldNot"),
        words: refusalMessage(error, t, date),
        customerLink: refusalCode(error) === "buyer_identity_missing",
      });
    }
  };

  const cancelAndRetry = async (invoice: InvoiceDocument, attemptId: number) => {
    step({ kind: "asking" });
    try {
      await cancelPaymentRequest(invoice.id, attemptId);
    } catch (error) {
      step({
        kind: "refused",
        title: t("takePayment.couldNot"),
        words:
          refusalCode(error) === "payment_request_active" ? t("takePayment.parked") : refusalMessage(error, t, date),
      });
      return;
    }
    if (alive.current) await ask(invoice);
  };

  /** What the issue's answer says of the payment on site, when one was asked for. */
  const followPayment = async (answer: QuickInvoiceAnswer) => {
    const { invoice, paymentRequest, paymentRequestError } = answer;
    if (paymentRequest) {
      if (paymentRequest.qrSvg) step({ kind: "qr", attemptId: paymentRequest.attemptId, qrSvg: paymentRequest.qrSvg });
      else await recover(invoice.id, paymentRequest.attemptId);
      return;
    }
    if (paymentRequestError) {
      const code = paymentRequestError.code;
      const attemptId = (paymentRequestError as { attemptId?: unknown }).attemptId;
      if (code === "payment_request_active" && typeof attemptId === "number") {
        await recover(invoice.id, attemptId);
        return;
      }
      step({
        kind: "refused",
        title: t("quick.paymentNotMade"),
        words: code ? refusalWords(code, t) : t("quick.paymentFailedUnknown"),
        customerLink: code === "buyer_identity_missing",
      });
      return;
    }
    // A replay finds no request open: none was made, or it is over.
    step({ kind: "refused", title: t("quick.paymentNotOpen"), words: "", offerNew: true });
  };

  const issue = useMutation({
    mutationFn: postQuickInvoice,
    onMutate: () => {
      setFailure(null);
      setErrors({});
    },
    onSuccess: async (answer, sent) => {
      setIssued(answer.invoice);
      if (sent.payOnSite) await followPayment(answer);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => {
          if (!quickInputs.test(field)) return false;
          const index = /^lines\[(\d+)\]/.exec(field)?.[1];
          return index === undefined || Number(index) < lines.length;
        });
        setErrors(onInputs);
        if (elsewhere.length > 0) setFailure({ kind: "refused", words: elsewhere.join(" "), customerLink: false });
        return;
      }
      const status = (error as { status?: unknown } | null)?.status;
      // No answer at all, or a gateway's without a code: the invoice may be issued. Sent again with the same id.
      if (typeof status !== "number" || (status >= 500 && !refusalCode(error))) {
        setFailure({ kind: "lost" });
        return;
      }
      const code = refusalCode(error);
      setFailure({
        kind: "refused",
        words: refusalMessage(error, t, date),
        customerLink: code === "buyer_incomplete" || code === "buyer_identity_missing",
      });
    },
  });

  const submit = (payOnSite: boolean) => {
    if (issue.isPending) return;
    requestId.current ??= crypto.randomUUID();
    issue.mutate(body(payOnSite));
  };

  const reset = () => {
    requestId.current = null;
    setCustomerId(null);
    setCustomerName(undefined);
    setYourReference("");
    setLines([newLine(defaultVat)]);
    setErrors({});
    setFailure(null);
    setIssued(null);
    setPayment(null);
  };

  const setLine = (key: string, patch: Partial<QuickLine>) => {
    changed();
    setLines((current) => current.map((l) => (l.key === key ? { ...l, ...patch } : l)));
  };

  const customerHref = `/customers/${issued?.customerId ?? customerId}`;
  const customerLink = (
    <Anchor
      size="sm"
      href={appUrl(customerHref)}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        navigate({ to: customerHref });
      }}
    >
      {t("takePayment.openCustomer")}
    </Anchor>
  );

  const vatOptions = meta.vatCodes.map((c) => ({ value: String(c.id), label: `${c.code} ${c.name}` }));
  const blocked = !meta.storageAvailable || customerId === null;

  if (issued) {
    return (
      <Stack gap="md">
        <Alert color="green" icon={<IconCircleCheck size={16} />}>
          {t("quick.issued", { number: issued.number })}
        </Alert>
        <Group>
          <PdfButton url={pdfUrl(issued.id)} mode="download" leftSection={<IconDownload size={16} />}>
            {t("downloadPdf")}
          </PdfButton>
          {onSite && (
            <PdfButton url={pdfUrl(issued.id)} mode="open" variant="default">
              {t("quick.showPdf")}
            </PdfButton>
          )}
          <Button variant="default" onClick={() => navigate(invoiceLinkOptions(issued.id))}>
            {t("quick.openInvoice")}
          </Button>
          <Button variant="subtle" onClick={reset}>
            {t("quick.another")}
          </Button>
        </Group>
        {payment && (
          <Modal opened onClose={() => setPayment(null)} title={t("takePayment.title")} fullScreen>
            {payment.kind === "asking" && (
              <Stack align="center" py="xl">
                <Loader />
                <Text>{t("takePayment.asking")}</Text>
              </Stack>
            )}
            {payment.kind === "qr" && (
              <QrScreen
                invoiceId={issued.id}
                attemptId={payment.attemptId}
                qrSvg={payment.qrSvg}
                notice={payment.notice}
                onClose={() => navigate(invoiceLinkOptions(issued.id))}
              />
            )}
            {payment.kind === "refused" && (
              <Center py="xl">
                <Stack maw={560}>
                  <Alert color="red" icon={<IconAlertCircle size={16} />} title={payment.title}>
                    <Stack gap="xs">
                      {payment.words && <Text size="sm">{payment.words}</Text>}
                      {payment.customerLink && customerLink}
                    </Stack>
                  </Alert>
                  {payment.staleAttemptId !== undefined && (
                    <Button onClick={() => void cancelAndRetry(issued, payment.staleAttemptId as number)}>
                      {t("takePayment.cancelAndRetry")}
                    </Button>
                  )}
                  {payment.offerNew && onSite && (
                    <Button onClick={() => void ask(issued)}>{t("quick.takePayment")}</Button>
                  )}
                  <Button variant="default" onClick={() => setPayment(null)}>
                    {t("qr.close")}
                  </Button>
                </Stack>
              </Center>
            )}
          </Modal>
        )}
      </Stack>
    );
  }

  return (
    <Stack gap="md">
      {!meta.storageAvailable && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("storageUnavailableHint")}
        </Alert>
      )}
      <Card withBorder>
        <Stack gap="sm">
          <Group align="flex-end">
            <div style={{ flex: 1 }}>
              <CustomerPicker
                value={customerId}
                selected={customerId !== null ? { id: customerId, name: customerName } : undefined}
                required
                error={errors.customerId}
                onChange={(id) => {
                  changed();
                  setCustomerId(id);
                  setCustomerName(undefined);
                }}
              />
            </div>
            {canCreateCustomers && (
              <Button variant="default" leftSection={<IconPlus size={16} />} onClick={() => setCreatingCustomer(true)}>
                {t("quick.newCustomer")}
              </Button>
            )}
          </Group>
          {customerId !== null && address.isSuccess && (
            <div>
              <Text size="sm" fw={500}>
                {t("quick.address")}
              </Text>
              {address.data ? (
                <Text size="sm">{addressLine(address.data)}</Text>
              ) : (
                <Text size="sm" c="dimmed">
                  {t("quick.noAddress")}
                </Text>
              )}
            </div>
          )}
          <TextInput
            label={t("yourReference")}
            value={yourReference}
            error={errors.yourReference}
            onChange={(e) => {
              changed();
              setYourReference(e.currentTarget.value);
            }}
          />
        </Stack>
      </Card>
      <Card withBorder>
        <Stack gap="sm">
          <Text fw={500}>{t("quick.lines")}</Text>
          {lines.map((l, i) => (
            <Group key={l.key} align="flex-start" wrap="wrap" gap="xs">
              <TextInput
                style={{ flex: 3, minWidth: 200 }}
                aria-label={t("lineDescription", { n: i + 1 })}
                placeholder={t("description")}
                value={l.description}
                error={errors[`lines[${i}].description`]}
                onChange={(e) => setLine(l.key, { description: e.currentTarget.value })}
              />
              <NumberInput
                style={{ flex: 1, minWidth: 90 }}
                aria-label={t("lineQuantity", { n: i + 1 })}
                placeholder={t("quantity")}
                decimalScale={3}
                min={0}
                value={l.quantity}
                error={errors[`lines[${i}].quantity`]}
                onChange={(v) => setLine(l.key, { quantity: v })}
              />
              <TextInput
                style={{ flex: 1, minWidth: 80 }}
                aria-label={t("lineUnit", { n: i + 1 })}
                placeholder={t("unit")}
                value={l.unit}
                error={errors[`lines[${i}].unit`]}
                onChange={(e) => setLine(l.key, { unit: e.currentTarget.value })}
              />
              <NumberInput
                style={{ flex: 1, minWidth: 110 }}
                aria-label={t("lineUnitPrice", { n: i + 1 })}
                placeholder={t("unitPrice")}
                decimalScale={4}
                min={0}
                value={l.unitPrice}
                error={errors[`lines[${i}].unitPrice`]}
                onChange={(v) => setLine(l.key, { unitPrice: v })}
              />
              <Select
                style={{ flex: 2, minWidth: 160 }}
                aria-label={t("lineVatCode", { n: i + 1 })}
                data={vatOptions}
                value={l.vatCodeId === null ? null : String(l.vatCodeId)}
                error={errors[`lines[${i}].vatCodeId`]}
                onChange={(v) => setLine(l.key, { vatCodeId: v === null ? null : Number(v) })}
              />
              <ActionIcon
                variant="subtle"
                color="red"
                mt={6}
                aria-label={t("removeLine", { n: i + 1 })}
                disabled={lines.length === 1}
                onClick={() => {
                  changed();
                  setErrors({});
                  setLines((current) => current.filter((x) => x.key !== l.key));
                }}
              >
                <IconTrash size={16} />
              </ActionIcon>
            </Group>
          ))}
          <Group>
            <Button
              variant="light"
              leftSection={<IconPlus size={16} />}
              disabled={lines.length >= MAX_LINES}
              onClick={() => {
                changed();
                setLines((current) => [...current, newLine(defaultVat)]);
              }}
            >
              {t("addLine")}
            </Button>
          </Group>
        </Stack>
      </Card>
      {failure?.kind === "lost" && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("quick.lostTitle")}>
          <Stack gap="xs" align="flex-start">
            <Text size="sm">{t("quick.lost")}</Text>
            <Button size="xs" disabled={issue.isPending} onClick={() => submit(issue.variables?.payOnSite === true)}>
              {t("quick.tryAgain")}
            </Button>
          </Stack>
        </Alert>
      )}
      {failure?.kind === "refused" && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("quick.couldNotIssue")}>
          <Stack gap="xs">
            <Text size="sm">{failure.words}</Text>
            {failure.customerLink && customerLink}
          </Stack>
        </Alert>
      )}
      <Group>
        <Button
          disabled={blocked || issue.isPending}
          loading={issue.isPending && issue.variables?.payOnSite !== true}
          onClick={() => submit(false)}
        >
          {t("issue")}
        </Button>
        {onSite && (
          <>
            <Button
              color="orange"
              disabled={blocked || !hasAddress || issue.isPending}
              loading={issue.isPending && issue.variables?.payOnSite === true}
              onClick={() => submit(true)}
            >
              {t("quick.issueAndPay")}
            </Button>
            <Text size="sm" c="dimmed" maw={420}>
              {t("quick.kontantsalgReminder")}
            </Text>
          </>
        )}
      </Group>
      {creatingCustomer && (
        <NewCustomerModal
          onClose={() => setCreatingCustomer(false)}
          onCreated={(id, name) => {
            changed();
            setCustomerId(id);
            setCustomerName(name);
            setCreatingCustomer(false);
          }}
        />
      )}
    </Stack>
  );
};

/**
 * "New customer" (D22): a customer made through the customers module's API
 * with the address its invoice needs — the name, whether it is a business or
 * a private person, and the primary invoice address. A customer whose address
 * was refused is still picked, and the screen says the address is missing.
 */
const NewCustomerModal = ({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (id: number, name: string) => void;
}) => {
  const { t } = useInvoiceFormat();
  const [name, setName] = useState("");
  const [type, setType] = useState<"business" | "person">("business");
  const [line1, setLine1] = useState("");
  const [postalCode, setPostalCode] = useState("");
  const [city, setCity] = useState("");
  const [country, setCountry] = useState("NO");
  const create = useMutation({
    mutationFn: () =>
      createCustomerWithAddress({
        name: name.trim(),
        type,
        address: { line1: line1.trim(), postalCode: postalCode.trim(), city: city.trim(), country: country.trim() },
      }),
    onSuccess: (created) => onCreated(created.id, name.trim()),
    onError: (error) => {
      const createdId = (error as { createdCustomerId?: unknown }).createdCustomerId;
      if (typeof createdId === "number") {
        notifications.show({ color: "yellow", message: t("quick.addressNotSaved") });
        onCreated(createdId, name.trim());
        return;
      }
      notifications.show({
        color: "red",
        title: t("quick.couldNotCreateCustomer"),
        message: error instanceof Error ? error.message : String(error),
      });
    },
  });
  const ready = name.trim() && line1.trim() && city.trim() && country.trim();
  return (
    <Modal opened onClose={onClose} title={t("quick.newCustomerTitle")}>
      <Stack gap="sm">
        <TextInput
          label={t("quick.customerName")}
          required
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        <Select
          label={t("quick.customerType")}
          allowDeselect={false}
          data={[
            { value: "business", label: t("quick.business") },
            { value: "person", label: t("quick.person") },
          ]}
          value={type}
          onChange={(v) => setType(v === "person" ? "person" : "business")}
        />
        <TextInput
          label={t("quick.addressLine1")}
          required
          value={line1}
          onChange={(e) => setLine1(e.currentTarget.value)}
        />
        <Group grow>
          <TextInput
            label={t("quick.postalCode")}
            value={postalCode}
            onChange={(e) => setPostalCode(e.currentTarget.value)}
          />
          <TextInput label={t("quick.city")} required value={city} onChange={(e) => setCity(e.currentTarget.value)} />
        </Group>
        <TextInput
          label={t("quick.country")}
          required
          maxLength={2}
          value={country}
          onChange={(e) => setCountry(e.currentTarget.value.toUpperCase())}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={!ready} loading={create.isPending} onClick={() => create.mutate()}>
            {t("quick.createCustomer")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
