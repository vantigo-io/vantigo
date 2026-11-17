import { Alert, Anchor, Badge, Button, Group, Modal, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { PageSection } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import {
  type PurchaseOrder,
  type PurchasingInvoiceMatch,
  type PurchasingInvoiceRegistration,
  purchaseOrderInvoicesQueryOptions,
  purchasingMetaQueryOptions,
  registerPurchaseOrderInvoice,
} from "../api/purchasing";
import { ApiValidationError, PURCHASING_QUERY_KEY, refusalCode } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { newRequestId, today, usePurchasingFormat } from "../lib/format";
import { accountingSupplierLinkOptions } from "../lib/routes";

/** A match's warnings, in the reader's words (P38). */
export const MatchWarnings = ({ match }: { match: PurchasingInvoiceMatch }) => {
  const { t } = usePurchasingFormat();
  if (match.warnings.length === 0) return null;
  return (
    <Group gap={4}>
      {match.warnings.map((w) => (
        <Badge key={w} size="xs" color="orange" variant="light" data-testid="match-warning">
          {t(`invoiceWarning.${w}`)}
        </Badge>
      ))}
    </Group>
  );
};

/** A match's state: provisional while the invoice is a draft, posted once it is posted (P37). */
export const MatchState = ({ state }: { state: string }) => {
  const { t } = usePurchasingFormat();
  return (
    <Badge size="xs" variant="light" color={state === "posted" ? "green" : "gray"}>
      {t(state === "posted" ? "matchPosted" : "matchProvisional")}
    </Badge>
  );
};

/** The matches grouped by invoice, in the order they came. */
const byInvoice = (matches: readonly PurchasingInvoiceMatch[]) => {
  const groups = new Map<number, PurchasingInvoiceMatch[]>();
  for (const m of matches) groups.set(m.supplierInvoiceId, [...(groups.get(m.supplierInvoiceId) ?? []), m]);
  return [...groups.values()];
};

/**
 * The order's supplier invoices (P37, P40, A20): the matches grouped by
 * invoice, with their quantities, state and warnings; the invoice lines'
 * amounts only when the caller holds accounting:read. Register invoice for a
 * holder of purchasing:invoices-match and accounting:post (A1).
 */
export const OrderInvoicesCard = ({ order, canRegister }: { order: PurchaseOrder; canRegister: boolean }) => {
  const { t, date, quantity, money, unitPrice } = usePurchasingFormat();
  const invoices = useQuery(purchaseOrderInvoicesQueryOptions(order.id));
  const [registering, setRegistering] = useState(false);
  const billable = order.lines.some((l) => {
    const goods = l.kind === "variant" && l.productType === "Goods";
    const billed = goods ? Number(l.received) : l.deliveredAt ? Number(l.quantity) : 0;
    return billed > Number(l.invoiced);
  });
  const matches = invoices.data?.matches ?? [];
  const visible = invoices.data?.linesVisible === true;
  return (
    <PageSection>
      <Stack gap="sm" data-testid="order-invoices">
        <Group justify="space-between">
          <Title order={4}>{t("supplierInvoices")}</Title>
          {canRegister && (
            <Button variant="default" disabled={!billable} onClick={() => setRegistering(true)}>
              {t("registerInvoice")}
            </Button>
          )}
        </Group>
        {matches.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noInvoices")}
          </Text>
        ) : (
          byInvoice(matches).map((group) => {
            const first = group[0];
            return (
              <Stack key={first.supplierInvoiceId} gap={4} data-testid="order-invoice">
                <Group gap="xs">
                  <Text fw={600} size="sm">
                    {t(first.kind === "credit_note" ? "creditNoteNumber" : "invoiceNumberTitle", {
                      number: first.invoiceNumber,
                    })}
                  </Text>
                  <Text size="sm" c="dimmed">
                    {date(first.invoiceDate)}
                  </Text>
                  <MatchState state={first.state} />
                </Group>
                <Table>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("product")}</Table.Th>
                      <Table.Th ta="right">{t("matchedQuantity")}</Table.Th>
                      <Table.Th ta="right">{t("orderUnitNet")}</Table.Th>
                      {visible && <Table.Th ta="right">{t("invoiceUnitNet")}</Table.Th>}
                      {visible && <Table.Th ta="right">{t("invoiceLineNet")}</Table.Th>}
                      <Table.Th />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {group.map((m) => (
                      <Table.Tr key={m.id}>
                        <Table.Td>{m.name}</Table.Td>
                        <Table.Td ta="right">{`${quantity(m.quantity)} ${m.unit}`}</Table.Td>
                        <Table.Td ta="right">{unitPrice(m.orderUnitNet, order.currency)}</Table.Td>
                        {visible && (
                          <Table.Td ta="right">
                            {m.invoiceUnitNet ? unitPrice(m.invoiceUnitNet, m.currency) : ""}
                          </Table.Td>
                        )}
                        {visible && <Table.Td ta="right">{m.lineNet ? money(m.lineNet, m.currency) : ""}</Table.Td>}
                        <Table.Td>
                          <MatchWarnings match={m} />
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Stack>
            );
          })
        )}
      </Stack>
      {registering && <RegisterInvoiceDialog order={order} onClose={() => setRegistering(false)} />}
    </PageSection>
  );
};

/**
 * Register invoice (P37, A1, A11): what Accounting's draft needs that the
 * order does not. The clientRequestId belongs to the open dialog, so a retry
 * after a lost answer replays the first draft, never makes a second.
 */
export const RegisterInvoiceDialog = ({ order, onClose }: { order: PurchaseOrder; onClose: () => void }) => {
  const { t, money } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(purchasingMetaQueryOptions());
  const requestId = useRef(newRequestId());
  const [invoiceNumber, setInvoiceNumber] = useState("");
  const [invoiceDate, setInvoiceDate] = useState<string | null>(today());
  const [dueDate, setDueDate] = useState<string | null>(null);
  const [exchangeRate, setExchangeRate] = useState("");
  const [totalNok, setTotalNok] = useState("");
  const [total, setTotal] = useState("");
  const [result, setResult] = useState<PurchasingInvoiceRegistration | null>(null);
  const foreign = order.currency !== (meta.data?.baseCurrency ?? "NOK");
  const register = useMutation({
    mutationFn: () =>
      registerPurchaseOrderInvoice(order.id, {
        clientRequestId: requestId.current,
        invoiceNumber: invoiceNumber.trim(),
        invoiceDate: invoiceDate ?? "",
        dueDate: dueDate || null,
        exchangeRate: foreign && exchangeRate.trim() ? exchangeRate.trim() : null,
        totalNok: foreign && totalNok.trim() ? totalNok.trim() : null,
        total: total.trim() ? total.trim() : null,
      }),
    onSuccess: async (made) => {
      setResult(made);
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("invoiceRegistered") });
    },
  });
  const error = register.error;
  const fieldError = (field: string) => (error instanceof ApiValidationError ? error.errors[field]?.[0] : undefined);
  const code = refusalCode(error);
  return (
    <Modal opened onClose={onClose} title={t("registerInvoiceTitle")}>
      {result ? (
        <Stack data-testid="invoice-registered">
          <Text size="sm">
            {t("invoiceRegisteredBody", { lines: result.lines, total: money(result.total, result.currency) })}
          </Text>
          {result.linesLeft > 0 && (
            <Alert color="yellow" variant="light">
              {t("invoicedInParts", { count: result.linesLeft })}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button onClick={onClose}>{t("close")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack>
          <Text size="sm">{t("registerInvoiceBody")}</Text>
          <TextInput
            label={t("supplierInvoiceNumber")}
            required
            maxLength={60}
            value={invoiceNumber}
            onChange={(e) => setInvoiceNumber(e.currentTarget.value)}
            error={fieldError("invoiceNumber")}
          />
          <DateInput
            label={t("invoiceDate")}
            required
            valueFormat="YYYY-MM-DD"
            value={invoiceDate}
            onChange={setInvoiceDate}
            error={fieldError("invoiceDate")}
          />
          <DateInput
            label={t("dueDate")}
            description={t("dueDateHint")}
            clearable
            valueFormat="YYYY-MM-DD"
            value={dueDate}
            onChange={setDueDate}
            error={fieldError("dueDate")}
          />
          <TextInput
            label={t("invoiceTotal", { currency: order.currency })}
            description={t("invoiceTotalHint")}
            value={total}
            onChange={(e) => setTotal(e.currentTarget.value)}
            error={fieldError("total")}
          />
          {foreign && (
            <>
              <TextInput
                label={t("exchangeRate", { currency: order.currency })}
                description={t("exchangeRateHint")}
                value={exchangeRate}
                onChange={(e) => setExchangeRate(e.currentTarget.value)}
                error={fieldError("exchangeRate")}
              />
              <TextInput
                label={t("totalNok")}
                value={totalNok}
                onChange={(e) => setTotalNok(e.currentTarget.value)}
                error={fieldError("totalNok")}
              />
            </>
          )}
          {error && !(error instanceof ApiValidationError) && (
            <Alert color="red" variant="light" data-testid="register-error">
              <Text size="sm">{refusalMessage(error, t)}</Text>
              {code === "supplier_account_missing" && meta.data?.canOpenAccountingSuppliers && (
                <Anchor
                  size="sm"
                  renderRoot={(props) => <Link {...accountingSupplierLinkOptions(order.supplierId)} {...props} />}
                >
                  {t("openSupplierInAccounting")}
                </Anchor>
              )}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button
              loading={register.isPending}
              disabled={!invoiceNumber.trim() || !invoiceDate}
              onClick={() => register.mutate()}
            >
              {t("registerInvoice")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
};
