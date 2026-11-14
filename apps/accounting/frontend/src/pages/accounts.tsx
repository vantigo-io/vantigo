import { Alert, Badge, Button, Group, Modal, Select, Stack, Switch, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconSearch } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingAccount,
  type AccountingAccountKind,
  type AccountingSystemRole,
  accountKinds,
  accountsQueryOptions,
  assignRole,
  createAccount,
  deleteAccount,
  groupingCodesQueryOptions,
  putGroupings,
  systemRoles,
  taxCodesQueryOptions,
  updateAccount,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";

/** The chart of accounts (A3): the list, and an account's editor with its grouping and roles. */
export const AccountsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navAccounts")} description={t("accountsDescription")} />
          <AccountsTable
            canClose={meta.canClose}
            year={Number((meta.today ?? "").slice(0, 4)) || new Date().getFullYear()}
          />
        </Stack>
      )}
    </BooksGate>
  );
};

const AccountsTable = ({ canClose, year }: { canClose: boolean; year: number }) => {
  const { t } = useAccountingFormat();
  const accounts = useQuery(accountsQueryOptions());
  const taxCodes = useQuery(taxCodesQueryOptions());
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<AccountingAccount | "new" | null>(null);
  if (accounts.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {accounts.error.message}
      </Alert>
    );
  if (accounts.isPending) return <ContentSkeleton rows={6} />;
  const needle = search.trim().toLowerCase();
  const rows = accounts.data.filter(
    (a) => !needle || a.number.startsWith(needle) || a.name.toLowerCase().includes(needle),
  );
  const codeOf = (id: number | null) => taxCodes.data?.find((c) => c.id === id)?.code ?? "";
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <TextInput
          leftSection={<IconSearch size={16} />}
          placeholder={t("searchAccounts")}
          value={search}
          onChange={(event) => setSearch(event.currentTarget.value)}
        />
        {canClose && (
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => setEditing("new")}
            data-assist="accounting.accounts.new"
          >
            {t("newAccount")}
          </Button>
        )}
      </Group>
      <Table striped highlightOnHover data-assist="accounting.accounts.table">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("accountNumber")}</Table.Th>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("kind")}</Table.Th>
            <Table.Th>{t("defaultTaxCode")}</Table.Th>
            <Table.Th>{t("grouping")}</Table.Th>
            <Table.Th>{t("systemRole")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((a) => (
            <Table.Tr key={a.id} onClick={() => setEditing(a)} style={{ cursor: "pointer" }}>
              <Table.Td>{a.number}</Table.Td>
              <Table.Td>
                {a.name}
                {!a.active && (
                  <Badge ml="xs" color="gray" variant="light">
                    {t("inactive")}
                  </Badge>
                )}
              </Table.Td>
              <Table.Td>{t(`kind_${a.kind}`)}</Table.Td>
              <Table.Td>{codeOf(a.defaultTaxCodeId)}</Table.Td>
              <Table.Td>{a.groupings.at(-1)?.code ?? "NA"}</Table.Td>
              <Table.Td>{a.systemRole && <Badge variant="light">{t(`role_${a.systemRole}`)}</Badge>}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {editing && (
        <AccountEditor
          account={editing === "new" ? null : editing}
          canClose={canClose}
          year={year}
          onClose={() => setEditing(null)}
        />
      )}
    </Stack>
  );
};

const AccountEditor = ({
  account,
  canClose,
  year,
  onClose,
}: {
  account: AccountingAccount | null;
  canClose: boolean;
  year: number;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [number, setNumber] = useState(account?.number ?? "");
  const [name, setName] = useState(account?.name ?? "");
  const [kind, setKind] = useState<string | null>(account?.kind ?? "expense");
  const [taxCode, setTaxCode] = useState<string | null>(
    account?.defaultTaxCodeId ? String(account.defaultTaxCodeId) : null,
  );
  const [active, setActive] = useState(account?.active ?? true);
  const current = account?.groupings.at(-1);
  const [grouping, setGrouping] = useState<string | null>(current ? `${current.category}|${current.code}` : null);
  const [role, setRole] = useState<string | null>(null);
  const codes = useQuery(groupingCodesQueryOptions(year));
  const done = async (message: string) => {
    await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
    notifications.show({ color: "green", message });
    onClose();
  };
  const save = useMutation({
    mutationFn: async () => {
      const [category, code] = (grouping ?? "|").split("|");
      const chosen = grouping ? { fromYear: year, category, code } : null;
      if (!account)
        return createAccount({
          number,
          name,
          kind: (kind ?? "expense") as AccountingAccountKind,
          defaultTaxCodeId: taxCode ? Number(taxCode) : null,
          grouping: chosen,
        });
      const saved = await updateAccount(account.id, {
        number,
        name,
        kind: (kind ?? "expense") as AccountingAccountKind,
        defaultTaxCodeId: taxCode ? Number(taxCode) : null,
        active,
        revision: account.revision,
      });
      if (chosen && (chosen.category !== current?.category || chosen.code !== current?.code)) {
        const others = account.groupings.filter((g) => g.fromYear !== year);
        await putGroupings(account.id, [...others, chosen]);
      }
      if (role) await assignRole(role as AccountingSystemRole, account.id);
      return saved;
    },
    onSuccess: () => done(t("saved")),
  });
  const remove = useMutation({
    mutationFn: () => deleteAccount(account?.id ?? 0),
    onSuccess: () => done(t("deleted")),
  });
  const fields = refusedFields(save.error);
  const error = save.error ?? remove.error;
  const locked = account?.hasPostings ?? false;
  return (
    <Modal opened onClose={onClose} title={account ? `${account.number} ${account.name}` : t("newAccount")} size="lg">
      <Stack gap="sm">
        <Group grow>
          <TextInput
            label={t("accountNumber")}
            value={number}
            onChange={(event) => setNumber(event.currentTarget.value)}
            disabled={!canClose || locked}
            description={locked ? t("numberFixed") : undefined}
            error={fields.number}
          />
          <Select
            label={t("kind")}
            data={accountKinds.map((k) => ({ value: k, label: t(`kind_${k}`) }))}
            value={kind}
            onChange={setKind}
            disabled={!canClose || locked}
            allowDeselect={false}
          />
        </Group>
        <TextInput
          label={t("name")}
          value={name}
          onChange={(event) => setName(event.currentTarget.value)}
          disabled={!canClose}
          error={fields.name}
        />
        <TaxCodeSelect label={t("defaultTaxCode")} value={taxCode} onChange={setTaxCode} disabled={!canClose} />
        <Select
          searchable
          clearable
          label={t("groupingFor", { year })}
          description={t("groupingHint")}
          data={(codes.data?.items ?? []).map((c) => ({
            value: `${c.category}|${c.code}`,
            label: `${c.code} ${c.nameNb} (${c.category})`,
          }))}
          value={grouping}
          onChange={setGrouping}
          disabled={!canClose}
          limit={50}
          data-assist="accounting.accounts.grouping"
        />
        {account && (
          <>
            <Switch
              label={t("active")}
              checked={active}
              onChange={(event) => setActive(event.currentTarget.checked)}
              disabled={!canClose}
            />
            {account.systemRole ? (
              <Text size="sm">{t("holdsRole", { role: t(`role_${account.systemRole}`) })}</Text>
            ) : (
              canClose && (
                <Select
                  clearable
                  label={t("moveRoleHere")}
                  description={t("moveRoleHint")}
                  data={systemRoles.map((r) => ({ value: r, label: t(`role_${r}`) }))}
                  value={role}
                  onChange={setRole}
                />
              )
            )}
          </>
        )}
        {error && Object.keys(fields).length === 0 && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, error)}
          </Alert>
        )}
        {canClose && (
          <Group justify="space-between">
            {account && !account.hasPostings && !account.systemRole ? (
              <Button color="red" variant="subtle" onClick={() => remove.mutate()} loading={remove.isPending}>
                {t("delete")}
              </Button>
            ) : (
              <span />
            )}
            <Button onClick={() => save.mutate()} loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        )}
      </Stack>
    </Modal>
  );
};
