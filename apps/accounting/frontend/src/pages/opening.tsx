import { ActionIcon, Alert, Anchor, Button, Group, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconFileImport, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type AccountingMeta, accountsQueryOptions, openingQueryOptions, postOpening } from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, CustomerSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { fromOre, toOre } from "../lib/journal";
import { voucherPath } from "../lib/routes";

/** One opening line as typed. */
interface OpeningLine {
  key: string;
  accountId: string | null;
  debit: string;
  credit: string;
  customerId: string;
  documentNumber: string;
  documentDate: string;
  dueDate: string;
  sourceDocumentId?: number;
}

let opened = 0;
const blank = (): OpeningLine => {
  opened += 1;
  return {
    key: `o-${opened}`,
    accountId: null,
    debit: "",
    credit: "",
    customerId: "",
    documentNumber: "",
    documentDate: "",
    dueDate: "",
  };
};

const signedOre = (line: OpeningLine): bigint | undefined => {
  const debit = line.debit.trim() ? toOre(line.debit) : undefined;
  const credit = line.credit.trim() ? toOre(line.credit) : undefined;
  if (debit !== undefined && credit === undefined) return debit;
  if (credit !== undefined && debit === undefined) return -credit;
  return undefined;
};

const signed = (line: OpeningLine): string | undefined => {
  const ore = signedOre(line);
  return ore === undefined ? undefined : fromOre(ore);
};

/**
 * The opening balances (A9): one voucher dated the books' first day; a
 * receivables line per open document, so the customer ledger starts with
 * real items. The open invoices Invoices knows at that day are offered.
 * The difference goes to the opening account.
 */
export const OpeningPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("openingBalances")} description={t("openingDescription")} />
          <Opening meta={meta} />
        </Stack>
      )}
    </BooksGate>
  );
};

const Opening = ({ meta }: { meta: AccountingMeta }) => {
  const { t, day, money } = useAccountingFormat();
  const opening = useQuery(openingQueryOptions());
  const accounts = useQuery(accountsQueryOptions());
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<OpeningLine[]>(() => [blank(), blank()]);
  const post = useMutation({
    mutationFn: () =>
      postOpening({
        lines: lines
          .filter((l) => l.accountId && signed(l))
          .map((l) => ({
            accountId: Number(l.accountId),
            amount: signed(l) ?? "",
            customerId: l.customerId ? Number(l.customerId) : null,
            documentNumber: l.documentNumber || null,
            documentDate: l.documentDate || null,
            dueDate: l.dueDate || null,
            sourceDocumentId: l.sourceDocumentId ?? null,
          })),
      }),
    onSuccess: async (voucher) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("posted", { voucher: voucher.display }) });
      await navigate({ to: voucherPath(voucher.id) });
    },
  });
  if (opening.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {opening.error.message}
      </Alert>
    );
  if (opening.isPending) return <ContentSkeleton rows={4} />;
  const o = opening.data;
  if (o.voucher)
    return (
      <Alert color="blue" title={t("openingEntered")}>
        <Text size="sm">
          <Anchor component={Link} to={voucherPath(o.voucher.id)}>
            {o.voucher.display}
          </Anchor>{" "}
          {o.locked ? t("openingFinal") : t("openingReversible")}
        </Text>
      </Alert>
    );
  if (!o.canEnter || !meta.canClose)
    return <Alert color="yellow">{o.locked ? t("openingFinal") : t("notAllowed_close")}</Alert>;
  const receivables = accounts.data?.find((a) => a.systemRole === "receivables");
  const update = (key: string, change: Partial<OpeningLine>) =>
    setLines((current) => current.map((l) => (l.key === key ? { ...l, ...change } : l)));
  const isReceivables = (accountId: string | null) => receivables !== undefined && accountId === String(receivables.id);
  const addInvoices = () =>
    setLines((current) => [
      ...current.filter((l) => l.accountId || l.debit || l.credit),
      ...o.openInvoices
        .filter((invoice) => !current.some((l) => l.sourceDocumentId === invoice.documentId))
        .map((invoice) => ({
          ...blank(),
          accountId: receivables ? String(receivables.id) : null,
          debit: invoice.amount.startsWith("-") ? "" : invoice.amount,
          credit: invoice.amount.startsWith("-") ? invoice.amount.slice(1) : "",
          customerId: String(invoice.customerId),
          documentNumber: String(invoice.number),
          documentDate: invoice.issueDate,
          dueDate: invoice.dueDate,
          sourceDocumentId: invoice.documentId,
        })),
    ]);
  const total = lines.reduce((sum, l) => sum + (signedOre(l) ?? 0n), 0n);
  return (
    <Stack gap="md">
      <Text size="sm">{t("openingDated", { date: day(o.booksStart) })}</Text>
      {o.invoicesAvailable && o.openInvoices.length > 0 && (
        <Alert color="blue" title={t("openInvoicesFound", { count: o.openInvoices.length })}>
          <Button
            size="xs"
            variant="light"
            leftSection={<IconFileImport size={14} />}
            onClick={addInvoices}
            data-assist="accounting.opening.add-invoices"
          >
            {t("addOpenInvoices")}
          </Button>
        </Alert>
      )}
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("debit")}</Table.Th>
            <Table.Th>{t("credit")}</Table.Th>
            <Table.Th>{t("customer")}</Table.Th>
            <Table.Th>{t("document")}</Table.Th>
            <Table.Th>{t("documentDate")}</Table.Th>
            <Table.Th>{t("dueDate")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {lines.map((l) => (
            <Table.Tr key={l.key}>
              <Table.Td miw={200}>
                <AccountSelect
                  aria-label={t("account")}
                  value={l.accountId}
                  onChange={(value) => update(l.key, { accountId: value })}
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  aria-label={t("debit")}
                  w={110}
                  value={l.debit}
                  onChange={(e) => update(l.key, { debit: e.currentTarget.value })}
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  aria-label={t("credit")}
                  w={110}
                  value={l.credit}
                  onChange={(e) => update(l.key, { credit: e.currentTarget.value })}
                />
              </Table.Td>
              <Table.Td miw={180}>
                {isReceivables(l.accountId) && (
                  <CustomerSelect
                    aria-label={t("customer")}
                    value={l.customerId || null}
                    onChange={(value) => update(l.key, { customerId: value ?? "" })}
                  />
                )}
              </Table.Td>
              <Table.Td>
                {isReceivables(l.accountId) && (
                  <TextInput
                    aria-label={t("document")}
                    w={100}
                    value={l.documentNumber}
                    onChange={(e) => update(l.key, { documentNumber: e.currentTarget.value })}
                  />
                )}
              </Table.Td>
              <Table.Td>
                {isReceivables(l.accountId) && (
                  <TextInput
                    type="date"
                    aria-label={t("documentDate")}
                    value={l.documentDate}
                    onChange={(e) => update(l.key, { documentDate: e.currentTarget.value })}
                  />
                )}
              </Table.Td>
              <Table.Td>
                {isReceivables(l.accountId) && (
                  <TextInput
                    type="date"
                    aria-label={t("dueDate")}
                    value={l.dueDate}
                    onChange={(e) => update(l.key, { dueDate: e.currentTarget.value })}
                  />
                )}
              </Table.Td>
              <Table.Td>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={t("removeLine")}
                  onClick={() => setLines((c) => c.filter((x) => x.key !== l.key))}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Group justify="space-between">
        <Button variant="subtle" leftSection={<IconPlus size={16} />} onClick={() => setLines((c) => [...c, blank()])}>
          {t("addLine")}
        </Button>
        <Text size="sm">{t("toOpeningAccount", { amount: money(fromOre(-total)) })}</Text>
      </Group>
      {post.error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
          {refusalMessage(t, post.error)}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button onClick={() => post.mutate()} loading={post.isPending} data-assist="accounting.opening.post">
          {t("postOpening")}
        </Button>
      </Group>
    </Stack>
  );
};
