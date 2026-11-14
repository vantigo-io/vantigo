import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  FileButton,
  Group,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowBackUp, IconCopy, IconPaperclip } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingMeta,
  type AccountingVoucher,
  attachmentUrl,
  reverseVoucher,
  uploadAttachment,
  voucherQueryOptions,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { JOURNAL_ROUTE_PATH, VOUCHERS_ROUTE_PATH, voucherPath } from "../lib/routes";

/** One voucher (A4, A7): its postings, its links, its attachments, and Reverse and Copy. */
export const VoucherPage = () => {
  const { voucherId } = useParams({ strict: false }) as { voucherId: string };
  return <BooksGate>{(meta) => <VoucherView id={Number(voucherId)} meta={meta} />}</BooksGate>;
};

const VoucherView = ({ id, meta }: { id: number; meta: AccountingMeta }) => {
  const { t, day, amount, stamp } = useAccountingFormat();
  const voucher = useQuery(voucherQueryOptions(id));
  const [reversing, setReversing] = useState(false);
  const queryClient = useQueryClient();
  const upload = useMutation({
    mutationFn: (file: File) => uploadAttachment(id, file),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("attached") });
    },
  });
  if (voucher.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {(voucher.error as { status?: number }).status === 404 ? t("voucherNotFound") : voucher.error.message}
      </Alert>
    );
  if (voucher.isPending) return <ContentSkeleton rows={6} />;
  const v = voucher.data;
  // The opening is reversed only by who may enter it (A9, M1).
  const canReverse =
    meta.canPost && !v.system && !v.reversedBy && !v.reverses && (v.kind !== "opening" || meta.canClose);
  const debit = v.postings.reduce((sum, p) => (p.amount.startsWith("-") ? sum : sum + Number(p.amount)), 0);
  return (
    <Stack gap="lg">
      <PageHeader
        title={`${t("voucher")} ${v.display}`}
        description={v.description}
        breadcrumbs={[{ label: t("navVouchers"), to: VOUCHERS_ROUTE_PATH }, { label: v.display }]}
        actions={
          <Group gap="xs">
            {meta.canPost && v.kind === "manual" && (
              <Button
                variant="light"
                leftSection={<IconCopy size={16} />}
                renderRoot={(props) => <Link to={JOURNAL_ROUTE_PATH} search={{ copy: v.id }} {...props} />}
                data-assist="accounting.voucher.copy"
              >
                {t("copy")}
              </Button>
            )}
            {canReverse && (
              <Button
                color="red"
                variant="light"
                leftSection={<IconArrowBackUp size={16} />}
                onClick={() => setReversing(true)}
                data-assist="accounting.voucher.reverse"
              >
                {t("reverse")}
              </Button>
            )}
          </Group>
        }
      />
      <SimpleGrid cols={{ base: 1, sm: 4 }}>
        <Fact label={t("voucherDate")} value={day(v.voucherDate)} />
        <Fact label={t("documentDate")} value={day(v.documentDate)} />
        <Fact label={t("kind")} value={t(`voucherKind_${v.kind}`)} />
        <Fact label={t("postedAt")} value={stamp(v.postedAt)} />
      </SimpleGrid>
      {v.system && v.source && (
        <Alert color="blue" title={t("systemVoucher")}>
          {t("systemVoucherHint", { source: `${v.source.module} ${v.source.kind} ${v.source.id}` })}
          {v.rule && ` ${t("postedByRule", { rule: v.rule, version: v.ruleVersion ?? 1 })}`}
        </Alert>
      )}
      {v.reverses && (
        <Text>
          {t("reverses")}{" "}
          <Anchor component={Link} to={voucherPath(v.reverses.id)}>
            {v.reverses.display}
          </Anchor>
          {v.reversalReason && ` — ${v.reversalReason}`}
        </Text>
      )}
      {v.reversedBy && (
        <Text>
          {t("reversedBy")}{" "}
          <Anchor component={Link} to={voucherPath(v.reversedBy.id)}>
            {v.reversedBy.display}
          </Anchor>
        </Text>
      )}
      <Table striped data-assist="accounting.voucher.postings">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("text")}</Table.Th>
            <Table.Th>{t("taxCode")}</Table.Th>
            <Table.Th>{t("party")}</Table.Th>
            <Table.Th>{t("project")}</Table.Th>
            <Table.Th ta="right">{t("debit")}</Table.Th>
            <Table.Th ta="right">{t("credit")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {v.postings.map((p) => (
            <Table.Tr key={p.lineNo}>
              <Table.Td>
                {p.accountNumber} {p.accountName}
              </Table.Td>
              <Table.Td>{p.description}</Table.Td>
              <Table.Td>
                {p.taxCode}
                {p.taxRole === "tax" && (
                  <Badge ml="xs" variant="light">
                    {t("taxLineShort")}
                  </Badge>
                )}
              </Table.Td>
              <Table.Td>{p.customerName ?? (p.customerId ? t("customerNumber", { id: p.customerId }) : "")}</Table.Td>
              <Table.Td>{p.projectName ?? p.projectId ?? ""}</Table.Td>
              <Table.Td ta="right">{p.amount.startsWith("-") ? "" : amount(p.amount)}</Table.Td>
              <Table.Td ta="right">{p.amount.startsWith("-") ? amount(p.amount.slice(1)) : ""}</Table.Td>
            </Table.Tr>
          ))}
          <Table.Tr>
            <Table.Td colSpan={5} fw={600}>
              {t("total")}
            </Table.Td>
            <Table.Td ta="right" fw={600}>
              {amount(String(debit))}
            </Table.Td>
            <Table.Td ta="right" fw={600}>
              {amount(String(debit))}
            </Table.Td>
          </Table.Tr>
        </Table.Tbody>
      </Table>
      <Card withBorder padding="md">
        <Group justify="space-between" mb="xs">
          <Text fw={600}>{t("attachments")}</Text>
          {meta.canPost && (
            <FileButton onChange={(file) => file && upload.mutate(file)}>
              {(props) => (
                <Button
                  variant="light"
                  size="xs"
                  leftSection={<IconPaperclip size={14} />}
                  loading={upload.isPending}
                  {...props}
                >
                  {t("attachFile")}
                </Button>
              )}
            </FileButton>
          )}
        </Group>
        {v.attachments.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noAttachments")}
          </Text>
        ) : (
          <Stack gap={4}>
            {v.attachments.map((a) => (
              <Anchor key={a.id} href={attachmentUrl(v.id, a.id)}>
                {a.fileName}
              </Anchor>
            ))}
          </Stack>
        )}
        {upload.error && (
          <Text c="red" size="sm" mt="xs">
            {refusalMessage(t, upload.error)}
          </Text>
        )}
      </Card>
      {reversing && <ReverseDialog voucher={v} today={meta.today} onClose={() => setReversing(false)} />}
    </Stack>
  );
};

const Fact = ({ label, value }: { label: string; value: string }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text>{value}</Text>
  </Stack>
);

const ReverseDialog = ({
  voucher,
  today,
  onClose,
}: {
  voucher: AccountingVoucher;
  today: string;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [date, setDate] = useState(voucher.kind === "opening" ? voucher.voucherDate : today);
  const [reason, setReason] = useState("");
  const reverse = useMutation({
    mutationFn: () => reverseVoucher(voucher.id, { date, reason }),
    onSuccess: async (reversal) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("posted", { voucher: reversal.display }) });
      onClose();
      await navigate({ to: voucherPath(reversal.id) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("reverseVoucher", { voucher: voucher.display })}>
      <Stack gap="sm">
        <Text size="sm">{t("reverseHint")}</Text>
        <TextInput
          type="date"
          label={t("voucherDate")}
          value={date}
          onChange={(e) => setDate(e.currentTarget.value)}
          disabled={voucher.kind === "opening"}
        />
        <TextInput required label={t("reason")} value={reason} onChange={(e) => setReason(e.currentTarget.value)} />
        {reverse.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
            {refusalMessage(t, reverse.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color="red" onClick={() => reverse.mutate()} loading={reverse.isPending} disabled={!reason.trim()}>
            {t("reverse")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
