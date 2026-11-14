import { Alert, Badge, Button, Group, Modal, Stack, Switch, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingTaxCode,
  accountsQueryOptions,
  addTaxRate,
  taxCodesQueryOptions,
  updateTaxCode,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";

/** The tax codes (A5): the standard list, each with its VAT accounts and its rates. */
export const TaxCodesPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navTaxCodes")} description={t("taxCodesDescription")} />
          <TaxCodesTable canClose={meta.canClose} />
        </Stack>
      )}
    </BooksGate>
  );
};

const TaxCodesTable = ({ canClose }: { canClose: boolean }) => {
  const { t } = useAccountingFormat();
  const codes = useQuery(taxCodesQueryOptions());
  const accounts = useQuery(accountsQueryOptions());
  const [editing, setEditing] = useState<AccountingTaxCode | null>(null);
  if (codes.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {codes.error.message}
      </Alert>
    );
  if (codes.isPending) return <ContentSkeleton rows={6} />;
  const accountOf = (id: number | null) => accounts.data?.find((a) => a.id === id)?.number ?? "";
  return (
    <>
      <Table striped highlightOnHover data-assist="accounting.tax-codes.table">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("taxCode")}</Table.Th>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("direction")}</Table.Th>
            <Table.Th>{t("rate")}</Table.Th>
            <Table.Th>{t("vatAccount")}</Table.Th>
            <Table.Th>{t("reported")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {codes.data.map((c) => (
            <Table.Tr key={c.id} onClick={() => setEditing(c)} style={{ cursor: "pointer" }}>
              <Table.Td>{c.code}</Table.Td>
              <Table.Td>
                {c.name}
                {c.reverseCharge && (
                  <Badge ml="xs" variant="light">
                    {t("reverseCharge")}
                  </Badge>
                )}
                {!c.active && (
                  <Badge ml="xs" color="gray" variant="light">
                    {t("inactive")}
                  </Badge>
                )}
              </Table.Td>
              <Table.Td>{t(`direction_${c.direction}`)}</Table.Td>
              <Table.Td>{c.currentRate === null ? "" : `${c.currentRate} %`}</Table.Td>
              <Table.Td>
                {accountOf(c.vatAccountId)}
                {c.counterVatAccountId !== null && ` / ${accountOf(c.counterVatAccountId)}`}
              </Table.Td>
              <Table.Td>{c.reported ? t("yes") : t("no")}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {editing && <TaxCodeEditor code={editing} canClose={canClose} onClose={() => setEditing(null)} />}
    </>
  );
};

const TaxCodeEditor = ({
  code,
  canClose,
  onClose,
}: {
  code: AccountingTaxCode;
  canClose: boolean;
  onClose: () => void;
}) => {
  const { t, day } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [name, setName] = useState(code.name);
  const [vat, setVat] = useState<string | null>(code.vatAccountId === null ? null : String(code.vatAccountId));
  const [counter, setCounter] = useState<string | null>(
    code.counterVatAccountId === null ? null : String(code.counterVatAccountId),
  );
  const [active, setActive] = useState(code.active);
  const [rate, setRate] = useState("");
  const [from, setFrom] = useState("");
  const done = async () => {
    await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
    notifications.show({ color: "green", message: t("saved") });
    onClose();
  };
  const save = useMutation({
    mutationFn: () =>
      updateTaxCode(code.id, {
        name,
        vatAccountId: vat ? Number(vat) : null,
        counterVatAccountId: code.reverseCharge && counter ? Number(counter) : null,
        active,
        revision: code.revision,
      }),
    onSuccess: done,
  });
  const addRate = useMutation({
    mutationFn: () => addTaxRate(code.id, { ratePercent: rate.replace(",", "."), validFrom: from }),
    onSuccess: done,
  });
  const error = save.error ?? addRate.error;
  return (
    <Modal opened onClose={onClose} title={`${code.code} ${code.name}`} size="lg">
      <Stack gap="sm">
        <TextInput
          label={t("name")}
          value={name}
          onChange={(event) => setName(event.currentTarget.value)}
          disabled={!canClose}
        />
        <AccountSelect label={t("vatAccount")} value={vat} onChange={setVat} disabled={!canClose} />
        {code.reverseCharge && (
          <AccountSelect label={t("counterVatAccount")} value={counter} onChange={setCounter} disabled={!canClose} />
        )}
        <Switch
          label={t("active")}
          checked={active}
          onChange={(event) => setActive(event.currentTarget.checked)}
          disabled={!canClose}
        />
        {canClose && (
          <Group justify="flex-end">
            <Button onClick={() => save.mutate()} loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        )}
        <Text fw={600}>{t("rates")}</Text>
        <Table>
          <Table.Tbody>
            {code.rates.map((r) => (
              <Table.Tr key={r.validFrom}>
                <Table.Td>{r.ratePercent} %</Table.Td>
                <Table.Td>
                  {day(r.validFrom)} – {r.validTo ? day(r.validTo) : ""}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        {canClose && (
          <Group align="flex-end">
            <TextInput
              label={t("newRate")}
              value={rate}
              onChange={(event) => setRate(event.currentTarget.value)}
              w={120}
            />
            <TextInput
              type="date"
              label={t("validFrom")}
              value={from}
              onChange={(event) => setFrom(event.currentTarget.value)}
            />
            <Button
              variant="light"
              onClick={() => addRate.mutate()}
              loading={addRate.isPending}
              disabled={!rate || !from}
            >
              {t("addRate")}
            </Button>
          </Group>
        )}
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, error)}
          </Alert>
        )}
      </Stack>
    </Modal>
  );
};
