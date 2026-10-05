import { Alert, Button, Checkbox, Group, Modal, NumberInput, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { customerLanguageQueryOptions } from "../api/customers";
import { type Deductible, deductibleQueryOptions } from "../api/deductible";
import { vatCodesQueryOptions } from "../api/vat-codes";
import { invoicesCatalog } from "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/** A deduction line as the step proposes it (D7): the editor adds it with quantity -1 and no discount. */
export interface ProposedDeduction {
  description: string;
  unitPrice: number;
  vatCodeId: number;
  deductsInvoiceId: number;
}

export interface DeductModalProps {
  /** The invoice draft the deductions go on. */
  invoiceId: number;
  currency: string;
  /** The draft's customer, whose billing profile says the language its lines are written in. */
  customerId: number;
  /** The draft's buyer snapshot's language, when it has one (a draft rarely does). */
  buyerLanguage?: string;
  /** Whether the caller may read the customer's billing profile (`customers:view`). */
  canViewCustomers: boolean;
  /** The (invoice, VAT code) pairs the draft already deducts: one line each, never two (409 deduction_duplicated). */
  taken: { invoiceId: number; vatCodeId: number | null }[];
  onAdd: (lines: ProposedDeduction[]) => void;
  onClose: () => void;
}

const keyOf = (d: { invoiceId: number; vatCodeId: number | null }) => `${d.invoiceId}:${d.vatCodeId}`;

/**
 * The editor's "Deduct earlier invoices" step (invoices work design D7): what
 * each of the customer's issued invoices — the a-kontos — has left to deduct,
 * per VAT code, from `GET /{id}/deductible`. Each row can be chosen with the
 * amount to deduct, at most what is left and more than zero; the editor then
 * adds one line per row — "Previously invoiced on account, invoice n", -1 at
 * the amount, the row's VAT code — which the save sends with
 * `deductsInvoiceId`. A pair the draft already deducts is shown, not offered.
 */
export const DeductModal = ({
  invoiceId,
  currency,
  customerId,
  buyerLanguage,
  canViewCustomers,
  taken,
  onAdd,
  onClose,
}: DeductModalProps) => {
  const { t, money, percent, date } = useInvoiceFormat();
  // The proposed text is in the language the wizard writes the draft's other
  // lines in — the buyer's: the snapshot's, else the billing profile's (the
  // server's rule: English for "en", Norwegian otherwise) — so one draft's
  // lines speak one language. Without either, the reader's.
  const profile = useQuery({
    ...customerLanguageQueryOptions(customerId),
    enabled: !buyerLanguage && canViewCustomers,
  });
  const language = buyerLanguage ? (buyerLanguage === "en" ? "en" : "nb") : profile.data;
  const lineText = (number: number) =>
    language
      ? invoicesCatalog[language].deductionLineText.replace("{{number}}", String(number))
      : t("deductionLineText", { number });
  const deductible = useQuery(deductibleQueryOptions(invoiceId));
  const codes = useQuery(vatCodesQueryOptions());
  const [chosen, setChosen] = useState<Record<string, number | string>>({});
  const takenKeys = new Set(taken.map(keyOf));
  const codeName = (row: Deductible) => {
    const code = codes.data?.find((c) => c.id === row.vatCodeId);
    return code ? t("vatCodeOption", { code: code.code, name: code.name }) : String(row.vatCodeId);
  };
  const rows = deductible.data ?? [];
  const picked = rows.filter((row) => chosen[keyOf(row)] !== undefined);
  const valid = (row: Deductible) => {
    const amount = Number(chosen[keyOf(row)]);
    return Number.isFinite(amount) && amount > 0 && amount <= row.left;
  };
  return (
    <Modal opened onClose={onClose} title={t("deductEarlier")} size="xl">
      <Stack>
        <Text size="sm" c="dimmed">
          {t("deductEarlierHint")}
        </Text>
        {deductible.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadDeductible")}>
            {refusalMessage(deductible.error, t, date)}
          </Alert>
        )}
        {deductible.isPending && <ContentSkeleton rows={2} rowHeight={40} />}
        {deductible.data && rows.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("deductibleNone")}
          </Text>
        )}
        {rows.length > 0 && (
          <Table.ScrollContainer minWidth={640}>
            <Table aria-label={t("deductEarlier")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th />
                  <Table.Th>{t("deductInvoice")}</Table.Th>
                  <Table.Th>{t("issueDate")}</Table.Th>
                  <Table.Th>{t("vatCode")}</Table.Th>
                  <Table.Th ta="right">{t("vatPercent")}</Table.Th>
                  <Table.Th ta="right">{t("deductLeft")}</Table.Th>
                  <Table.Th>{t("deductAmount")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((row) => {
                  const key = keyOf(row);
                  const already = takenKeys.has(key);
                  const names = { number: row.number, code: codeName(row) };
                  return (
                    <Table.Tr key={key} data-deductible={key}>
                      <Table.Td>
                        <Checkbox
                          aria-label={t("deductRow", names)}
                          disabled={already}
                          checked={chosen[key] !== undefined}
                          onChange={(e) => {
                            const on = e.currentTarget.checked;
                            setChosen((current) => {
                              const next = { ...current };
                              if (on) next[key] = row.left;
                              else delete next[key];
                              return next;
                            });
                          }}
                        />
                      </Table.Td>
                      <Table.Td>{row.number}</Table.Td>
                      <Table.Td>{date(row.issueDate)}</Table.Td>
                      <Table.Td>{codeName(row)}</Table.Td>
                      <Table.Td ta="right">{percent(row.ratePercent)}</Table.Td>
                      <Table.Td ta="right">{money(row.left, currency)}</Table.Td>
                      <Table.Td>
                        {already ? (
                          <Text size="sm" c="dimmed">
                            {t("deductAlreadyOnDraft")}
                          </Text>
                        ) : (
                          chosen[key] !== undefined && (
                            <NumberInput
                              aria-label={t("deductAmountOf", names)}
                              decimalScale={2}
                              min={0.01}
                              max={row.left}
                              value={chosen[key]}
                              error={
                                valid(row) ? undefined : t("deductAmountInvalid", { left: money(row.left, currency) })
                              }
                              onChange={(v) => setChosen((current) => ({ ...current, [key]: v }))}
                            />
                          )
                        )}
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={picked.length === 0 || !picked.every(valid)}
            onClick={() =>
              onAdd(
                picked.map((row) => ({
                  description: lineText(row.number),
                  unitPrice: Number(chosen[keyOf(row)]),
                  vatCodeId: row.vatCodeId,
                  deductsInvoiceId: row.invoiceId,
                })),
              )
            }
          >
            {t("addDeductions")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
