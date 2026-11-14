import { Alert, Anchor, Badge, Button, Checkbox, Group, Modal, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChecks, IconRefresh } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingUnposted,
  acknowledgeUnposted,
  enterNokAmounts,
  makeSupplierInvoiceFromUnposted,
  retryUnposted,
  unpostedQueryOptions,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { accountingCatalog } from "../i18n";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { invoicePath, supplierInvoicePath, voucherPath } from "../lib/routes";

/**
 * Needs attention (A15, R2): the facts the books could not post — an unknown
 * tax code, an inactive account, a kind this version does not know, an
 * expense in another currency waiting for its NOK amount, an expense of the
 * supplier invoice kind waiting to be made into one — each with why and its
 * action; and, apart, the rows that posted but say something (VAT not
 * deducted), which are acknowledged. Nothing that waits is dismissed: a fact
 * the books cannot hold is fixed, never waved away.
 */
export const NeedsAttentionPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navNeedsAttention")} description={t("needsAttentionDescription")} />
          <Unposted canPost={meta.canPost} />
        </Stack>
      )}
    </BooksGate>
  );
};

/** A reason in words: the module's own for a reason it knows, the refusal's for an engine's code. */
const useReasonWords = () => {
  const { t } = useAccountingFormat();
  const en = accountingCatalog.en as Record<string, string>;
  return (reason: string) =>
    `unpostedReason_${reason}` in en
      ? t(`unpostedReason_${reason}`)
      : `refusal.${reason}` in en
        ? t(`refusal.${reason}`)
        : reason;
};

const Unposted = ({ canPost }: { canPost: boolean }) => {
  const { t, day, money } = useAccountingFormat();
  const reasonWords = useReasonWords();
  const [all, setAll] = useState(false);
  const items = useQuery(unpostedQueryOptions({ open: !all }));
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [entering, setEntering] = useState<AccountingUnposted | null>(null);
  const makeInvoice = useMutation({
    mutationFn: (id: number) => makeSupplierInvoiceFromUnposted(id),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      await navigate({ to: supplierInvoicePath(draft.id) });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const acknowledge = useMutation({
    mutationFn: (id: number) => acknowledgeUnposted(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] }),
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const retry = useMutation({
    mutationFn: (id: number) => retryUnposted(id),
    onSuccess: async (item: AccountingUnposted) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show(
        item.postedVoucher
          ? { color: "green", message: t("retryPosted", { voucher: item.postedVoucher.display }) }
          : { color: "orange", message: t("retryFailed", { reason: reasonWords(item.reason) }) },
      );
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Stack gap="md">
      <Checkbox label={t("showPosted")} checked={all} onChange={(e) => setAll(e.currentTarget.checked)} />
      {items.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {items.error.message}
        </Alert>
      ) : items.isPending ? (
        <ContentSkeleton rows={4} />
      ) : items.data.items.length === 0 ? (
        <EmptyState icon={IconChecks} title={t("nothingNeedsAttention")} description={t("needsAttentionEmptyHint")} />
      ) : (
        <Table striped data-testid="unposted">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("date")}</Table.Th>
              <Table.Th>{t("unpostedFact")}</Table.Th>
              <Table.Th>{t("customer")}</Table.Th>
              <Table.Th ta="right">{t("amount")}</Table.Th>
              <Table.Th>{t("reason")}</Table.Th>
              <Table.Th>{t("attempts")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.data.items.map((u) => (
              <Table.Tr key={u.id}>
                <Table.Td>{day(u.factDate)}</Table.Td>
                <Table.Td>
                  {t(
                    `sourceKind_${u.source.kind}` in (accountingCatalog.en as object)
                      ? `sourceKind_${u.source.kind}`
                      : "sourceKind_other",
                    {
                      kind: u.source.kind,
                    },
                  )}{" "}
                  {u.documentId !== null && u.documentNumber !== null ? (
                    <Anchor href={appUrl(invoicePath(u.documentId))}>{u.documentNumber}</Anchor>
                  ) : u.source.module === "expenses" ? (
                    u.source.id
                  ) : (
                    u.documentNumber
                  )}
                </Table.Td>
                <Table.Td>{u.customerName ?? (u.customerId ? t("customerNumber", { id: u.customerId }) : "")}</Table.Td>
                <Table.Td ta="right">{money(u.amount)}</Table.Td>
                <Table.Td>
                  {u.informational && (
                    <Badge variant="light" color="blue" mr="xs">
                      {t("informational")}
                    </Badge>
                  )}
                  <Text size="sm">{reasonWords(u.reason)}</Text>
                  {u.detail && (
                    <Text size="xs" c="dimmed">
                      {u.detail}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td>{u.attempts}</Table.Td>
                <Table.Td ta="right">
                  {u.postedVoucher ? (
                    <Group justify="flex-end" gap="xs">
                      <Anchor component={Link} to={voucherPath(u.postedVoucher.id)}>
                        {u.postedVoucher.display}
                      </Anchor>
                      {canPost && u.informational && !u.resolvedAt && (
                        <Button
                          size="xs"
                          variant="subtle"
                          loading={acknowledge.isPending && acknowledge.variables === u.id}
                          onClick={() => acknowledge.mutate(u.id)}
                        >
                          {t("acknowledge")}
                        </Button>
                      )}
                    </Group>
                  ) : u.supplierInvoiceId ? (
                    <Anchor component={Link} to={supplierInvoicePath(u.supplierInvoiceId)}>
                      {t("openSupplierInvoice")}
                    </Anchor>
                  ) : canPost && u.reason === "enter_as_supplier_invoice" ? (
                    <Button
                      size="xs"
                      variant="light"
                      loading={makeInvoice.isPending && makeInvoice.variables === u.id}
                      onClick={() => makeInvoice.mutate(u.id)}
                    >
                      {t("makeSupplierInvoice")}
                    </Button>
                  ) : canPost && u.reason === "amount_in_nok_needed" ? (
                    <Button size="xs" variant="light" onClick={() => setEntering(u)}>
                      {t("enterNokAmount")}
                    </Button>
                  ) : (
                    canPost && (
                      <Group justify="flex-end">
                        <Button
                          size="xs"
                          variant="light"
                          leftSection={<IconRefresh size={14} />}
                          loading={retry.isPending && retry.variables === u.id}
                          aria-label={t("retryOf", { document: u.documentNumber ?? u.source.id })}
                          onClick={() => retry.mutate(u.id)}
                        >
                          {t("retry")}
                        </Button>
                      </Group>
                    )
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {entering && <NokAmountsModal row={entering} onClose={() => setEntering(null)} />}
    </Stack>
  );
};

/** The NOK amount of each line of an expense in another currency (R2), from the bank statement. */
const NokAmountsModal = ({ row, onClose }: { row: AccountingUnposted; onClose: () => void }) => {
  const { t, amount } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [values, setValues] = useState<Record<number, string>>(
    Object.fromEntries(row.expenseLines.map((l) => [l.entryId, l.amountNok ?? ""])),
  );
  const save = useMutation({
    mutationFn: () =>
      enterNokAmounts(
        row.id,
        row.expenseLines.map((l) => ({
          entryId: l.entryId,
          amountNok: (values[l.entryId] ?? "").trim().replace(",", "."),
        })),
      ),
    onSuccess: async (item) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show(
        item.postedVoucher
          ? { color: "green", message: t("retryPosted", { voucher: item.postedVoucher.display }) }
          : { color: "orange", message: t("retryFailed", { reason: item.reason }) },
      );
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("enterNokAmount")}>
      <Stack>
        <Text size="sm">{t("enterNokAmountHint")}</Text>
        {row.expenseLines.map((l) => (
          <TextInput
            key={l.entryId}
            label={t("expenseLineInCurrency", { id: l.entryId, amount: amount(l.gross), currency: l.currency })}
            value={values[l.entryId] ?? ""}
            onChange={(e) => {
              const v = e.currentTarget.value;
              setValues((x) => ({ ...x, [l.entryId]: v }));
            }}
          />
        ))}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            loading={save.isPending}
            disabled={row.expenseLines.some((l) => !(values[l.entryId] ?? "").trim())}
            onClick={() => save.mutate()}
          >
            {t("enterAndPost")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
