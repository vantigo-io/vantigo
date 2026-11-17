import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  FileInput,
  Group,
  Modal,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPaperclip, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  type AccountingMeta,
  type AccountingSupplierInvoice,
  type AccountingSupplierInvoiceInput,
  type AccountingSupplierInvoiceState,
  type AccountingTaxCode,
  createSupplierInvoice,
  deleteSupplierInvoice,
  deleteSupplierInvoiceFile,
  paySupplierInvoice,
  postSupplierInvoice,
  removeSupplierPayment,
  reverseAndCopySupplierInvoice,
  supplierInvoiceFileUrl,
  supplierInvoiceQueryOptions,
  supplierInvoicesQueryOptions,
  suppliersQueryOptions,
  taxCodesQueryOptions,
  updateSupplierInvoice,
  uploadSupplierInvoiceFile,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, ProjectSelect, TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { fromOre, type LineCode, lineTotals, rateOf, reverseChargeVAT, toNOK, toOre } from "../lib/purchase";
import {
  NEW_SUPPLIER_INVOICE_ROUTE_PATH,
  purchaseOrderPath,
  supplierInvoicePath,
  supplierPath,
  voucherPath,
} from "../lib/routes";
import { SupplierFormModal } from "./suppliers";

const states: AccountingSupplierInvoiceState[] = ["draft", "posted", "paid", "reversed"];

/**
 * Supplier invoices (A18): drafts, posted ones and what is paid, filtered by
 * state, supplier and due date. Entering one needs accounting:post.
 */
export const SupplierInvoicesPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navSupplierInvoices")} description={t("supplierInvoicesDescription")} />
          <InvoiceList canPost={meta.canPost} />
        </Stack>
      )}
    </BooksGate>
  );
};

const InvoiceList = ({ canPost }: { canPost: boolean }) => {
  const { t, money, day } = useAccountingFormat();
  const [state, setState] = useState<AccountingSupplierInvoiceState | null>(null);
  const [supplier, setSupplier] = useState<string | null>(null);
  const [dueFrom, setDueFrom] = useState("");
  const [dueTo, setDueTo] = useState("");
  const [overdue, setOverdue] = useState(false);
  const suppliers = useQuery(suppliersQueryOptions({ limit: 500 }));
  const list = useQuery(
    supplierInvoicesQueryOptions({
      state: state ?? undefined,
      supplierId: supplier ? Number(supplier) : undefined,
      dueFrom: dueFrom || undefined,
      dueTo: dueTo || undefined,
      overdue: overdue || undefined,
    }),
  );
  return (
    <Stack gap="md">
      <Group align="flex-end">
        <Select
          label={t("state")}
          clearable
          data={states.map((s) => ({ value: s, label: t(`supplierInvoiceState_${s}`) }))}
          value={state}
          onChange={(v) => setState(v as AccountingSupplierInvoiceState | null)}
        />
        <Select
          label={t("supplier")}
          searchable
          clearable
          data={(suppliers.data?.items ?? []).map((s) => ({ value: String(s.id), label: `${s.number} ${s.name}` }))}
          value={supplier}
          onChange={setSupplier}
        />
        <TextInput
          type="date"
          label={t("dueFrom")}
          value={dueFrom}
          onChange={(e) => setDueFrom(e.currentTarget.value)}
        />
        <TextInput type="date" label={t("dueTo")} value={dueTo} onChange={(e) => setDueTo(e.currentTarget.value)} />
        <Checkbox label={t("overdueOnly")} checked={overdue} onChange={(e) => setOverdue(e.currentTarget.checked)} />
        {canPost && (
          <Button
            leftSection={<IconPlus size={16} />}
            renderRoot={(props) => <Link to={NEW_SUPPLIER_INVOICE_ROUTE_PATH} {...props} />}
          >
            {t("newSupplierInvoice")}
          </Button>
        )}
      </Group>
      {list.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {list.error.message}
        </Alert>
      ) : list.isPending ? (
        <ContentSkeleton rows={4} />
      ) : list.data.items.length === 0 ? (
        <EmptyState title={t("noSupplierInvoices")} description={t("noSupplierInvoicesHint")} />
      ) : (
        <Table striped highlightOnHover data-testid="supplier-invoices">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("supplier")}</Table.Th>
              <Table.Th>{t("invoiceNumber")}</Table.Th>
              <Table.Th>{t("invoiceDate")}</Table.Th>
              <Table.Th>{t("dueDate")}</Table.Th>
              <Table.Th ta="right">{t("total")}</Table.Th>
              <Table.Th ta="right">{t("balance")}</Table.Th>
              <Table.Th>{t("state")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {list.data.items.map((inv) => (
              <Table.Tr key={inv.id}>
                <Table.Td>{inv.supplierName}</Table.Td>
                <Table.Td>
                  <Anchor component={Link} to={supplierInvoicePath(inv.id)}>
                    {inv.kind === "credit_note" ? `${t("creditNote")} ${inv.invoiceNumber}` : inv.invoiceNumber}
                  </Anchor>
                </Table.Td>
                <Table.Td>{day(inv.invoiceDate)}</Table.Td>
                <Table.Td>{day(inv.dueDate)}</Table.Td>
                <Table.Td ta="right">{money(inv.totalNok)}</Table.Td>
                <Table.Td ta="right">{money(inv.balance)}</Table.Td>
                <Table.Td>
                  <Group gap={4}>
                    <StateBadge invoice={inv} />
                    <PurchaseMatchBadge invoice={inv} />
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  );
};

const StateBadge = ({ invoice }: { invoice: AccountingSupplierInvoice }) => {
  const { t } = useAccountingFormat();
  const color = { draft: "gray", posted: "blue", paid: "green", reversed: "red" }[invoice.state];
  return (
    <Group gap={4}>
      <Badge variant="light" color={color}>
        {t(`supplierInvoiceState_${invoice.state}`)}
      </Badge>
      {invoice.overdue && (
        <Badge variant="light" color="orange">
          {t("overdue")}
        </Badge>
      )}
    </Group>
  );
};

/**
 * The purchase orders Purchasing has matched the invoice to (purchasing
 * design P6), each a badge linking to the order, and the match's differences;
 * nothing when Purchasing is off or the invoice is matched to none.
 */
const PurchaseMatchBadge = ({ invoice }: { invoice: AccountingSupplierInvoice }) => {
  const { t } = useAccountingFormat();
  const match = invoice.purchaseMatch;
  if (!match || match.orders.length === 0) return null;
  return (
    <Group gap={4} data-testid="purchase-match">
      {match.orders.map((o) => (
        <Badge
          key={o.id}
          variant="outline"
          color="teal"
          style={{ cursor: "pointer" }}
          renderRoot={(props) => <Link to={purchaseOrderPath(o.id) as never} {...props} />}
        >
          {t("purchaseOrderMatched", { number: o.number })}
        </Badge>
      ))}
      {match.differences > 0 && (
        <Badge variant="light" color="orange">
          {t("purchaseMatchDifferences", { count: match.differences })}
        </Badge>
      )}
    </Group>
  );
};

/** A new supplier invoice: the form, empty. */
export const NewSupplierInvoicePage = () => <BooksGate>{(meta) => <InvoiceForm meta={meta} />}</BooksGate>;

/** A supplier invoice's page: a draft's form, or what is posted, with its payments. */
/**
 * One supplier invoice. `extraPanels` is the host's slot for what other
 * modules show beside the invoice — Purchasing's match, for one — given the
 * invoice's id once it has loaded; Accounting never names them.
 */
export const SupplierInvoicePage = ({ extraPanels }: { extraPanels?: (invoiceId: number) => ReactNode } = {}) => {
  const { supplierInvoiceId } = useParams({ strict: false }) as { supplierInvoiceId: string };
  const id = Number(supplierInvoiceId);
  const { t } = useAccountingFormat();
  const invoice = useQuery(supplierInvoiceQueryOptions(id));
  return (
    <BooksGate>
      {(meta) =>
        invoice.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {invoice.error.message}
          </Alert>
        ) : invoice.isPending ? (
          <ContentSkeleton rows={6} />
        ) : (
          <Stack gap="lg">
            {invoice.data.state === "draft" && meta.canPost ? (
              <InvoiceForm meta={meta} invoice={invoice.data} key={invoice.data.revision} />
            ) : (
              <PostedInvoice meta={meta} invoice={invoice.data} />
            )}
            {extraPanels?.(invoice.data.id)}
          </Stack>
        )
      }
    </BooksGate>
  );
};

/** One line of the form, as text. */
interface LineFields {
  accountId: string | null;
  taxCodeId: string | null;
  amount: string;
  entry: "net" | "gross";
  projectId: string | null;
  text: string;
}

const emptyLine = (): LineFields => ({
  accountId: null,
  taxCodeId: null,
  amount: "",
  entry: "gross",
  projectId: null,
  text: "",
});

/** A code as the live totals read it, at its current rate. */
const lineCode = (codes: AccountingTaxCode[] | undefined, id: string | null): LineCode | null => {
  const c = codes?.find((x) => String(x.id) === id);
  return c ? { direction: c.direction, reverseCharge: c.reverseCharge, rate: c.currentRate } : null;
};

/**
 * The form's live totals in øre (A18): each line's net and VAT, the lines'
 * gross, their VAT, and the reverse-charge VAT the buyer calculates on the NOK
 * base — at the invoice's rate when it is in another currency.
 */
const formTotals = (lines: LineFields[], codes: AccountingTaxCode[] | undefined, rate: string | null) => {
  const computed: (ReturnType<typeof lineTotals> | null)[] = [];
  let linesOre = 0n;
  let vatOre = 0n;
  let rcOre = 0n;
  for (const l of lines) {
    const ore = toOre(l.amount);
    if (ore === null) {
      computed.push(null);
      continue;
    }
    const code = lineCode(codes, l.taxCodeId);
    const totals = lineTotals(ore, l.entry === "gross", code);
    computed.push(totals);
    linesOre += totals.gross;
    vatOre += totals.vat;
    if (code?.reverseCharge) rcOre += reverseChargeVAT(rate ? (toNOK(totals.net, rate) ?? 0n) : totals.net, code.rate);
  }
  return { computed, linesOre, vatOre, rcOre };
};

/**
 * The form of a draft (A18, R8): the supplier, the supplier's number, the
 * dates, the currency with its rate or its NOK total, the lines with their
 * account, tax code, amount as entered — net or gross — and project, and the
 * live totals with the VAT computed in øre as Post will book it; the
 * duplicate warning; the files; Save, Post and Delete.
 */
const InvoiceForm = ({ meta, invoice }: { meta: AccountingMeta; invoice?: AccountingSupplierInvoice }) => {
  const { t, amount } = useAccountingFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const suppliers = useQuery(suppliersQueryOptions({ active: true, limit: 500 }));
  const codes = useQuery(taxCodesQueryOptions());
  const [supplierId, setSupplierId] = useState<string | null>(invoice ? String(invoice.supplierId) : null);
  const [kind, setKind] = useState<"invoice" | "credit_note">(invoice?.kind ?? "invoice");
  const [number, setNumber] = useState(invoice?.invoiceNumber ?? "");
  const [invoiceDate, setInvoiceDate] = useState(invoice?.invoiceDate ?? meta.today);
  const [dueDate, setDueDate] = useState(invoice?.dueDate ?? "");
  const [currency, setCurrency] = useState(invoice?.currency ?? "NOK");
  const [byRate, setByRate] = useState(true);
  const [rate, setRate] = useState(invoice && invoice.currency !== "NOK" ? (invoice.exchangeRate ?? "") : "");
  const [totalNok, setTotalNok] = useState(invoice && invoice.currency !== "NOK" ? (invoice.totalNok ?? "") : "");
  const [total, setTotal] = useState(invoice?.total ?? "");
  const [kid, setKid] = useState(invoice?.kid ?? "");
  const [description, setDescription] = useState(invoice?.description ?? "");
  const [credits, setCredits] = useState<string | null>(
    invoice?.creditsInvoiceId ? String(invoice.creditsInvoiceId) : null,
  );
  const [lines, setLines] = useState<LineFields[]>(
    invoice && invoice.lines.length > 0
      ? invoice.lines.map((l) => ({
          accountId: String(l.accountId),
          taxCodeId: l.taxCodeId ? String(l.taxCodeId) : null,
          amount: l.amount,
          entry: l.entry,
          projectId: l.projectId ? String(l.projectId) : null,
          text: l.text,
        }))
      : [emptyLine()],
  );
  const [addingSupplier, setAddingSupplier] = useState(false);
  const foreign = currency !== "NOK";
  const chosen = suppliers.data?.items.find((s) => String(s.id) === supplierId);

  // The duplicate warning: the same supplier's number on another invoice not reversed.
  const duplicates = useQuery({
    ...supplierInvoicesQueryOptions({ supplierId: Number(supplierId), supplierNumber: number.trim() }),
    enabled: supplierId !== null && number.trim() !== "",
  });
  const duplicate = duplicates.data?.items.find(
    (d) => d.id !== invoice?.id && d.state !== "reversed" && d.kind === kind,
  );
  const credited = useQuery({
    ...supplierInvoicesQueryOptions({ supplierId: Number(supplierId), state: "posted" }),
    enabled: kind === "credit_note" && supplierId !== null,
  });

  // The live totals, in øre.
  const { computed, linesOre, vatOre, rcOre } = formTotals(lines, codes.data, foreign ? rate : null);
  const totalOre = toOre(total);
  const difference = totalOre === null ? null : totalOre - linesOre;
  const nokOre =
    totalOre === null ? null : !foreign ? totalOre : byRate ? (rate ? toNOK(totalOre, rate) : null) : toOre(totalNok);
  const shownRate = foreign && !byRate && nokOre !== null && totalOre !== null ? rateOf(nokOre, totalOre) : rate;

  const body = (): AccountingSupplierInvoiceInput => ({
    supplierId: Number(supplierId),
    kind,
    invoiceNumber: number.trim(),
    invoiceDate,
    dueDate: dueDate || null,
    currency,
    exchangeRate: foreign && byRate ? rate || null : null,
    totalNok: foreign && !byRate ? totalNok || null : null,
    total: total.trim(),
    kid: kid.trim() || null,
    description: description.trim() || null,
    creditsInvoiceId: kind === "credit_note" && credits ? Number(credits) : null,
    lines: lines
      .filter((l) => l.accountId && l.amount.trim())
      .map((l) => ({
        accountId: Number(l.accountId),
        taxCodeId: l.taxCodeId ? Number(l.taxCodeId) : null,
        amount: l.amount.trim().replace(",", "."),
        entry: l.entry,
        projectId: l.projectId ? Number(l.projectId) : null,
        text: l.text.trim() || null,
      })),
    ...(invoice ? { revision: invoice.revision } : {}),
  });
  const save = useMutation({
    mutationFn: () => (invoice ? updateSupplierInvoice(invoice.id, body()) : createSupplierInvoice(body())),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
      if (!invoice) await navigate({ to: supplierInvoicePath(saved.id) });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const post = useMutation({
    mutationFn: async () => {
      if (!invoice) throw new Error("not saved");
      await updateSupplierInvoice(invoice.id, body());
      return postSupplierInvoice(invoice.id);
    },
    onSuccess: async (posted) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("posted", { voucher: posted.voucher?.display ?? "" }) });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const remove = useMutation({
    mutationFn: () => (invoice ? deleteSupplierInvoice(invoice.id) : Promise.resolve()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "supplier-invoices"] });
      notifications.show({ color: "green", message: t("deleted") });
      await navigate({ to: "/accounting/supplier-invoices" });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const setLine = (i: number, change: Partial<LineFields>) =>
    setLines((ls) => ls.map((l, j) => (j === i ? { ...l, ...change } : l)));
  const pickAccount = (i: number, accountId: string | null) => {
    setLine(i, { accountId });
  };
  const ready = supplierId !== null && number.trim() !== "" && totalOre !== null;
  return (
    <Stack gap="lg">
      <PageHeader
        title={invoice ? `${t("supplierInvoice")} ${invoice.invoiceNumber}` : t("newSupplierInvoice")}
        description={t("supplierInvoiceFormDescription")}
      />
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        <Group align="flex-end" gap="xs" wrap="nowrap">
          <Select
            style={{ flex: 1 }}
            label={t("supplier")}
            required
            searchable
            data={(suppliers.data?.items ?? []).map((s) => ({ value: String(s.id), label: `${s.number} ${s.name}` }))}
            value={supplierId}
            onChange={(v) => {
              setSupplierId(v);
              const s = suppliers.data?.items.find((x) => String(x.id) === v);
              if (s && !invoice) setCurrency(s.currency);
            }}
          />
          <ActionIcon variant="light" size="lg" aria-label={t("addSupplier")} onClick={() => setAddingSupplier(true)}>
            <IconPlus size={16} />
          </ActionIcon>
        </Group>
        <SegmentedControl
          data={[
            { value: "invoice", label: t("invoice") },
            { value: "credit_note", label: t("creditNote") },
          ]}
          value={kind}
          onChange={(v) => setKind(v as "invoice" | "credit_note")}
        />
        <TextInput
          label={t("invoiceNumber")}
          required
          maxLength={60}
          value={number}
          onChange={(e) => setNumber(e.currentTarget.value)}
        />
        <TextInput
          type="date"
          label={t("invoiceDate")}
          required
          value={invoiceDate}
          onChange={(e) => setInvoiceDate(e.currentTarget.value)}
        />
        <TextInput
          type="date"
          label={t("dueDate")}
          description={chosen ? t("dueDateHint", { days: chosen.paymentTermsDays }) : undefined}
          value={dueDate}
          onChange={(e) => setDueDate(e.currentTarget.value)}
        />
        <TextInput label={t("kid")} value={kid} onChange={(e) => setKid(e.currentTarget.value)} />
        <TextInput
          label={t("currency")}
          maxLength={3}
          value={currency}
          onChange={(e) => setCurrency(e.currentTarget.value.toUpperCase())}
        />
        <TextInput
          label={t("totalInCurrency", { currency })}
          required
          value={total}
          onChange={(e) => setTotal(e.currentTarget.value)}
        />
        {kind === "credit_note" && (
          <Select
            label={t("creditsInvoice")}
            clearable
            data={(credited.data?.items ?? [])
              .filter((i) => i.kind === "invoice")
              .map((i) => ({ value: String(i.id), label: i.invoiceNumber }))}
            value={credits}
            onChange={setCredits}
          />
        )}
      </SimpleGrid>
      {foreign && (
        <Group align="flex-end">
          <SegmentedControl
            data={[
              { value: "rate", label: t("byRate") },
              { value: "nok", label: t("byNokTotal") },
            ]}
            value={byRate ? "rate" : "nok"}
            onChange={(v) => setByRate(v === "rate")}
          />
          {byRate ? (
            <TextInput
              label={t("exchangeRate", { currency })}
              value={rate}
              onChange={(e) => setRate(e.currentTarget.value)}
            />
          ) : (
            <TextInput label={t("totalNok")} value={totalNok} onChange={(e) => setTotalNok(e.currentTarget.value)} />
          )}
          <Text size="sm" data-testid="currency-both">
            {t("rateAndNok", { rate: shownRate || "—", nok: nokOre === null ? "—" : amount(fromOre(nokOre)) })}
          </Text>
        </Group>
      )}
      <TextInput label={t("description")} value={description} onChange={(e) => setDescription(e.currentTarget.value)} />
      {duplicate && (
        <Alert color="orange" icon={<IconAlertCircle size={16} />} title={t("duplicateWarning")}>
          <Anchor component={Link} to={supplierInvoicePath(duplicate.id)}>
            {duplicate.invoiceNumber}
          </Anchor>
        </Alert>
      )}
      <Table data-testid="supplier-invoice-lines">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("taxCode")}</Table.Th>
            <Table.Th>{t("entered")}</Table.Th>
            <Table.Th>{t("amount")}</Table.Th>
            {meta.projectsEnabled && <Table.Th>{t("project")}</Table.Th>}
            <Table.Th>{t("text")}</Table.Th>
            <Table.Th ta="right">{t("net")}</Table.Th>
            <Table.Th ta="right">{t("vat")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {lines.map((l, i) => (
            <Table.Tr key={i}>
              <Table.Td>
                <AccountSelect
                  aria-label={t("lineAccount", { n: i + 1 })}
                  value={l.accountId}
                  onChange={(v) => pickAccount(i, v)}
                />
              </Table.Td>
              <Table.Td>
                <TaxCodeSelect
                  aria-label={t("lineTaxCode", { n: i + 1 })}
                  value={l.taxCodeId}
                  onChange={(v) => setLine(i, { taxCodeId: v })}
                />
              </Table.Td>
              <Table.Td>
                <SegmentedControl
                  size="xs"
                  data={[
                    { value: "gross", label: t("gross") },
                    { value: "net", label: t("netEntry") },
                  ]}
                  value={l.entry}
                  onChange={(v) => setLine(i, { entry: v as "net" | "gross" })}
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  aria-label={t("lineAmount", { n: i + 1 })}
                  value={l.amount}
                  onChange={(e) => setLine(i, { amount: e.currentTarget.value })}
                />
              </Table.Td>
              {meta.projectsEnabled && (
                <Table.Td>
                  <ProjectSelect value={l.projectId} onChange={(v) => setLine(i, { projectId: v })} />
                </Table.Td>
              )}
              <Table.Td>
                <TextInput
                  aria-label={t("lineText", { n: i + 1 })}
                  value={l.text}
                  onChange={(e) => setLine(i, { text: e.currentTarget.value })}
                />
              </Table.Td>
              <Table.Td ta="right">{computed[i] ? amount(fromOre(computed[i].net)) : ""}</Table.Td>
              <Table.Td ta="right">{computed[i] ? amount(fromOre(computed[i].vat)) : ""}</Table.Td>
              <Table.Td>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={t("removeLine", { n: i + 1 })}
                  onClick={() => setLines((ls) => (ls.length > 1 ? ls.filter((_, j) => j !== i) : [emptyLine()]))}
                >
                  <IconTrash size={14} />
                </ActionIcon>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Group justify="space-between">
        <Button
          variant="subtle"
          leftSection={<IconPlus size={14} />}
          onClick={() => setLines((ls) => [...ls, emptyLine()])}
        >
          {t("addLine")}
        </Button>
        <Stack gap={2} align="flex-end" data-testid="supplier-invoice-totals">
          <Text size="sm">{t("linesTotal", { amount: amount(fromOre(linesOre)) })}</Text>
          <Text size="sm">{t("vatTotal", { amount: amount(fromOre(vatOre)) })}</Text>
          {rcOre !== 0n && <Text size="sm">{t("reverseChargeTotal", { amount: amount(fromOre(rcOre)) })}</Text>}
          {difference !== null && difference !== 0n && (
            <Text size="sm" c="red">
              {t("linesDifference", { amount: amount(fromOre(difference)) })}
            </Text>
          )}
        </Stack>
      </Group>
      {invoice && <DraftFiles invoice={invoice} attachmentsEnabled={meta.attachmentsEnabled} />}
      <Group justify="flex-end">
        {invoice && (
          <Button variant="subtle" color="red" loading={remove.isPending} onClick={() => remove.mutate()}>
            {t("delete")}
          </Button>
        )}
        <Button variant="default" loading={save.isPending} disabled={!ready} onClick={() => save.mutate()}>
          {t("saveDraft")}
        </Button>
        {invoice && (
          <Button
            loading={post.isPending}
            disabled={!ready || difference !== 0n || invoice.files.length === 0}
            onClick={() => post.mutate()}
          >
            {t("post")}
          </Button>
        )}
      </Group>
      {addingSupplier && (
        <SupplierFormModal
          onClose={() => setAddingSupplier(false)}
          onSaved={(s) => {
            setSupplierId(String(s.id));
            setCurrency(s.currency);
          }}
        />
      )}
    </Stack>
  );
};

/** A draft's files: attached, downloaded, removed; at Post they become the voucher's. */
const DraftFiles = ({
  invoice,
  attachmentsEnabled,
}: {
  invoice: AccountingSupplierInvoice;
  attachmentsEnabled: boolean;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "supplier-invoice", invoice.id] });
  const upload = useMutation({
    mutationFn: (file: File) => uploadSupplierInvoiceFile(invoice.id, file),
    onSuccess: refresh,
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const remove = useMutation({
    mutationFn: (fileId: number) => deleteSupplierInvoiceFile(invoice.id, fileId),
    onSuccess: refresh,
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Stack gap="xs">
      <Text fw={600}>{t("supplierInvoiceFiles")}</Text>
      {invoice.files.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("supplierInvoiceNeedsFile")}
        </Text>
      )}
      {invoice.files.map((f) => (
        <Group key={f.id} gap="xs">
          <IconPaperclip size={14} />
          <Anchor href={supplierInvoiceFileUrl(invoice.id, f.id)}>{f.fileName}</Anchor>
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={t("removeFile", { name: f.fileName })}
            onClick={() => remove.mutate(f.id)}
          >
            <IconTrash size={14} />
          </ActionIcon>
        </Group>
      ))}
      {attachmentsEnabled ? (
        <FileInput
          aria-label={t("attachFile")}
          placeholder={t("attachFile")}
          value={null}
          onChange={(file) => file && upload.mutate(file)}
          disabled={upload.isPending}
        />
      ) : (
        <Alert color="orange">{t("attachmentsUnavailable")}</Alert>
      )}
    </Stack>
  );
};

/** What is posted (A18, A19): the lines, the voucher, the payments, Pay and Reverse and copy. */
const PostedInvoice = ({ meta, invoice }: { meta: AccountingMeta; invoice: AccountingSupplierInvoice }) => {
  const { t, money, amount, day } = useAccountingFormat();
  const [paying, setPaying] = useState(false);
  const [reversing, setReversing] = useState(false);
  const queryClient = useQueryClient();
  const removePayment = useMutation({
    mutationFn: (paymentId: number) => removeSupplierPayment(invoice.id, paymentId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("paymentRemoved") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const owed = invoice.balance !== null && invoice.balance !== "0.00";
  return (
    <Stack gap="lg">
      <PageHeader
        title={`${invoice.kind === "credit_note" ? t("creditNote") : t("supplierInvoice")} ${invoice.invoiceNumber}`}
        description={`${invoice.supplierName} · ${day(invoice.invoiceDate)}`}
      />
      <Group>
        <StateBadge invoice={invoice} />
        <PurchaseMatchBadge invoice={invoice} />
        <Anchor component={Link} to={supplierPath(invoice.supplierId)}>
          {invoice.supplierName}
        </Anchor>
        {invoice.voucher && (
          <Anchor component={Link} to={voucherPath(invoice.voucher.id)}>
            {t("voucher")} {invoice.voucher.display}
          </Anchor>
        )}
        {invoice.reversalVoucher && (
          <Anchor component={Link} to={voucherPath(invoice.reversalVoucher.id)}>
            {t("reversal")} {invoice.reversalVoucher.display}
          </Anchor>
        )}
      </Group>
      <SimpleGrid cols={{ base: 1, sm: 4 }}>
        <Text size="sm">
          {t("total")}: {invoice.currency !== "NOK" ? `${amount(invoice.total)} ${invoice.currency} · ` : ""}
          {money(invoice.totalNok)}
        </Text>
        {invoice.currency !== "NOK" && (
          <Text size="sm">
            {t("exchangeRate", { currency: invoice.currency })}: {invoice.exchangeRate ?? "—"}
          </Text>
        )}
        <Text size="sm">
          {t("dueDate")}: {day(invoice.dueDate)}
        </Text>
        <Text size="sm" data-testid="supplier-invoice-balance">
          {t("balance")}: {money(invoice.balance)}
        </Text>
      </SimpleGrid>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("taxCode")}</Table.Th>
            <Table.Th>{t("text")}</Table.Th>
            <Table.Th ta="right">{t("net")}</Table.Th>
            <Table.Th ta="right">{t("vat")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {invoice.lines.map((l) => (
            <Table.Tr key={l.id}>
              <Table.Td>
                {l.accountNumber} {l.accountName}
              </Table.Td>
              <Table.Td>{l.taxCode}</Table.Td>
              <Table.Td>{l.text}</Table.Td>
              <Table.Td ta="right">{amount(l.net)}</Table.Td>
              <Table.Td ta="right">{amount(l.vat)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Text fw={600}>{t("payments")}</Text>
      {invoice.payments.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("noPayments")}
        </Text>
      ) : (
        <Table data-testid="supplier-payments">
          <Table.Tbody>
            {invoice.payments.map((p) => (
              <Table.Tr key={p.id}>
                <Table.Td>{day(p.paidOn)}</Table.Td>
                <Table.Td ta="right">{money(p.amountNok)}</Table.Td>
                {invoice.currency !== "NOK" && (
                  <Table.Td ta="right">
                    {amount(p.amountCurrency)} {invoice.currency}
                  </Table.Td>
                )}
                <Table.Td>{p.bankAccountNumber}</Table.Td>
                <Table.Td>
                  <Anchor component={Link} to={voucherPath(p.voucher.id)}>
                    {p.voucher.display}
                  </Anchor>
                </Table.Td>
                <Table.Td>
                  {p.currencyDifference !== "0.00" &&
                    t(Number(p.currencyDifference) > 0 ? "currencyLoss" : "currencyGain", {
                      amount: amount(p.currencyDifference.replace("-", "")),
                    })}
                </Table.Td>
                <Table.Td ta="right">
                  {p.removedAt ? (
                    <Badge variant="light" color="gray">
                      {t("removed")}
                    </Badge>
                  ) : (
                    meta.canPost && (
                      <Button
                        size="compact-sm"
                        variant="subtle"
                        color="red"
                        loading={removePayment.isPending && removePayment.variables === p.id}
                        onClick={() => removePayment.mutate(p.id)}
                      >
                        {t("removePayment")}
                      </Button>
                    )
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {meta.canPost && invoice.state !== "reversed" && (
        <Group>
          {owed && <Button onClick={() => setPaying(true)}>{t("registerPayment")}</Button>}
          <Button variant="default" onClick={() => setReversing(true)}>
            {t("reverseAndCopy")}
          </Button>
        </Group>
      )}
      {paying && <PaymentModal invoice={invoice} today={meta.today} onClose={() => setPaying(false)} />}
      {reversing && <ReverseCopyModal invoice={invoice} today={meta.today} onClose={() => setReversing(false)} />}
    </Stack>
  );
};

/** A payment (A19, R8): the date, the NOK the bank took, the share settled in the currency, the bank account. */
const PaymentModal = ({
  invoice,
  today,
  onClose,
}: {
  invoice: AccountingSupplierInvoice;
  today: string;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const left = (invoice.balance ?? "0.00").replace("-", "");
  const leftCurrency = (invoice.balanceCurrency ?? "0.00").replace("-", "");
  const foreign = invoice.currency !== "NOK";
  const [date, setDate] = useState(today);
  const [nok, setNok] = useState(foreign ? "" : left);
  const [share, setShare] = useState(leftCurrency);
  const [bank, setBank] = useState<string | null>(null);
  const pay = useMutation({
    mutationFn: () =>
      paySupplierInvoice(invoice.id, {
        date,
        amountNok: nok.trim().replace(",", "."),
        amountCurrency: foreign ? share.trim() || null : null,
        bankAccountId: bank ? Number(bank) : null,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("paymentRegistered") });
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("registerPayment")}>
      <Stack>
        <TextInput type="date" label={t("date")} value={date} onChange={(e) => setDate(e.currentTarget.value)} />
        <TextInput
          label={t("amountNokPaid")}
          description={t("amountNokPaidHint")}
          value={nok}
          onChange={(e) => setNok(e.currentTarget.value)}
        />
        {foreign && (
          <TextInput
            label={t("amountSettled", { currency: invoice.currency })}
            description={t("amountSettledHint", { left: leftCurrency, currency: invoice.currency })}
            value={share}
            onChange={(e) => setShare(e.currentTarget.value)}
          />
        )}
        <AccountSelect
          label={t("bankAccount")}
          description={t("bankAccountHint")}
          accountFilter={(a) => a.kind === "asset"}
          value={bank}
          onChange={setBank}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={pay.isPending} disabled={!nok.trim()} onClick={() => pay.mutate()}>
            {t("registerPayment")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Reverse and copy (A18): the reversal and a new draft with the same lines and files. */
const ReverseCopyModal = ({
  invoice,
  today,
  onClose,
}: {
  invoice: AccountingSupplierInvoice;
  today: string;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [date, setDate] = useState(today);
  const [reason, setReason] = useState("");
  const go = useMutation({
    mutationFn: () => reverseAndCopySupplierInvoice(invoice.id, { date, reason: reason.trim() }),
    onSuccess: async (copy) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("reversedAndCopied") });
      onClose();
      await navigate({ to: supplierInvoicePath(copy.id) });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("reverseAndCopy")}>
      <Stack>
        <Text size="sm">{t("reverseAndCopyHint")}</Text>
        <TextInput type="date" label={t("date")} value={date} onChange={(e) => setDate(e.currentTarget.value)} />
        <TextInput
          label={t("reason")}
          required
          maxLength={500}
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={go.isPending} disabled={!reason.trim()} onClick={() => go.mutate()}>
            {t("reverseAndCopy")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
