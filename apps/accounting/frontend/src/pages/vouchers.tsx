import { Alert, Badge, Button, Group, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { IconAlertCircle, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type AccountingVoucherKind, voucherKinds, vouchersQueryOptions } from "../api/accounting";
import { BooksGate } from "../components/books-gate";
import { useAccountingFormat } from "../lib/format";
import { voucherPath } from "../lib/routes";

const PAGE = 50;

/** Every voucher, newest first, filtered by period, kind and text (A4). */
export const VouchersPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {() => (
        <Stack gap="lg">
          <PageHeader title={t("navVouchers")} description={t("vouchersDescription")} />
          <VoucherList />
        </Stack>
      )}
    </BooksGate>
  );
};

const VoucherList = () => {
  const { t, day, money } = useAccountingFormat();
  const navigate = useNavigate();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [kind, setKind] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [offset, setOffset] = useState(0);
  const vouchers = useQuery(
    vouchersQueryOptions({
      from: from || undefined,
      to: to || undefined,
      kind: (kind ?? undefined) as AccountingVoucherKind | undefined,
      q: q.trim() || undefined,
      offset,
      limit: PAGE,
    }),
  );
  return (
    <Stack gap="md">
      <Group align="flex-end" data-assist="accounting.vouchers.filters">
        <TextInput
          type="date"
          label={t("from")}
          value={from}
          onChange={(e) => {
            setFrom(e.currentTarget.value);
            setOffset(0);
          }}
        />
        <TextInput
          type="date"
          label={t("to")}
          value={to}
          onChange={(e) => {
            setTo(e.currentTarget.value);
            setOffset(0);
          }}
        />
        <Select
          clearable
          label={t("kind")}
          data={voucherKinds.map((k) => ({ value: k, label: t(`voucherKind_${k}`) }))}
          value={kind}
          onChange={(value) => {
            setKind(value);
            setOffset(0);
          }}
        />
        <TextInput
          leftSection={<IconSearch size={16} />}
          label={t("search")}
          placeholder={t("searchVouchers")}
          value={q}
          onChange={(e) => {
            setQ(e.currentTarget.value);
            setOffset(0);
          }}
        />
      </Group>
      {vouchers.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {vouchers.error.message}
        </Alert>
      ) : vouchers.isPending ? (
        <ContentSkeleton rows={6} />
      ) : vouchers.data.items.length === 0 ? (
        <EmptyState icon={IconSearch} title={t("noVouchers")} description={t("noVouchersHint")} />
      ) : (
        <>
          <Table striped highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("voucher")}</Table.Th>
                <Table.Th>{t("voucherDate")}</Table.Th>
                <Table.Th>{t("description")}</Table.Th>
                <Table.Th>{t("kind")}</Table.Th>
                <Table.Th ta="right">{t("amount")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {vouchers.data.items.map((v) => (
                <Table.Tr key={v.id} style={{ cursor: "pointer" }} onClick={() => navigate({ to: voucherPath(v.id) })}>
                  <Table.Td>
                    <Link to={voucherPath(v.id)}>{v.display}</Link>
                  </Table.Td>
                  <Table.Td>{day(v.voucherDate)}</Table.Td>
                  <Table.Td>
                    {v.description}
                    {v.reversedBy && (
                      <Badge ml="xs" color="gray" variant="light">
                        {t("reversed")}
                      </Badge>
                    )}
                  </Table.Td>
                  <Table.Td>{t(`voucherKind_${v.kind}`)}</Table.Td>
                  <Table.Td ta="right">{money(v.amount)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("showingOf", {
                from: offset + 1,
                to: offset + vouchers.data.items.length,
                total: vouchers.data.total,
              })}
            </Text>
            <Group gap="xs">
              <Button variant="default" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
                {t("previous")}
              </Button>
              <Button
                variant="default"
                disabled={offset + PAGE >= vouchers.data.total}
                onClick={() => setOffset(offset + PAGE)}
              >
                {t("next")}
              </Button>
            </Group>
          </Group>
        </>
      )}
    </Stack>
  );
};
