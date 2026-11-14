import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  SegmentedControl,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type AccountingOpenItem, openItemsQueryOptions, settleOpenItem } from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";

/** The party ledgers' open items (A10), and a receipt, payment or refund on one (R5). */
export const OpenItemsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navOpenItems")} description={t("openItemsDescription")} />
          <OpenItems canPost={meta.canPost} today={meta.today} />
        </Stack>
      )}
    </BooksGate>
  );
};

const OpenItems = ({ canPost, today }: { canPost: boolean; today: string }) => {
  const { t, day, money } = useAccountingFormat();
  const [kind, setKind] = useState<"customer" | "supplier">("customer");
  const [settled, setSettled] = useState(false);
  const [settling, setSettling] = useState<AccountingOpenItem | null>(null);
  const items = useQuery(openItemsQueryOptions({ kind, open: !settled }));
  return (
    <Stack gap="md">
      <Group>
        <SegmentedControl
          value={kind}
          onChange={(value) => setKind(value === "supplier" ? "supplier" : "customer")}
          data={[
            { value: "customer", label: t("customers") },
            { value: "supplier", label: t("suppliers") },
          ]}
        />
        <Checkbox label={t("showSettled")} checked={settled} onChange={(e) => setSettled(e.currentTarget.checked)} />
      </Group>
      {items.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {items.error.message}
        </Alert>
      ) : items.isPending ? (
        <ContentSkeleton rows={5} />
      ) : items.data.length === 0 ? (
        <EmptyState icon={IconCash} title={t("noOpenItems")} description={t("noOpenItemsHint")} />
      ) : (
        <Table striped data-assist="accounting.open-items.table">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("party")}</Table.Th>
              <Table.Th>{t("document")}</Table.Th>
              <Table.Th>{t("documentDate")}</Table.Th>
              <Table.Th>{t("dueDate")}</Table.Th>
              <Table.Th ta="right">{t("originalAmount")}</Table.Th>
              <Table.Th ta="right">{t("balance")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.data.map((item) => (
              <Table.Tr key={item.id}>
                <Table.Td>{item.customerName ?? item.customerId ?? item.supplierId}</Table.Td>
                <Table.Td>
                  {item.unallocated ? t("unallocated") : item.documentNumber}
                  {item.sourceDocumentId !== null && (
                    <Badge ml="xs" variant="light">
                      {t("fromInvoices")}
                    </Badge>
                  )}
                </Table.Td>
                <Table.Td>{day(item.documentDate)}</Table.Td>
                <Table.Td>{day(item.dueDate)}</Table.Td>
                <Table.Td ta="right">{money(item.originalAmount)}</Table.Td>
                <Table.Td ta="right">{money(item.balance)}</Table.Td>
                <Table.Td>
                  {canPost && !item.settled && item.sourceDocumentId === null && (
                    <Button size="xs" variant="light" onClick={() => setSettling(item)}>
                      {t("settle")}
                    </Button>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {settling && <SettleDialog item={settling} today={today} onClose={() => setSettling(null)} />}
    </Stack>
  );
};

const SettleDialog = ({ item, today, onClose }: { item: AccountingOpenItem; today: string; onClose: () => void }) => {
  const { t, money } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [date, setDate] = useState(today);
  const [amount, setAmount] = useState(item.balance.replace("-", ""));
  const [bank, setBank] = useState<string | null>(null);
  const [text, setText] = useState("");
  const settle = useMutation({
    mutationFn: () =>
      settleOpenItem(item.id, {
        date,
        amount: amount.replace(",", "."),
        bankAccountId: bank ? Number(bank) : null,
        text,
      }),
    onSuccess: async (voucher) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("posted", { voucher: voucher.display }) });
      onClose();
    },
  });
  const what =
    item.kind === "customer"
      ? item.balance.startsWith("-")
        ? "refund"
        : "receipt"
      : item.balance.startsWith("-")
        ? "payment"
        : "refund";
  return (
    <Modal opened onClose={onClose} title={t(`settle_${what}`)}>
      <Stack gap="sm">
        <Text size="sm">{t("settleBalance", { amount: money(item.balance) })}</Text>
        <TextInput type="date" label={t("date")} value={date} onChange={(e) => setDate(e.currentTarget.value)} />
        <TextInput label={t("amount")} value={amount} onChange={(e) => setAmount(e.currentTarget.value)} />
        <AccountSelect
          label={t("bankAccount")}
          description={t("bankAccountHint")}
          value={bank}
          onChange={setBank}
          accountFilter={(a) => a.kind === "asset"}
        />
        <TextInput label={t("text")} value={text} onChange={(e) => setText(e.currentTarget.value)} />
        {settle.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
            {refusalMessage(t, settle.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => settle.mutate()} loading={settle.isPending}>
            {t("register")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
