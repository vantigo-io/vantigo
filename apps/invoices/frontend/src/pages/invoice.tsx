import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconArrowDown,
  IconArrowUp,
  IconDownload,
  IconEye,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  creditInvoice,
  deleteInvoice,
  type InvoiceDocument,
  type InvoiceInput,
  invoiceQueryOptions,
  pdfUrl,
  previewUrl,
  replaceInvoice,
} from "../api/invoices";
import { type InvoicesMeta, invoicesMetaQueryOptions } from "../api/meta";
import { ApiConflictError, INVOICES_QUERY_KEY } from "../api/request";
import { vatCodesQueryOptions } from "../api/vat-codes";
import { CustomerPicker } from "../components/customer-picker";
import { DocumentLink } from "../components/document-link";
import { PdfButton } from "../components/pdf-button";
import { StaleAlert } from "../components/stale-alert";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { documentTotals, lineAmounts } from "../lib/money";
import { invoiceLinkOptions } from "../lib/routes";
import { draftRate } from "../lib/vat";
import { IssueModal } from "./-issue-modal";

export interface InvoicePageProps {
  invoiceId: number;
  /** Whether the caller holds `customers:view`, which changing the buyer needs (D1). */
  canViewCustomers: boolean;
}

/**
 * One document (D12): a draft's editor, or an issued document's page. The
 * route owns the id and hands it over as a number.
 */
export const InvoicePage = ({ invoiceId, canViewCustomers }: InvoicePageProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const document = useQuery(invoiceQueryOptions(invoiceId));
  // The draft the unsaved edits were made on. While there are any, the editor
  // keeps it rather than remounting on a newer revision a background refetch
  // brings in (coming back from the Preview tab is enough): the person's edits
  // are never thrown away unasked, and the editor says the draft changed.
  const [editedFrom, setEditedFrom] = useState<InvoiceDocument | null>(null);
  if (meta.isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
        {refusalMessage(meta.error, t, date)}
      </Alert>
    );
  }
  if (document.isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoice")}>
        {refusalMessage(document.error, t, date)}
      </Alert>
    );
  }
  if (!document.data || !meta.data) return <ContentSkeleton rows={6} rowHeight={48} />;
  const shown = editedFrom?.id === invoiceId ? editedFrom : document.data;
  return shown.status === "draft" ? (
    <DraftEditor
      key={shown.revision}
      draft={shown}
      latestRevision={document.data.revision}
      dirty={shown === editedFrom}
      onDirtyChange={(dirty) => setEditedFrom((current) => (dirty ? (current ?? shown) : null))}
      meta={meta.data}
      canViewCustomers={canViewCustomers}
    />
  ) : (
    <IssuedDocument document={document.data} canIssue={meta.data.capabilities.canIssue} />
  );
};

/** The heading a document shows: its kind and its number, or "Draft". */
const useHeading = (doc: InvoiceDocument) => {
  const { t } = useInvoiceFormat();
  const kind = doc.kind === "credit_note" ? t("kindCreditNote") : t("kindInvoice");
  return doc.number ? `${kind} ${doc.number}` : `${kind} — ${t("statusDraft")}`;
};

/** A credit note's link to its original: "Credit note for invoice N". */
const CreditsLink = ({ doc }: { doc: InvoiceDocument }) => {
  const { t } = useInvoiceFormat();
  if (!doc.credits) return null;
  return (
    <Text>
      {t("creditsInvoice")} <DocumentLink invoiceId={doc.credits.id}>{doc.credits.number}</DocumentLink>
    </Text>
  );
};

/** A line as the editor holds it. */
interface EditorLine {
  key: string;
  description: string;
  quantity: number | string;
  unit: string;
  unitPrice: number | string;
  discountPercent: number | string;
  vatCodeId: number | null;
  creditsLineId?: number;
}

const numberOf = (v: number | string): number => {
  if (typeof v === "number") return v;
  const parsed = Number(v.replace(",", ".").trim());
  return Number.isFinite(parsed) ? parsed : 0;
};

/**
 * The largest quantity and unit price a line's columns hold (numeric(12,3) and
 * numeric(14,4)), which the server refuses past. The inputs clamp to them;
 * the arithmetic in lib/money copes with anything typed before the clamp.
 */
const MAX_QUANTITY = 999999999.999;
const MAX_UNIT_PRICE = 9999999999.9999;

let lineKeys = 0;
const nextKey = () => `line-${++lineKeys}`;

interface DraftEditorProps {
  draft: InvoiceDocument;
  /** The revision the server last answered: newer than the draft's when someone else saved meanwhile. */
  latestRevision: number;
  /** Whether there are unsaved edits; the page holds it, so a refetch does not remount the editor under them. */
  dirty: boolean;
  onDirtyChange: (dirty: boolean) => void;
  /** The codes a new line may take, today, the capabilities and whether a store exists. */
  meta: InvoicesMeta;
  canViewCustomers: boolean;
}

/**
 * A draft's editor (D12): the buyer, the delivery — required before issue —
 * with an optional place of delivery, the references with a nudge when
 * "Deres ref." is empty, the terms, the lines with a VAT code each from the
 * codes in force today, the totals per rate — the server's for the draft as
 * saved, a live estimate by D5's rule while editing — the draft's warnings,
 * and Save, Preview, Issue and Delete. A save refused as stale, or a newer
 * revision seen while editing, says the draft changed and offers Reload. A credit-note draft offers
 * only what D8 allows: removing lines and lowering quantities and prices, never
 * past the original line's. A caller who may not create drafts sees it read-only;
 * without an object store nothing is issued, so Issue is not offered then.
 */
const DraftEditor = ({
  draft,
  latestRevision,
  dirty,
  onDirtyChange: setDirty,
  meta,
  canViewCustomers,
}: DraftEditorProps) => {
  const { t, money, date } = useInvoiceFormat();
  const { vatCodes, today, storageAvailable } = meta;
  const { canCreate, canIssue } = meta.capabilities;
  // Every code, inactive and expired ones too: a line may carry one the
  // codes in force no longer list, and it is named and totalled as the
  // server does.
  const allCodes = useQuery(vatCodesQueryOptions());
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const heading = useHeading(draft);
  const credit = draft.kind === "credit_note";
  const original = useQuery({
    ...invoiceQueryOptions(draft.credits?.id ?? 0),
    enabled: credit && Boolean(draft.credits),
  });

  const [customerId, setCustomerId] = useState<number | null>(draft.customerId);
  const [deliveryMode, setDeliveryMode] = useState<"date" | "period">(draft.deliveryFrom ? "period" : "date");
  const [deliveryDate, setDeliveryDate] = useState<string | null>(draft.deliveryDate ?? null);
  const [deliveryFrom, setDeliveryFrom] = useState<string | null>(draft.deliveryFrom ?? null);
  const [deliveryTo, setDeliveryTo] = useState<string | null>(draft.deliveryTo ?? null);
  const [elsewhere, setElsewhere] = useState(Boolean(draft.deliveryAddress));
  const [address, setAddress] = useState({
    line1: draft.deliveryAddress?.line1 ?? "",
    line2: draft.deliveryAddress?.line2 ?? "",
    postalCode: draft.deliveryAddress?.postalCode ?? "",
    city: draft.deliveryAddress?.city ?? "",
    country: draft.deliveryAddress?.country ?? "NO",
  });
  const [yourReference, setYourReference] = useState(draft.yourReference);
  const [ourReference, setOurReference] = useState(draft.ourReference);
  const [orderReference, setOrderReference] = useState(draft.orderReference);
  const [terms, setTerms] = useState<number | string>(draft.paymentTermsDays ?? "");
  const [note, setNote] = useState(draft.note);
  const [internalNote, setInternalNote] = useState(draft.internalNote);
  const [lines, setLines] = useState<EditorLine[]>(
    draft.lines.map((l) => ({
      key: nextKey(),
      description: l.description,
      quantity: l.quantity,
      unit: l.unit,
      unitPrice: l.unitPrice,
      discountPercent: l.discountPercent,
      vatCodeId: l.vatCodeId,
      creditsLineId: l.creditsLineId,
    })),
  );
  const [issuing, setIssuing] = useState(false);
  // A save refused as stale (a 409 without a code), or a newer revision seen
  // while editing: nothing to fix but look at the latest version.
  const [conflict, setConflict] = useState(false);
  const [reloadFailed, setReloadFailed] = useState(false);
  const stale = conflict || latestRevision > draft.revision;
  const touch =
    <T,>(setter: (v: T) => void) =>
    (v: T) => {
      setter(v);
      setDirty(true);
    };
  const setLine = (key: string, change: Partial<EditorLine>) => {
    setLines((current) => current.map((l) => (l.key === key ? { ...l, ...change } : l)));
    setDirty(true);
  };
  const move = (index: number, by: number) => {
    setLines((current) => {
      const next = [...current];
      const [line] = next.splice(index, 1);
      next.splice(index + by, 0, line);
      return next;
    });
    setDirty(true);
  };

  // The live estimate by D5's rule, as the server totals the draft: an invoice's
  // lines at the rate each code has today — offered or not, 0 % without a
  // period today — and a credit note's at its original lines' own.
  const originalLine = (line: EditorLine) => original.data?.lines.find((ol) => ol.id === line.creditsLineId);
  const rateOf = (line: EditorLine): { category: string; ratePercent: number } => {
    if (credit) {
      const o = originalLine(line);
      return { category: o?.vatCategory ?? "", ratePercent: o?.vatRatePercent ?? 0 };
    }
    const code = allCodes.data?.find((c) => c.id === line.vatCodeId);
    if (code) return draftRate(code, today);
    const inForce = vatCodes.find((c) => c.id === line.vatCodeId);
    return { category: inForce?.ehfCategory ?? "", ratePercent: inForce?.ratePercent ?? 0 };
  };
  const amounts = lines.map((l) =>
    lineAmounts(numberOf(l.quantity), numberOf(l.unitPrice), numberOf(l.discountPercent)),
  );
  const live = documentTotals(lines.map((l, i) => ({ net: amounts[i].net, ...rateOf(l) })));
  // What the page shows: the server's own figures for the draft as saved —
  // the one authority, which on the credit note completing a full reversal
  // takes what the original charged less what was reversed, where the live
  // rule would round afresh — and the live figures, labelled an estimate,
  // only while the person edits.
  const totals = dirty
    ? live
    : {
        rates: draft.vatSummaries.map((v) => ({
          category: v.vatCategory,
          ratePercent: v.ratePercent,
          taxable: v.taxableAmount,
          vat: v.vatAmount,
        })),
        net: draft.netTotal,
        vat: draft.vatTotal,
        gross: draft.grossTotal,
      };
  const lineNet = (i: number) => (dirty ? amounts[i].net : (draft.lines[i]?.lineNet ?? amounts[i].net));

  const input = (): InvoiceInput => ({
    customerId: customerId ?? draft.customerId,
    revision: draft.revision,
    ...(deliveryMode === "date" && deliveryDate ? { deliveryDate } : {}),
    ...(deliveryMode === "period" && deliveryFrom && deliveryTo ? { deliveryFrom, deliveryTo } : {}),
    ...(elsewhere
      ? {
          deliveryAddress: {
            line1: address.line1,
            line2: address.line2 || undefined,
            postalCode: address.postalCode || undefined,
            city: address.city,
            country: address.country,
          },
        }
      : {}),
    yourReference,
    ourReference,
    orderReference,
    ...(credit || terms === "" ? {} : { paymentTermsDays: numberOf(terms) }),
    note,
    internalNote,
    lines: lines.map((l) => ({
      description: l.description,
      quantity: numberOf(l.quantity),
      unit: l.unit,
      unitPrice: numberOf(l.unitPrice),
      discountPercent: numberOf(l.discountPercent),
      vatCodeId: l.vatCodeId ?? 0,
      ...(l.creditsLineId ? { creditsLineId: l.creditsLineId } : {}),
    })),
  });

  const save = useMutation({
    mutationFn: () => replaceInvoice(draft.id, input()),
    onSuccess: async (saved) => {
      queryClient.setQueryData(invoiceQueryOptions(draft.id).queryKey, saved);
      // The saved revision replaces the edited one: the editor remounts on it.
      setDirty(false);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "list"] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t, date) });
    },
  });
  // Reload drops the unsaved edits for the latest revision, which the editor
  // then remounts on.
  const reload = useMutation({
    mutationFn: () => queryClient.fetchQuery({ ...invoiceQueryOptions(draft.id), staleTime: 0 }),
    onMutate: () => setReloadFailed(false),
    onSuccess: () => setDirty(false),
    onError: () => setReloadFailed(true),
  });
  const remove = useMutation({
    mutationFn: () => deleteInvoice(draft.id),
    // Away first, then the cache: invalidated while the page still showed it,
    // the deleted draft would be fetched again and the page would flash "Could
    // not load the document" on the way out. Its own entry is dropped, never
    // refetched.
    onSuccess: async () => {
      await navigate({ to: "/invoices" });
      queryClient.removeQueries({ queryKey: invoiceQueryOptions(draft.id).queryKey, exact: true });
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDelete"), message: refusalMessage(error, t, date) }),
  });
  const confirmDelete = () =>
    modals.openConfirmModal({
      title: t("deleteDraftTitle"),
      children: <Text size="sm">{t("deleteDraftBody")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  // The codes in force today, and any code a line carries that they no
  // longer list — deactivated or expired — by its name, so it never shows
  // blank.
  const vatOptions = vatCodes.map((c) => ({ value: String(c.id), label: `${c.code} — ${c.name}` }));
  for (const id of new Set(lines.map((l) => l.vatCodeId))) {
    if (id === null || vatOptions.some((o) => o.value === String(id))) continue;
    const code = allCodes.data?.find((c) => c.id === id);
    const name = code ? `${code.code} — ${code.name}` : String(id);
    vatOptions.push({ value: String(id), label: t("vatCodeNotOffered", { label: name }) });
  }
  const editable = canCreate;
  const fixed = credit || !editable;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("invoices"), to: "/invoices" }, { label: heading }]}
        title={heading}
        actions={
          <Group>
            {canCreate && (
              // The preview renders the draft as saved, so while there are
              // unsaved edits it is held back as Issue is: it would show
              // something other than what is on screen.
              <PdfButton
                url={previewUrl(draft.id)}
                mode="open"
                variant="default"
                disabled={dirty}
                leftSection={<IconEye size={16} />}
              >
                {t("preview")}
              </PdfButton>
            )}
            {canCreate && (
              <Button variant="default" color="red" leftSection={<IconTrash size={16} />} onClick={confirmDelete}>
                {t("delete")}
              </Button>
            )}
            {canCreate && (
              <Button variant="default" loading={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
                {t("save")}
              </Button>
            )}
            {canIssue && (
              <Button disabled={dirty || !storageAvailable} onClick={() => setIssuing(true)}>
                {t("issue")}
              </Button>
            )}
          </Group>
        }
      />
      <CreditsLink doc={draft} />
      {stale && (
        <StaleAlert
          title={t("draftChangedTitle")}
          message={t("draftChangedMessage")}
          reloadFailedMessage={reloadFailed ? t("couldNotReload") : undefined}
          reloading={reload.isPending}
          onReload={() => reload.mutate()}
        />
      )}
      {dirty && (
        <Text size="sm" c="dimmed">
          {canIssue ? t("saveBeforePreviewOrIssue") : t("saveBeforePreview")}
        </Text>
      )}
      {!storageAvailable && canIssue && (
        <Text size="sm" c="dimmed">
          {t("storageUnavailableHint")}
        </Text>
      )}
      {draft.warnings.length > 0 && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("warnings")}>
          <Stack gap={4}>
            {draft.warnings.map((w) => (
              <Text key={w} size="sm">
                {t(`warning.${w}`)}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
      <Card withBorder>
        <Stack>
          {credit || !canViewCustomers ? (
            <Text>
              <Text span fw={600}>
                {t("customer")}:
              </Text>{" "}
              {draft.customerName ?? t("unknownCustomer")}
            </Text>
          ) : (
            <CustomerPicker
              value={customerId}
              readOnly={!editable}
              onChange={touch(setCustomerId)}
              selectedName={draft.customerName}
              required
            />
          )}
          <Group align="flex-end">
            <SegmentedControl
              aria-label={t("delivery")}
              disabled={fixed}
              value={deliveryMode}
              onChange={(v) => touch(setDeliveryMode)(v as "date" | "period")}
              data={[
                { value: "date", label: t("deliveryDay") },
                { value: "period", label: t("deliveryPeriod") },
              ]}
            />
            {deliveryMode === "date" ? (
              <DateInput
                label={t("deliveryDate")}
                valueFormat={t("dateInputFormat")}
                disabled={fixed}
                value={deliveryDate}
                onChange={touch(setDeliveryDate)}
              />
            ) : (
              <>
                <DateInput
                  label={t("deliveryFrom")}
                  valueFormat={t("dateInputFormat")}
                  disabled={fixed}
                  value={deliveryFrom}
                  onChange={touch(setDeliveryFrom)}
                />
                <DateInput
                  label={t("deliveryTo")}
                  valueFormat={t("dateInputFormat")}
                  disabled={fixed}
                  value={deliveryTo}
                  onChange={touch(setDeliveryTo)}
                />
              </>
            )}
          </Group>
          {!deliveryDate && !(deliveryFrom && deliveryTo) && (
            <Text size="sm" c="orange">
              {t("deliveryRequiredToIssue")}
            </Text>
          )}
          <Checkbox
            label={t("deliverElsewhere")}
            disabled={fixed}
            checked={elsewhere}
            onChange={(e) => touch(setElsewhere)(e.currentTarget.checked)}
          />
          {elsewhere && (
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              <TextInput
                label={t("addressLine1")}
                disabled={fixed}
                value={address.line1}
                onChange={(e) => touch(setAddress)({ ...address, line1: e.currentTarget.value })}
              />
              <TextInput
                label={t("addressLine2")}
                disabled={fixed}
                value={address.line2}
                onChange={(e) => touch(setAddress)({ ...address, line2: e.currentTarget.value })}
              />
              <TextInput
                label={t("postalCode")}
                disabled={fixed}
                value={address.postalCode}
                onChange={(e) => touch(setAddress)({ ...address, postalCode: e.currentTarget.value })}
              />
              <TextInput
                label={t("city")}
                disabled={fixed}
                value={address.city}
                onChange={(e) => touch(setAddress)({ ...address, city: e.currentTarget.value })}
              />
              <TextInput
                label={t("country")}
                disabled={fixed}
                value={address.country}
                onChange={(e) => touch(setAddress)({ ...address, country: e.currentTarget.value.toUpperCase() })}
              />
            </SimpleGrid>
          )}
          <SimpleGrid cols={{ base: 1, sm: 4 }}>
            <TextInput
              label={t("yourReference")}
              disabled={fixed}
              value={yourReference}
              onChange={(e) => touch(setYourReference)(e.currentTarget.value)}
              description={yourReference === "" ? t("yourReferenceNudge") : undefined}
            />
            <TextInput
              label={t("ourReference")}
              disabled={fixed}
              value={ourReference}
              onChange={(e) => touch(setOurReference)(e.currentTarget.value)}
            />
            <TextInput
              label={t("orderReference")}
              disabled={fixed}
              value={orderReference}
              onChange={(e) => touch(setOrderReference)(e.currentTarget.value)}
            />
            {!credit && (
              <NumberInput
                label={t("paymentTermsDays")}
                min={0}
                max={365}
                readOnly={!editable}
                value={terms}
                onChange={touch(setTerms)}
              />
            )}
          </SimpleGrid>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={4}>{t("lines")}</Title>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("description")}</Table.Th>
                <Table.Th>{t("quantity")}</Table.Th>
                <Table.Th>{t("unit")}</Table.Th>
                <Table.Th>{t("unitPrice")}</Table.Th>
                <Table.Th>{t("discountPercent")}</Table.Th>
                <Table.Th>{t("vatCode")}</Table.Th>
                <Table.Th ta="right">{t("lineNet")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map((l, i) => (
                <Table.Tr key={l.key}>
                  <Table.Td>
                    <TextInput
                      aria-label={t("lineDescription", { n: i + 1 })}
                      readOnly={!editable}
                      value={l.description}
                      onChange={(e) => setLine(l.key, { description: e.currentTarget.value })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineQuantity", { n: i + 1 })}
                      decimalScale={3}
                      min={0}
                      max={credit ? originalLine(l)?.quantity : MAX_QUANTITY}
                      readOnly={!editable}
                      value={l.quantity}
                      onChange={(v) => setLine(l.key, { quantity: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <TextInput
                      aria-label={t("lineUnit", { n: i + 1 })}
                      disabled={fixed}
                      value={l.unit}
                      onChange={(e) => setLine(l.key, { unit: e.currentTarget.value })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineUnitPrice", { n: i + 1 })}
                      decimalScale={4}
                      min={0}
                      max={credit ? originalLine(l)?.unitPrice : MAX_UNIT_PRICE}
                      readOnly={!editable}
                      value={l.unitPrice}
                      onChange={(v) => setLine(l.key, { unitPrice: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <NumberInput
                      aria-label={t("lineDiscount", { n: i + 1 })}
                      decimalScale={2}
                      min={0}
                      max={100}
                      disabled={fixed}
                      value={l.discountPercent}
                      onChange={(v) => setLine(l.key, { discountPercent: v })}
                    />
                  </Table.Td>
                  <Table.Td>
                    <Select
                      aria-label={t("lineVatCode", { n: i + 1 })}
                      disabled={fixed}
                      data={vatOptions}
                      value={l.vatCodeId === null ? null : String(l.vatCodeId)}
                      onChange={(v) => setLine(l.key, { vatCodeId: v === null ? null : Number(v) })}
                    />
                  </Table.Td>
                  <Table.Td ta="right">{money(lineNet(i), draft.currency)}</Table.Td>
                  <Table.Td>
                    {editable && (
                      <Group gap={4} wrap="nowrap">
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("moveLineUp", { n: i + 1 })}
                          disabled={i === 0}
                          onClick={() => move(i, -1)}
                        >
                          <IconArrowUp size={16} />
                        </ActionIcon>
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("moveLineDown", { n: i + 1 })}
                          disabled={i === lines.length - 1}
                          onClick={() => move(i, 1)}
                        >
                          <IconArrowDown size={16} />
                        </ActionIcon>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          aria-label={t("removeLine", { n: i + 1 })}
                          onClick={() => {
                            setLines((c) => c.filter((x) => x.key !== l.key));
                            setDirty(true);
                          }}
                        >
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Group>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {!fixed && (
            <Group>
              <Button
                variant="light"
                leftSection={<IconPlus size={16} />}
                onClick={() => {
                  setLines((c) => [
                    ...c,
                    {
                      key: nextKey(),
                      description: "",
                      quantity: 1,
                      unit: "",
                      unitPrice: 0,
                      discountPercent: 0,
                      vatCodeId: vatCodes[0]?.id ?? null,
                    },
                  ]);
                  setDirty(true);
                }}
              >
                {t("addLine")}
              </Button>
            </Group>
          )}
          {dirty && (
            <Text size="sm" c="dimmed">
              {t("totalsEstimate")}
            </Text>
          )}
          <Totals
            currency={draft.currency}
            rates={totals.rates}
            net={totals.net}
            vat={totals.vat}
            gross={totals.gross}
          />
        </Stack>
      </Card>
      <Card withBorder>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Textarea
            label={t("note")}
            description={t("noteHint")}
            readOnly={!editable}
            value={note}
            onChange={(e) => touch(setNote)(e.currentTarget.value)}
          />
          <Textarea
            label={t("internalNote")}
            description={t("internalNoteHint")}
            readOnly={!editable}
            value={internalNote}
            onChange={(e) => touch(setInternalNote)(e.currentTarget.value)}
          />
        </SimpleGrid>
      </Card>
      {issuing && <IssueModal draft={draft} onClose={() => setIssuing(false)} />}
    </Stack>
  );
};

interface TotalsProps {
  currency: string;
  rates: { category: string; ratePercent: number; taxable: number; vat: number }[];
  net: number;
  vat: number;
  gross: number;
}

/** The totals per rate, then net, VAT and gross. */
const Totals = ({ currency, rates, net, vat, gross }: TotalsProps) => {
  const { t, money, number } = useInvoiceFormat();
  return (
    <Table withRowBorders={false} data-testid="totals">
      <Table.Tbody>
        {rates.map((r) => (
          <Table.Tr key={`${r.category}-${r.ratePercent}`}>
            <Table.Td>{t("vatAtRate", { category: r.category, rate: number(r.ratePercent, 2) })}</Table.Td>
            <Table.Td ta="right">{money(r.taxable, currency)}</Table.Td>
            <Table.Td ta="right">{money(r.vat, currency)}</Table.Td>
          </Table.Tr>
        ))}
        <Table.Tr>
          <Table.Td fw={600}>{t("netTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" data-testid="net-total">
            {money(net, currency)}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Td fw={600}>{t("vatTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" data-testid="vat-total">
            {money(vat, currency)}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Td fw={700}>{t("grossTotal")}</Table.Td>
          <Table.Td />
          <Table.Td ta="right" fw={700} data-testid="gross-total">
            {money(gross, currency)}
          </Table.Td>
        </Table.Tr>
      </Table.Tbody>
    </Table>
  );
};

/**
 * An issued document's page (D12): its header, lines, VAT summary and totals,
 * its credit notes, Download PDF, and Credit, which opens the new credit-note
 * draft. A document whose PDF could not be stored at issue says the first
 * download stores it.
 */
const IssuedDocument = ({ document: doc, canIssue }: { document: InvoiceDocument; canIssue: boolean }) => {
  const { t, money, unitPrice, date, number } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const heading = useHeading(doc);
  const credit = useMutation({
    mutationFn: () => creditInvoice(doc.id),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCredit"), message: refusalMessage(error, t, date) }),
  });
  const creditable = doc.kind === "invoice" && canIssue && (doc.uncreditedAmount ?? 0) > 0;
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("invoices"), to: "/invoices" }, { label: heading }]}
        title={heading}
        actions={
          <Group>
            <PdfButton url={pdfUrl(doc.id)} mode="download" leftSection={<IconDownload size={16} />}>
              {t("downloadPdf")}
            </PdfButton>
            {creditable && (
              <Button variant="default" loading={credit.isPending} onClick={() => credit.mutate()}>
                {t("credit")}
              </Button>
            )}
          </Group>
        }
      />
      <CreditsLink doc={doc} />
      {doc.pdfStored === false && <Alert color="yellow">{t("pdfNotStoredYet")}</Alert>}
      {doc.warnings.includes("issued_late") && <Alert color="yellow">{t("warning.issued_late")}</Alert>}
      <Card withBorder>
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <Stack gap={2}>
            <Text fw={600}>{doc.buyer?.name}</Text>
            {doc.buyer?.addressLine1 && <Text size="sm">{doc.buyer.addressLine1}</Text>}
            {(doc.buyer?.postalCode || doc.buyer?.city) && (
              <Text size="sm">{`${doc.buyer?.postalCode ?? ""} ${doc.buyer?.city ?? ""}`.trim()}</Text>
            )}
            {doc.buyer?.organisationNumber && (
              <Text size="sm">{t("orgNumber", { number: doc.buyer.organisationNumber })}</Text>
            )}
            {doc.buyer?.foreignId && <Text size="sm">{t("foreignId", { id: doc.buyer.foreignId })}</Text>}
          </Stack>
          <Stack gap={2}>
            <Text size="sm">
              {t("issueDate")}: {doc.issueDate ? date(doc.issueDate) : ""}
            </Text>
            {doc.dueDate && (
              <Text size="sm">
                {t("dueDate")}: {date(doc.dueDate)}
              </Text>
            )}
            {doc.deliveryDate && (
              <Text size="sm">
                {t("deliveryDate")}: {date(doc.deliveryDate)}
              </Text>
            )}
            {doc.deliveryFrom && doc.deliveryTo && (
              <Text size="sm">
                {t("deliveryPeriod")}: {date(doc.deliveryFrom)} – {date(doc.deliveryTo)}
              </Text>
            )}
          </Stack>
          <Stack gap={2}>
            {doc.yourReference && (
              <Text size="sm">
                {t("yourReference")}: {doc.yourReference}
              </Text>
            )}
            {doc.ourReference && (
              <Text size="sm">
                {t("ourReference")}: {doc.ourReference}
              </Text>
            )}
            {doc.orderReference && (
              <Text size="sm">
                {t("orderReference")}: {doc.orderReference}
              </Text>
            )}
          </Stack>
        </SimpleGrid>
      </Card>
      <Card withBorder>
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("description")}</Table.Th>
              <Table.Th ta="right">{t("quantity")}</Table.Th>
              <Table.Th>{t("unit")}</Table.Th>
              <Table.Th ta="right">{t("unitPrice")}</Table.Th>
              <Table.Th ta="right">{t("discountPercent")}</Table.Th>
              <Table.Th ta="right">{t("vatPercent")}</Table.Th>
              <Table.Th ta="right">{t("lineNet")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {doc.lines.map((l) => (
              <Table.Tr key={l.id}>
                <Table.Td>{l.description}</Table.Td>
                <Table.Td ta="right">{number(l.quantity)}</Table.Td>
                <Table.Td>{l.unit}</Table.Td>
                <Table.Td ta="right">{unitPrice(l.unitPrice, doc.currency)}</Table.Td>
                <Table.Td ta="right">{number(l.discountPercent, 2)}</Table.Td>
                <Table.Td ta="right">{number(l.vatRatePercent ?? 0, 2)}</Table.Td>
                <Table.Td ta="right">{money(l.lineNet, doc.currency)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Totals
          currency={doc.currency}
          rates={doc.vatSummaries.map((s) => ({
            category: s.vatCategory,
            ratePercent: s.ratePercent,
            taxable: s.taxableAmount,
            vat: s.vatAmount,
          }))}
          net={doc.netTotal}
          vat={doc.vatTotal}
          gross={doc.grossTotal}
        />
      </Card>
      {doc.kind === "invoice" && (
        <Card withBorder>
          <Stack gap="xs">
            <Title order={4}>{t("creditNotes")}</Title>
            {(doc.creditNotes ?? []).length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("noCreditNotes")}
              </Text>
            ) : (
              (doc.creditNotes ?? []).map((c) => (
                <Group key={c.id} gap="xs">
                  <DocumentLink invoiceId={c.id}>
                    {c.number ? `${t("kindCreditNote")} ${c.number}` : `${t("kindCreditNote")} — ${t("statusDraft")}`}
                  </DocumentLink>
                  {c.status === "draft" && <Badge variant="light">{t("statusDraft")}</Badge>}
                  <Text size="sm">{money(c.grossTotal, doc.currency)}</Text>
                </Group>
              ))
            )}
            <Text size="sm">
              {t("uncreditedAmount")}: {money(doc.uncreditedAmount ?? 0, doc.currency)}
            </Text>
          </Stack>
        </Card>
      )}
    </Stack>
  );
};
