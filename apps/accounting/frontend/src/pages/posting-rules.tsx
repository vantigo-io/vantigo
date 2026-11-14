import { Alert, Badge, Button, Group, Modal, NumberInput, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArchive, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingAccount,
  type AccountingPostingRule,
  type AccountingRuleMatch,
  createPostingRule,
  deletePostingRule,
  lineKinds,
  postingRulesQueryOptions,
  taxCodesQueryOptions,
  updatePostingRule,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";

/**
 * The posting rules (A14): which account a sales line's net goes to, matched
 * on its line kind, its product's tax category or its tax code, in that
 * order, then the default rule of its tax code. Each shows its version: a
 * voucher names the rule and version that made it, and a change affects
 * future vouchers only. Changing them needs accounting:close.
 */
export const PostingRulesPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navPostingRules")} description={t("postingRulesDescription")} />
          <RulesTable canClose={meta.canClose} />
        </Stack>
      )}
    </BooksGate>
  );
};

/** What a rule matches, in words. */
const useMatchWords = () => {
  const { t } = useAccountingFormat();
  return (r: AccountingPostingRule) => {
    switch (r.match) {
      case "line_kind":
        return t(`lineKind_${r.lineKind ?? ""}`);
      case "tax_category":
        return t("taxCategoryNumber", { id: r.taxCategoryId ?? "" });
      default:
        return t("taxCodeNumber", { code: r.taxCode ?? "" });
    }
  };
};

const RulesTable = ({ canClose }: { canClose: boolean }) => {
  const { t } = useAccountingFormat();
  const rules = useQuery(postingRulesQueryOptions());
  const matchWords = useMatchWords();
  const [editing, setEditing] = useState<AccountingPostingRule | null>(null);
  const [adding, setAdding] = useState(false);
  const queryClient = useQueryClient();
  // A rule is archived, never deleted (R15): what it placed keeps naming it.
  const remove = useMutation({
    mutationFn: (id: number) => deletePostingRule(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "posting-rules"] });
      notifications.show({ color: "green", message: t("archived") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  if (rules.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {rules.error.message}
      </Alert>
    );
  if (rules.isPending) return <ContentSkeleton rows={6} />;
  return (
    <Stack gap="md">
      {canClose && (
        <Group>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setAdding(true)}>
            {t("addPostingRule")}
          </Button>
        </Group>
      )}
      <Table striped highlightOnHover data-testid="posting-rules">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("ruleMatch")}</Table.Th>
            <Table.Th>{t("ruleMatches")}</Table.Th>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("ruleVersion")}</Table.Th>
            {canClose && <Table.Th />}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rules.data.map((r) => (
            <Table.Tr key={r.id}>
              <Table.Td>
                {canClose ? (
                  <Button variant="subtle" size="compact-sm" onClick={() => setEditing(r)}>
                    {r.name}
                  </Button>
                ) : (
                  r.name
                )}
              </Table.Td>
              <Table.Td>
                {t(`ruleMatch_${r.match}`)}
                {r.match === "default" && (
                  <Badge ml="xs" variant="light" color="gray">
                    {t("ruleDefault")}
                  </Badge>
                )}
              </Table.Td>
              <Table.Td>{matchWords(r)}</Table.Td>
              <Table.Td>
                {r.accountNumber} {r.accountName}
              </Table.Td>
              <Table.Td>{r.version}</Table.Td>
              {canClose && (
                <Table.Td ta="right">
                  {r.match !== "default" && (
                    <Button
                      size="compact-sm"
                      variant="subtle"
                      color="red"
                      aria-label={t("archiveRule", { name: r.name })}
                      leftSection={<IconArchive size={14} />}
                      loading={remove.isPending && remove.variables === r.id}
                      onClick={() => remove.mutate(r.id)}
                    >
                      {t("archive")}
                    </Button>
                  )}
                </Table.Td>
              )}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {adding && <AddRuleModal onClose={() => setAdding(false)} />}
      {editing && <EditRuleModal rule={editing} onClose={() => setEditing(null)} />}
    </Stack>
  );
};

const matches: AccountingRuleMatch[] = ["line_kind", "tax_category", "tax_code"];

/** A sales rule credits an income account (R15): the picker offers no other. */
const isIncome = (a: AccountingAccount) => a.kind === "income";

/** A new rule: its name, what it matches and the account its lines go to. */
const AddRuleModal = ({ onClose }: { onClose: () => void }) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const codes = useQuery(taxCodesQueryOptions());
  const [name, setName] = useState("");
  const [match, setMatch] = useState<AccountingRuleMatch>("line_kind");
  const [lineKind, setLineKind] = useState<string | null>("hours");
  const [taxCategory, setTaxCategory] = useState<number | string>("");
  const [taxCode, setTaxCode] = useState<string | null>(null);
  const [account, setAccount] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: () =>
      createPostingRule({
        name: name.trim(),
        scope: "sales",
        match,
        ...(match === "line_kind" && lineKind ? { lineKind } : {}),
        ...(match === "tax_category" && taxCategory !== "" ? { taxCategoryId: Number(taxCategory) } : {}),
        ...(match === "tax_code" && taxCode ? { taxCode } : {}),
        accountId: Number(account),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "posting-rules"] });
      notifications.show({ color: "green", message: t("saved") });
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("addPostingRule")}>
      <Stack>
        <TextInput
          label={t("name")}
          required
          maxLength={100}
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        <Select
          label={t("ruleMatch")}
          allowDeselect={false}
          data={matches.map((m) => ({ value: m, label: t(`ruleMatch_${m}`) }))}
          value={match}
          onChange={(v) => v && setMatch(v as AccountingRuleMatch)}
        />
        {match === "line_kind" && (
          <Select
            label={t("ruleMatches")}
            allowDeselect={false}
            data={lineKinds.map((k) => ({ value: k, label: t(`lineKind_${k}`) }))}
            value={lineKind}
            onChange={setLineKind}
          />
        )}
        {match === "tax_category" && (
          <NumberInput
            label={t("taxCategoryId")}
            description={t("taxCategoryIdHint")}
            min={1}
            allowDecimal={false}
            value={taxCategory}
            onChange={setTaxCategory}
          />
        )}
        {match === "tax_code" && (
          <Select
            label={t("taxCode")}
            searchable
            data={(codes.data ?? [])
              .filter((c) => c.active)
              .map((c) => ({ value: c.code, label: `${c.code} ${c.name}` }))}
            value={taxCode}
            onChange={setTaxCode}
          />
        )}
        <AccountSelect
          label={t("account")}
          description={t("ruleIncomeHint")}
          required
          accountFilter={isIncome}
          value={account}
          onChange={setAccount}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} disabled={!name.trim() || !account} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** A rule's name and account; a new account is a new version. */
const EditRuleModal = ({ rule, onClose }: { rule: AccountingPostingRule; onClose: () => void }) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [name, setName] = useState(rule.name);
  const [account, setAccount] = useState<string | null>(String(rule.accountId));
  const save = useMutation({
    mutationFn: () =>
      updatePostingRule(rule.id, { name: name.trim(), accountId: Number(account), revision: rule.revision }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "posting-rules"] });
      notifications.show({ color: "green", message: t("saved") });
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  return (
    <Modal opened onClose={onClose} title={rule.name}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("ruleChangeHint", { version: rule.version + 1 })}
        </Text>
        <TextInput
          label={t("name")}
          required
          maxLength={100}
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        <AccountSelect
          label={t("account")}
          description={t("ruleIncomeHint")}
          required
          accountFilter={isIncome}
          value={account}
          onChange={setAccount}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} disabled={!name.trim() || !account} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
