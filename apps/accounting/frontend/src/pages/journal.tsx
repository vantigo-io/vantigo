import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  FileInput,
  Group,
  SegmentedControl,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import {
  type AccountingMeta,
  type AccountingVoucher,
  accountsQueryOptions,
  postVoucher,
  previewVoucher,
  uploadAttachment,
  voucherQueryOptions,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, CustomerSelect, ProjectSelect, TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { emptyLine, type FormLine, fromOre, linesFromVoucher, toJournalInput, typedDifference } from "../lib/journal";
import { voucherPath } from "../lib/routes";
import type { JournalSearch } from "../lib/search";

/**
 * A manual journal (A6): lines with debit or credit, a tax code entered net
 * or gross, a party and a project, previewed live with the tax lines the
 * server computes, and posted at once — there is no draft (A4). Files are
 * attached once it is posted.
 */
export const JournalEntryPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("newJournalEntry")} description={t("journalDescription")} />
          {meta.canPost ? (
            <JournalForm meta={meta} />
          ) : (
            <Alert color="yellow" title={t("notAllowed")}>
              {t("notAllowed_post")}
            </Alert>
          )}
        </Stack>
      )}
    </BooksGate>
  );
};

/** The form, started from a copied voucher when the search names one (R13). */
const JournalForm = ({ meta }: { meta: AccountingMeta }) => {
  const search = useSearch({ strict: false }) as JournalSearch;
  const copied = useQuery({ ...voucherQueryOptions(search.copy ?? 0), enabled: search.copy !== undefined });
  if (search.copy !== undefined && copied.isPending) return <ContentSkeleton rows={4} />;
  return <JournalEditor key={copied.data?.id ?? "new"} meta={meta} copied={copied.data} />;
};

const JournalEditor = ({ meta, copied }: { meta: AccountingMeta; copied: AccountingVoucher | undefined }) => {
  const { t, amount } = useAccountingFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const accounts = useQuery(accountsQueryOptions());
  const [date, setDate] = useState(meta.today);
  const [description, setDescription] = useState(copied?.description ?? "");
  const [entry, setEntry] = useState<"net" | "gross">("net");
  const [lines, setLines] = useState<FormLine[]>(() =>
    copied ? [...linesFromVoucher(copied), emptyLine()] : [emptyLine(), emptyLine()],
  );
  const [files, setFiles] = useState<File[]>([]);

  const controlRole = (accountId: string | null) =>
    accounts.data?.find((a) => String(a.id) === accountId)?.systemRole ?? null;
  const update = (key: string, change: Partial<FormLine>) =>
    setLines((current) => current.map((line) => (line.key === key ? { ...line, ...change } : line)));

  const input = useMemo(
    () => toJournalInput(date, description || "-", entry, lines),
    [date, description, entry, lines],
  );
  const complete = input.lines.length > 0 && input.lines.every((line) => line.accountId > 0 && line.amount !== "");
  const [debounced] = useDebouncedValue(input, 400);
  const preview = useQuery({
    queryKey: [ACCOUNTING_QUERY_KEY, "preview", debounced],
    queryFn: ({ signal }) => previewVoucher(debounced, signal),
    enabled: complete,
    retry: false,
  });

  const post = useMutation({
    mutationFn: async () => {
      const voucher = await postVoucher({ ...input, description: description.trim() });
      for (const file of files) await uploadAttachment(voucher.id, file);
      return voucher;
    },
    onSuccess: async (voucher) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("posted", { voucher: voucher.display }) });
      await navigate({ to: voucherPath(voucher.id) });
    },
  });
  const fields = refusedFields(post.error);
  const difference = typedDifference(lines);
  const accountName = (id: number) => {
    const a = accounts.data?.find((account) => account.id === id);
    return a ? `${a.number} ${a.name}` : String(id);
  };

  return (
    <Stack gap="md">
      <Card withBorder padding="md">
        <Group align="flex-end" wrap="wrap">
          <TextInput
            type="date"
            required
            label={t("voucherDate")}
            value={date}
            onChange={(event) => setDate(event.currentTarget.value)}
            error={fields.voucherDate}
          />
          <TextInput
            required
            label={t("description")}
            value={description}
            onChange={(event) => setDescription(event.currentTarget.value)}
            error={fields.description}
            style={{ flex: 1, minWidth: 240 }}
          />
          <SegmentedControl
            data-assist="accounting.journal.entry-mode"
            value={entry}
            onChange={(value) => setEntry(value === "gross" ? "gross" : "net")}
            data={[
              { value: "net", label: t("entryNet") },
              { value: "gross", label: t("entryGross") },
            ]}
          />
        </Group>
        {copied && (
          <Text size="sm" c="dimmed" mt="xs">
            {t("copiedFrom", { voucher: copied.display })}
          </Text>
        )}
      </Card>
      <Table data-assist="accounting.journal.lines">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("debit")}</Table.Th>
            <Table.Th>{t("credit")}</Table.Th>
            <Table.Th>{t("taxCode")}</Table.Th>
            <Table.Th>{t("party")}</Table.Th>
            {meta.projectsEnabled && <Table.Th>{t("project")}</Table.Th>}
            <Table.Th>{t("text")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {lines.map((line, index) => {
            const role = controlRole(line.accountId);
            const field = (name: string) => fields[`lines[${index}].${name}`];
            return (
              <Table.Tr key={line.key}>
                <Table.Td miw={220}>
                  <AccountSelect
                    aria-label={t("account")}
                    value={line.accountId}
                    onChange={(value) => {
                      const account = accounts.data?.find((a) => String(a.id) === value);
                      update(line.key, {
                        accountId: value,
                        taxCodeId:
                          line.taxCodeId ?? (account?.defaultTaxCodeId ? String(account.defaultTaxCodeId) : null),
                      });
                    }}
                    error={field("accountId")}
                  />
                </Table.Td>
                <Table.Td>
                  <TextInput
                    aria-label={t("debit")}
                    value={line.debit}
                    onChange={(event) => update(line.key, { debit: event.currentTarget.value })}
                    error={field("amount")}
                    w={120}
                  />
                </Table.Td>
                <Table.Td>
                  <TextInput
                    aria-label={t("credit")}
                    value={line.credit}
                    onChange={(event) => update(line.key, { credit: event.currentTarget.value })}
                    w={120}
                  />
                </Table.Td>
                <Table.Td miw={180}>
                  <TaxCodeSelect
                    aria-label={t("taxCode")}
                    value={line.taxCodeId}
                    onChange={(value) => update(line.key, { taxCodeId: value })}
                    error={field("taxCodeId")}
                  />
                </Table.Td>
                <Table.Td miw={180}>
                  {role === "receivables" || line.customerId ? (
                    <CustomerSelect
                      aria-label={t("customer")}
                      value={line.customerId || null}
                      onChange={(value) => update(line.key, { customerId: value ?? "" })}
                      error={field("customerId")}
                    />
                  ) : role === "payables" ? (
                    <Text size="sm" c="dimmed">
                      {t("suppliersComeLater")}
                    </Text>
                  ) : null}
                </Table.Td>
                {meta.projectsEnabled && (
                  <Table.Td miw={160}>
                    <ProjectSelect
                      aria-label={t("project")}
                      value={line.projectId || null}
                      onChange={(value) => update(line.key, { projectId: value ?? "" })}
                    />
                  </Table.Td>
                )}
                <Table.Td>
                  <TextInput
                    aria-label={t("text")}
                    value={line.text}
                    onChange={(event) => update(line.key, { text: event.currentTarget.value })}
                  />
                </Table.Td>
                <Table.Td>
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label={t("removeLine")}
                    onClick={() => setLines((current) => current.filter((l) => l.key !== line.key))}
                    disabled={lines.length <= 1}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Table.Td>
              </Table.Tr>
            );
          })}
        </Table.Tbody>
      </Table>
      <Group justify="space-between">
        <Button
          variant="subtle"
          leftSection={<IconPlus size={16} />}
          onClick={() => setLines((c) => [...c, emptyLine()])}
        >
          {t("addLine")}
        </Button>
        <Text size="sm" c={difference === 0n ? "dimmed" : undefined}>
          {t("typedDifference", { amount: amount(fromOre(difference)) })}
        </Text>
      </Group>
      <Card withBorder padding="md" data-assist="accounting.journal.preview">
        <Text fw={600} mb="xs">
          {t("preview")}
        </Text>
        {!complete ? (
          <Text size="sm" c="dimmed">
            {t("previewWaiting")}
          </Text>
        ) : preview.error ? (
          <Text size="sm" c="red">
            {refusalMessage(t, preview.error)}
          </Text>
        ) : preview.data ? (
          <Stack gap="xs">
            <Table>
              <Table.Tbody>
                {preview.data.lines.map((l) => (
                  <Table.Tr key={l.lineNo}>
                    <Table.Td>{accountName(l.accountId)}</Table.Td>
                    <Table.Td>
                      {l.taxRole === "tax" && (
                        <Badge variant="light">{t("taxLine", { rate: l.ratePercent ?? "" })}</Badge>
                      )}
                    </Table.Td>
                    <Table.Td ta="right">{l.amount.startsWith("-") ? "" : amount(l.amount)}</Table.Td>
                    <Table.Td ta="right">{l.amount.startsWith("-") ? amount(l.amount.slice(1)) : ""}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            <Text size="sm" c={preview.data.balanced ? "green" : "red"}>
              {preview.data.balanced
                ? t("balances", { amount: amount(preview.data.debit) })
                : t("doesNotBalance", { amount: amount(preview.data.difference) })}
            </Text>
          </Stack>
        ) : (
          <Text size="sm" c="dimmed">
            {t("loading")}
          </Text>
        )}
      </Card>
      <FileInput
        multiple
        clearable
        label={t("attachments")}
        description={t("attachmentsHint")}
        value={files}
        onChange={setFiles}
      />
      {post.error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
          {refusalMessage(t, post.error)}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button
          data-assist="accounting.journal.post"
          onClick={() => post.mutate()}
          loading={post.isPending}
          disabled={!complete || !description.trim()}
        >
          {t("post")}
        </Button>
      </Group>
    </Stack>
  );
};
