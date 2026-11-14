import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Checkbox,
  FileInput,
  Group,
  Modal,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconBuildingBank,
  IconDownload,
  IconFileImport,
  IconListCheck,
  IconPlus,
  IconSettings,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingBankAccount,
  type AccountingBankAccountInput,
  type AccountingBankCsvMapping,
  type AccountingBankCsvPreview,
  type AccountingBankImportResult,
  type AccountingBankRule,
  type AccountingBankRuleInput,
  type AccountingMeta,
  bankAccountQueryOptions,
  bankAccountsQueryOptions,
  bankFeedAccountsQueryOptions,
  bankRulesQueryOptions,
  createBankAccount,
  createBankRule,
  csvDateFormats,
  deleteBankAccount,
  deleteBankRule,
  importStatement,
  previewCsv,
  putCsvMapping,
  setBankAccountSource,
  statementFileUrl,
  statementsQueryOptions,
  updateBankAccount,
  updateBankRule,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import {
  BANK_IMPORT_ROUTE_PATH,
  BANK_ROUTE_PATH,
  BANK_RULES_ROUTE_PATH,
  bankAccountPath,
  bankAccountSettingsPath,
} from "../lib/routes";

/** Everything a bank write changes: the accounts, their lines, the overview and the meta. */
const useInvalidate = () => {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
};

/**
 * The bank (A26): the company's bank accounts, each the statement of one
 * ledger account, with where its statement comes from and the lines not yet
 * matched. Reading needs accounting:read, importing accounting:post, the
 * accounts themselves accounting:close. The seller's account in Invoices is
 * offered as the first.
 */
export const BankAccountsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader
            title={t("navBank")}
            description={t("bankDescription")}
            actions={
              <Group gap="sm">
                {meta.canPost && (
                  <Button
                    data-assist="accounting.bank.import"
                    leftSection={<IconFileImport size={16} />}
                    renderRoot={(props) => <Link to={BANK_IMPORT_ROUTE_PATH} {...props} />}
                  >
                    {t("bankImport")}
                  </Button>
                )}
                <Button
                  variant="light"
                  leftSection={<IconListCheck size={16} />}
                  renderRoot={(props) => <Link to={BANK_RULES_ROUTE_PATH} {...props} />}
                >
                  {t("bankRules")}
                </Button>
              </Group>
            }
          />
          <BankAccountList meta={meta} />
        </Stack>
      )}
    </BooksGate>
  );
};

/** The source of a bank account, with the feed's state. */
const SourceBadge = ({ account }: { account: AccountingBankAccount }) => {
  const { t } = useAccountingFormat();
  if (account.source !== "feed")
    return (
      <Badge variant="light" color="gray">
        {t("bankSourceFile")}
      </Badge>
    );
  const status = account.feed?.status ?? "ok";
  return (
    <Badge variant="light" color={status === "ok" ? "teal" : status === "rate_limited" ? "yellow" : "red"}>
      {t("bankSourceFeed")} · {t(`bankFeedStatus_${status}`)}
    </Badge>
  );
};

const BankAccountList = ({ meta }: { meta: AccountingMeta }) => {
  const { t, day } = useAccountingFormat();
  const list = useQuery(bankAccountsQueryOptions());
  const [adding, setAdding] = useState<AccountingBankAccountInput | null>(null);
  if (list.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {list.error.message}
      </Alert>
    );
  if (list.isPending) return <ContentSkeleton rows={4} />;
  const { items, suggested } = list.data;
  return (
    <Stack gap="md">
      {suggested && meta.canClose && (
        <Alert
          color="blue"
          icon={<IconBuildingBank size={16} />}
          title={t("bankSuggestedTitle")}
          data-testid="bank-suggested"
        >
          <Stack gap="xs">
            <Text size="sm">{t("bankSuggestedHint", { number: suggested.accountNumber, name: suggested.name })}</Text>
            <Group>
              <Button
                size="xs"
                onClick={() =>
                  setAdding({
                    name: suggested.name,
                    accountNumber: suggested.accountNumber,
                    iban: suggested.iban,
                    bic: suggested.bic,
                    ledgerAccountId: suggested.ledgerAccountId ?? 0,
                  })
                }
              >
                {t("bankAddSuggested")}
              </Button>
            </Group>
          </Stack>
        </Alert>
      )}
      {meta.canClose && (
        <Group justify="flex-end">
          <Button
            leftSection={<IconPlus size={16} />}
            variant="light"
            onClick={() => setAdding({ name: "", accountNumber: "", ledgerAccountId: 0 })}
          >
            {t("bankAddAccount")}
          </Button>
        </Group>
      )}
      {items.length === 0 ? (
        <EmptyState title={t("bankNoAccounts")} description={t("bankNoAccountsHint")} />
      ) : (
        <Table striped highlightOnHover data-testid="bank-accounts">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("name")}</Table.Th>
              <Table.Th>{t("bankAccountNumber")}</Table.Th>
              <Table.Th>{t("bankLedgerAccount")}</Table.Th>
              <Table.Th>{t("bankSource")}</Table.Th>
              <Table.Th ta="right">{t("bankOpenLinesHeader")}</Table.Th>
              <Table.Th>{t("bankLastStatement")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.map((a) => (
              <Table.Tr key={a.id}>
                <Table.Td>
                  <Anchor component={Link} to={bankAccountPath(a.id)}>
                    {a.name}
                  </Anchor>
                  {!a.active && (
                    <Badge ml="xs" variant="light" color="gray">
                      {t("inactive")}
                    </Badge>
                  )}
                </Table.Td>
                <Table.Td>{a.accountNumber}</Table.Td>
                <Table.Td>
                  {a.ledgerAccountNumber} {a.ledgerAccountName}
                </Table.Td>
                <Table.Td>
                  <SourceBadge account={a} />
                </Table.Td>
                <Table.Td ta="right">{a.openLines}</Table.Td>
                <Table.Td>{day(a.lastStatementTo)}</Table.Td>
                <Table.Td>
                  {meta.canClose && (
                    <Anchor
                      component={Link}
                      to={bankAccountSettingsPath(a.id)}
                      aria-label={t("bankSettingsOf", { name: a.name })}
                    >
                      <IconSettings size={16} />
                    </Anchor>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {adding && <BankAccountForm initial={adding} onClose={() => setAdding(null)} />}
    </Stack>
  );
};

/** The record of a bank account: new, or a change of one at its revision. */
const BankAccountForm = ({
  initial,
  id,
  onClose,
}: {
  initial: AccountingBankAccountInput;
  id?: number;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const invalidate = useInvalidate();
  const [form, setForm] = useState(initial);
  const save = useMutation({
    mutationFn: () => (id === undefined ? createBankAccount(form) : updateBankAccount(id, form)),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("saved") });
      onClose();
    },
  });
  const fields = refusedFields(save.error);
  const set = (change: Partial<AccountingBankAccountInput>) => setForm((f) => ({ ...f, ...change }));
  return (
    <Modal opened onClose={onClose} title={id === undefined ? t("bankAddAccount") : t("bankEditAccount")}>
      <Stack gap="sm">
        <TextInput
          label={t("name")}
          value={form.name}
          onChange={(e) => set({ name: e.currentTarget.value })}
          error={fields.name}
          required
        />
        <TextInput
          label={t("bankAccountNumber")}
          description={t("bankAccountNumberHint")}
          value={form.accountNumber}
          onChange={(e) => set({ accountNumber: e.currentTarget.value })}
          error={fields.accountNumber}
          required
        />
        <SimpleGrid cols={2}>
          <TextInput
            label={t("iban")}
            value={form.iban ?? ""}
            onChange={(e) => set({ iban: e.currentTarget.value || null })}
            error={fields.iban}
          />
          <TextInput
            label={t("bic")}
            value={form.bic ?? ""}
            onChange={(e) => set({ bic: e.currentTarget.value || null })}
            error={fields.bic}
          />
        </SimpleGrid>
        <AccountSelect
          label={t("bankLedgerAccount")}
          description={t("bankLedgerAccountHint")}
          value={form.ledgerAccountId ? String(form.ledgerAccountId) : null}
          onChange={(v) => set({ ledgerAccountId: v ? Number(v) : 0 })}
          accountFilter={(a) => a.kind === "asset"}
          error={fields.ledgerAccountId}
          required
        />
        <Checkbox
          label={t("active")}
          checked={form.active !== false}
          onChange={(e) => set({ active: e.currentTarget.checked })}
        />
        {save.error && Object.keys(fields).length === 0 && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, save.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => save.mutate()} loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * A bank account's settings (A26, A27, A29): its record, where its statement
 * comes from — files, or the bank feed Invoices holds from a start day, with
 * the feed's state — the CSV mapping with a preview, and its statements.
 */
export const BankAccountSettingsPage = () => {
  const { bankAccountId } = useParams({ strict: false }) as { bankAccountId: string };
  return <BooksGate>{(meta) => <BankAccountSettings id={Number(bankAccountId)} meta={meta} />}</BooksGate>;
};

const BankAccountSettings = ({ id, meta }: { id: number; meta: AccountingMeta }) => {
  const { t } = useAccountingFormat();
  const account = useQuery(bankAccountQueryOptions(id));
  const [editing, setEditing] = useState(false);
  const invalidate = useInvalidate();
  const navigate = useNavigate();
  const remove = useMutation({
    mutationFn: () => deleteBankAccount(id),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("deleted") });
      await navigate({ to: BANK_ROUTE_PATH });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  if (account.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {account.error.message}
      </Alert>
    );
  if (account.isPending) return <ContentSkeleton rows={6} />;
  const a = account.data;
  return (
    <Stack gap="lg">
      <PageHeader
        title={a.name}
        description={`${a.accountNumber} · ${a.ledgerAccountNumber} ${a.ledgerAccountName}`}
        actions={
          meta.canClose && (
            <Group gap="sm">
              <Button variant="light" onClick={() => setEditing(true)}>
                {t("edit")}
              </Button>
              <Button variant="subtle" color="red" onClick={() => remove.mutate()} loading={remove.isPending}>
                {t("delete")}
              </Button>
            </Group>
          )
        }
      />
      <SourceCard account={a} meta={meta} />
      <MappingCard account={a} />
      <StatementsCard id={a.id} />
      {editing && (
        <BankAccountForm
          id={a.id}
          initial={{
            name: a.name,
            accountNumber: a.accountNumber,
            iban: a.iban,
            bic: a.bic,
            ledgerAccountId: a.ledgerAccountId,
            active: a.active,
            revision: a.revision,
          }}
          onClose={() => setEditing(false)}
        />
      )}
    </Stack>
  );
};

/** Where the statement comes from: files, or the bank feed from a start day (A29). */
const SourceCard = ({ account, meta }: { account: AccountingBankAccount; meta: AccountingMeta }) => {
  const { t, day, stamp } = useAccountingFormat();
  const invalidate = useInvalidate();
  const [source, setSource] = useState<"file" | "feed">(account.source === "feed" ? "feed" : "file");
  const [startDay, setStartDay] = useState(account.feedStartDay ?? meta.today);
  const feedAccounts = useQuery({
    ...bankFeedAccountsQueryOptions(),
    enabled: meta.bankFeedEnabled && source === "feed",
  });
  const offered = feedAccounts.data?.some((f) => f.number === account.accountNumber) ?? false;
  const save = useMutation({
    mutationFn: () => setBankAccountSource(account.id, { source, startDay: source === "feed" ? startDay : null }),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("saved") });
    },
  });
  return (
    <Card withBorder padding="md" data-testid="bank-source">
      <Title order={4} mb="xs">
        {t("bankSource")}
      </Title>
      <Stack gap="sm">
        <SegmentedControl
          value={source}
          onChange={(v) => setSource(v as "file" | "feed")}
          data={[
            { value: "file", label: t("bankSourceFile") },
            { value: "feed", label: t("bankSourceFeed"), disabled: !meta.bankFeedEnabled },
          ]}
        />
        {!meta.bankFeedEnabled && (
          <Text size="sm" c="dimmed">
            {t("bankFeedOffHint")}
          </Text>
        )}
        {source === "feed" && meta.bankFeedEnabled && (
          <>
            <Text size="sm">{t("bankFeedHint")}</Text>
            {feedAccounts.error && (
              <Alert color="red" icon={<IconAlertCircle size={16} />}>
                {refusalMessage(t, feedAccounts.error)}
              </Alert>
            )}
            {feedAccounts.data && !offered && (
              <Alert color="orange" icon={<IconAlertCircle size={16} />}>
                {t("bankFeedNotOffered", { number: account.accountNumber })}
              </Alert>
            )}
            <TextInput
              type="date"
              label={t("bankFeedStartDay")}
              description={t("bankFeedStartDayHint")}
              value={startDay}
              onChange={(e) => setStartDay(e.currentTarget.value)}
            />
          </>
        )}
        {account.source === "feed" && account.feed && (
          <Text size="sm" data-testid="bank-feed-state">
            {t(`bankFeedStatus_${account.feed.status ?? "ok"}`)}
            {account.feed.lastSyncedAt && ` · ${t("bankFeedLastSync", { at: stamp(account.feed.lastSyncedAt) })}`}
            {account.feed.nextSyncAt && ` · ${t("bankFeedNextSync", { at: stamp(account.feed.nextSyncAt) })}`}
            {account.feedStartDay && ` · ${t("bankFeedSince", { day: day(account.feedStartDay) })}`}
            {account.feed.detail && ` · ${account.feed.detail}`}
          </Text>
        )}
        {save.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, save.error)}
          </Alert>
        )}
        {meta.canClose && (
          <Group>
            <Button
              onClick={() => save.mutate()}
              loading={save.isPending}
              disabled={source === account.source && source === "file"}
            >
              {t("bankSourceSave")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/** A mapping that reads the commonest Norwegian bank export. */
const defaultMapping: AccountingBankCsvMapping = {
  delimiter: ";",
  encoding: "iso-8859-1",
  headerRows: 1,
  dateFormat: "DD.MM.YYYY",
  decimalSeparator: ",",
  thousandsSeparator: "",
  invertSign: false,
  columns: { date: 0, text: 1, amount: 2 },
};

/** The columns a mapping names, in the order the editor shows them. */
const columnKeys = [
  "date",
  "valueDate",
  "amount",
  "debit",
  "credit",
  "text",
  "reference",
  "kid",
  "counterpartyName",
  "counterpartyAccount",
  "bankId",
] as const;

/** How a CSV statement of the account is read (A27), with a preview of a file before it is saved. */
const MappingCard = ({ account }: { account: AccountingBankAccount }) => {
  const { t, money, day } = useAccountingFormat();
  const invalidate = useInvalidate();
  const [mapping, setMapping] = useState<AccountingBankCsvMapping>(account.csvMapping ?? defaultMapping);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<AccountingBankCsvPreview | null>(null);
  const read = useMutation({
    mutationFn: () => {
      if (!file) throw new Error(t("bankChooseFile"));
      return previewCsv(account.id, file, mapping);
    },
    onSuccess: setPreview,
  });
  const save = useMutation({
    mutationFn: () => putCsvMapping(account.id, mapping),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("saved") });
    },
  });
  const set = (change: Partial<AccountingBankCsvMapping>) => setMapping((m) => ({ ...m, ...change }));
  const setColumn = (key: (typeof columnKeys)[number], value: string | number) =>
    setMapping((m) => {
      const columns = { ...m.columns } as Record<string, number | null | undefined>;
      if (value === "" || value === undefined) delete columns[key];
      else columns[key] = Number(value) - 1;
      return { ...m, columns: columns as AccountingBankCsvMapping["columns"] };
    });
  const columnOf = (key: (typeof columnKeys)[number]) => {
    const v = (mapping.columns as Record<string, number | null | undefined>)[key];
    return v === null || v === undefined ? "" : v + 1;
  };
  return (
    <Card withBorder padding="md" data-testid="bank-mapping">
      <Title order={4} mb="xs">
        {t("bankCsvMapping")}
      </Title>
      <Text size="sm" c="dimmed" mb="sm">
        {t("bankCsvMappingHint")}
      </Text>
      <Stack gap="sm">
        <SimpleGrid cols={{ base: 2, sm: 4 }}>
          <Select
            label={t("bankCsvDelimiter")}
            data={[
              { value: ";", label: ";" },
              { value: ",", label: "," },
              { value: "\t", label: t("bankCsvTab") },
              { value: "|", label: "|" },
            ]}
            value={mapping.delimiter}
            onChange={(v) => v && set({ delimiter: v })}
          />
          <Select
            label={t("bankCsvEncoding")}
            data={["utf-8", "iso-8859-1"]}
            value={mapping.encoding}
            onChange={(v) => v && set({ encoding: v })}
          />
          <NumberInput
            label={t("bankCsvHeaderRows")}
            min={0}
            max={20}
            value={mapping.headerRows}
            onChange={(v) => set({ headerRows: Number(v) || 0 })}
          />
          <Select
            label={t("bankCsvDateFormat")}
            data={[...csvDateFormats]}
            value={mapping.dateFormat}
            onChange={(v) => v && set({ dateFormat: v })}
          />
          <Select
            label={t("bankCsvDecimal")}
            data={[",", "."]}
            value={mapping.decimalSeparator}
            onChange={(v) => v && set({ decimalSeparator: v })}
          />
          <Select
            label={t("bankCsvThousands")}
            data={[
              { value: "", label: t("bankCsvNone") },
              { value: " ", label: t("bankCsvSpace") },
              { value: ".", label: "." },
              { value: ",", label: "," },
              { value: "'", label: "'" },
            ]}
            value={mapping.thousandsSeparator}
            onChange={(v) => set({ thousandsSeparator: v ?? "" })}
          />
        </SimpleGrid>
        <Checkbox
          label={t("bankCsvInvert")}
          checked={mapping.invertSign}
          onChange={(e) => set({ invertSign: e.currentTarget.checked })}
        />
        <Text size="sm" fw={500}>
          {t("bankCsvColumns")}
        </Text>
        <SimpleGrid cols={{ base: 2, sm: 4 }}>
          {columnKeys.map((key) => (
            <NumberInput
              key={key}
              label={t(`bankCsvColumn_${key}`)}
              min={1}
              max={200}
              value={columnOf(key)}
              onChange={(v) => setColumn(key, v)}
              required={key === "date"}
            />
          ))}
        </SimpleGrid>
        <Group align="flex-end">
          <FileInput
            label={t("bankCsvPreviewFile")}
            placeholder={t("bankChooseFile")}
            value={file}
            onChange={setFile}
            accept=".csv,.txt,text/csv"
          />
          <Button variant="light" onClick={() => read.mutate()} loading={read.isPending} disabled={!file}>
            {t("bankCsvPreview")}
          </Button>
          <Button onClick={() => save.mutate()} loading={save.isPending}>
            {t("bankCsvSave")}
          </Button>
        </Group>
        {(read.error || save.error) && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, (read.error ?? save.error) as Error)}
          </Alert>
        )}
        {preview && (
          <Table data-testid="bank-csv-preview">
            <Table.Caption>{t("bankCsvPreviewCount", { total: preview.total, errors: preview.errors })}</Table.Caption>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("bankCsvRow")}</Table.Th>
                <Table.Th>{t("date")}</Table.Th>
                <Table.Th>{t("text")}</Table.Th>
                <Table.Th ta="right">{t("amount")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {preview.rows.map((r) => (
                <Table.Tr key={r.row}>
                  <Table.Td>{r.row}</Table.Td>
                  {r.error ? (
                    <Table.Td colSpan={3} c="red">
                      {r.error}
                    </Table.Td>
                  ) : (
                    <>
                      <Table.Td>{day(r.bookedOn)}</Table.Td>
                      <Table.Td>{[r.text, r.counterpartyName].filter(Boolean).join(" · ")}</Table.Td>
                      <Table.Td ta="right">{money(r.amount)}</Table.Td>
                    </>
                  )}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};

/** The statements imported for the account, each with its file. */
const StatementsCard = ({ id }: { id: number }) => {
  const { t, day, money, stamp } = useAccountingFormat();
  const statements = useQuery(statementsQueryOptions(id));
  if (!statements.data || statements.data.length === 0) return null;
  return (
    <Card withBorder padding="md">
      <Title order={4} mb="xs">
        {t("bankStatements")}
      </Title>
      <Table data-testid="bank-statements">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("bankPeriod")}</Table.Th>
            <Table.Th>{t("bankSource")}</Table.Th>
            <Table.Th ta="right">{t("bankOpening")}</Table.Th>
            <Table.Th ta="right">{t("bankClosing")}</Table.Th>
            <Table.Th>{t("bankContinuity")}</Table.Th>
            <Table.Th ta="right">{t("bankImported")}</Table.Th>
            <Table.Th>{t("bankImportedAt")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {statements.data.map((s) => (
            <Table.Tr key={s.id}>
              <Table.Td>
                {day(s.periodFrom)} – {day(s.periodTo)}
              </Table.Td>
              <Table.Td>{t(`bankStatementSource_${s.source}`)}</Table.Td>
              <Table.Td ta="right">{money(s.openingBalance)}</Table.Td>
              <Table.Td ta="right">{money(s.closingBalance)}</Table.Td>
              <Table.Td title={s.continuityDetail}>{t(`bankContinuity_${s.continuity}`)}</Table.Td>
              <Table.Td ta="right">
                {s.linesImported} / {s.linesSkipped}
              </Table.Td>
              <Table.Td>{stamp(s.importedAt)}</Table.Td>
              <Table.Td>
                {s.hasFile && (
                  <Anchor href={statementFileUrl(s.id)} aria-label={t("bankDownloadFile")}>
                    <IconDownload size={16} />
                  </Anchor>
                )}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Card>
  );
};

/**
 * Import a statement (A27): a camt.053 file from any Norwegian bank — its
 * statements routed to the bank accounts by number — or a CSV file of one
 * bank account, read by that account's mapping. The answer says what was
 * imported, what was skipped as already there and which accounts are not
 * here.
 */
export const BankImportPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("bankImport")} description={t("bankImportDescription")} />
          {meta.canPost ? <ImportForm /> : <Alert color="gray">{t("bankImportNeedsPost")}</Alert>}
        </Stack>
      )}
    </BooksGate>
  );
};

const ImportForm = () => {
  const { t, day } = useAccountingFormat();
  const invalidate = useInvalidate();
  const accounts = useQuery(bankAccountsQueryOptions());
  const [file, setFile] = useState<File | null>(null);
  const [account, setAccount] = useState<string | null>(null);
  const [result, setResult] = useState<AccountingBankImportResult | null>(null);
  const isCsv = file !== null && !/\.xml$/i.test(file.name);
  const run = useMutation({
    mutationFn: () => {
      if (!file) throw new Error(t("bankChooseFile"));
      return importStatement(file, isCsv && account ? Number(account) : undefined);
    },
    onSuccess: async (answer) => {
      setResult(answer);
      await invalidate();
    },
  });
  return (
    <Stack gap="md">
      <Card withBorder padding="md">
        <Stack gap="sm">
          <FileInput
            label={t("bankStatementFile")}
            description={t("bankStatementFileHint")}
            placeholder={t("bankChooseFile")}
            value={file}
            onChange={(f) => {
              setFile(f);
              setResult(null);
            }}
            accept=".xml,.csv,.txt"
            data-assist="accounting.bank.file"
          />
          {isCsv && (
            <Select
              label={t("bankCsvAccount")}
              description={t("bankCsvAccountHint")}
              data={(accounts.data?.items ?? []).map((a) => ({
                value: String(a.id),
                label: `${a.name} ${a.accountNumber}`,
              }))}
              value={account}
              onChange={setAccount}
              required
            />
          )}
          <Group>
            <Button onClick={() => run.mutate()} loading={run.isPending} disabled={!file || (isCsv && !account)}>
              {t("bankImportRun")}
            </Button>
          </Group>
          {run.error && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bankImportFailed")}>
              {refusalMessage(t, run.error)}
            </Alert>
          )}
        </Stack>
      </Card>
      {result && (
        <Card withBorder padding="md" data-testid="bank-import-result">
          <Title order={4} mb="xs">
            {t("bankImportResult")}
          </Title>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("navBank")}</Table.Th>
                <Table.Th>{t("bankPeriod")}</Table.Th>
                <Table.Th ta="right">{t("bankImported")}</Table.Th>
                <Table.Th ta="right">{t("bankSkipped")}</Table.Th>
                <Table.Th>{t("bankContinuity")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {result.statements.map((s) => (
                <Table.Tr key={s.statementId}>
                  <Table.Td>
                    <Anchor component={Link} to={bankAccountPath(s.bankAccountId)}>
                      {s.bankAccountName}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    {day(s.periodFrom)} – {day(s.periodTo)}
                  </Table.Td>
                  <Table.Td ta="right">{s.imported}</Table.Td>
                  <Table.Td ta="right">{s.skipped}</Table.Td>
                  <Table.Td>
                    {s.repeated ? t("bankRepeated") : t(`bankContinuity_${s.continuity}`)}
                    {s.continuityDetail && (
                      <Text size="xs" c="dimmed">
                        {s.continuityDetail}
                      </Text>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {result.unknownAccounts.length > 0 && (
            <Alert color="orange" mt="sm" icon={<IconAlertCircle size={16} />} title={t("bankUnknownAccounts")}>
              {t("bankUnknownAccountsHint", { accounts: result.unknownAccounts.join(", ") })}
            </Alert>
          )}
          {result.notBooked > 0 && (
            <Text size="sm" c="dimmed" mt="sm">
              {t("bankNotBooked", { count: result.notBooked })}
            </Text>
          )}
        </Card>
      )}
    </Stack>
  );
};

/**
 * Bank rules (A28): a line whose text or counterparty holds the words is
 * proposed as a voucher to the rule's account, with its tax code. A change is
 * a new version; a rule a match was made with is made inactive, not deleted.
 */
export const BankRulesPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("bankRules")} description={t("bankRulesDescription")} />
          <RulesList canClose={meta.canClose} />
        </Stack>
      )}
    </BooksGate>
  );
};

const emptyRule: AccountingBankRuleInput = { name: "", accountId: 0, direction: "out" };

const RulesList = ({ canClose }: { canClose: boolean }) => {
  const { t } = useAccountingFormat();
  const rules = useQuery(bankRulesQueryOptions());
  const invalidate = useInvalidate();
  const [editing, setEditing] = useState<{ id?: number; rule: AccountingBankRuleInput } | null>(null);
  const remove = useMutation({
    mutationFn: (id: number) => deleteBankRule(id),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("deleted") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const edit = (r: AccountingBankRule) =>
    setEditing({
      id: r.id,
      rule: {
        name: r.name,
        bankAccountId: r.bankAccountId,
        textContains: r.textContains,
        counterpartyContains: r.counterpartyContains,
        direction: r.direction,
        accountId: r.accountId,
        taxCodeId: r.taxCodeId,
        projectId: r.projectId,
        description: r.description,
        active: r.active,
      },
    });
  if (rules.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {rules.error.message}
      </Alert>
    );
  if (rules.isPending) return <ContentSkeleton rows={3} />;
  return (
    <Stack gap="md">
      {canClose && (
        <Group justify="flex-end">
          <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing({ rule: emptyRule })}>
            {t("bankAddRule")}
          </Button>
        </Group>
      )}
      {rules.data.length === 0 ? (
        <EmptyState title={t("bankNoRules")} description={t("bankNoRulesHint")} />
      ) : (
        <Table striped data-testid="bank-rules">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("name")}</Table.Th>
              <Table.Th>{t("bankRuleWords")}</Table.Th>
              <Table.Th>{t("bankRuleDirection")}</Table.Th>
              <Table.Th>{t("account")}</Table.Th>
              <Table.Th>{t("taxCode")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rules.data.map((r) => (
              <Table.Tr key={r.id}>
                <Table.Td>
                  {r.name}{" "}
                  <Text span size="xs" c="dimmed">
                    v{r.version}
                  </Text>
                  {!r.active && (
                    <Badge ml="xs" variant="light" color="gray">
                      {t("inactive")}
                    </Badge>
                  )}
                </Table.Td>
                <Table.Td>{[r.textContains, r.counterpartyContains].filter(Boolean).join(" · ")}</Table.Td>
                <Table.Td>{t(`bankDirection_${r.direction}`)}</Table.Td>
                <Table.Td>
                  {r.accountNumber} {r.accountName}
                </Table.Td>
                <Table.Td>{r.taxCode}</Table.Td>
                <Table.Td>
                  {canClose && (
                    <Group gap="xs" justify="flex-end">
                      <Button size="xs" variant="subtle" onClick={() => edit(r)}>
                        {t("edit")}
                      </Button>
                      <Button size="xs" variant="subtle" color="red" onClick={() => remove.mutate(r.id)}>
                        {t("delete")}
                      </Button>
                    </Group>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {editing && <RuleForm id={editing.id} initial={editing.rule} onClose={() => setEditing(null)} />}
    </Stack>
  );
};

const RuleForm = ({ id, initial, onClose }: { id?: number; initial: AccountingBankRuleInput; onClose: () => void }) => {
  const { t } = useAccountingFormat();
  const invalidate = useInvalidate();
  const [form, setForm] = useState(initial);
  const set = (change: Partial<AccountingBankRuleInput>) => setForm((f) => ({ ...f, ...change }));
  const save = useMutation({
    mutationFn: () => (id === undefined ? createBankRule(form) : updateBankRule(id, form)),
    onSuccess: async () => {
      await invalidate();
      notifications.show({ color: "green", message: t("saved") });
      onClose();
    },
  });
  const fields = refusedFields(save.error);
  return (
    <Modal opened onClose={onClose} title={id === undefined ? t("bankAddRule") : t("bankEditRule")}>
      <Stack gap="sm">
        <TextInput
          label={t("name")}
          value={form.name}
          onChange={(e) => set({ name: e.currentTarget.value })}
          error={fields.name}
          required
        />
        <TextInput
          label={t("bankRuleText")}
          description={t("bankRuleTextHint")}
          value={form.textContains ?? ""}
          onChange={(e) => set({ textContains: e.currentTarget.value || null })}
          error={fields.textContains}
        />
        <TextInput
          label={t("bankRuleCounterparty")}
          value={form.counterpartyContains ?? ""}
          onChange={(e) => set({ counterpartyContains: e.currentTarget.value || null })}
          error={fields.counterpartyContains}
        />
        <Select
          label={t("bankRuleDirection")}
          data={["out", "in", "any"].map((d) => ({ value: d, label: t(`bankDirection_${d}`) }))}
          value={form.direction ?? "any"}
          onChange={(v) => set({ direction: v ?? "any" })}
        />
        <AccountSelect
          label={t("account")}
          value={form.accountId ? String(form.accountId) : null}
          onChange={(v) => set({ accountId: v ? Number(v) : 0 })}
          error={fields.accountId}
          required
        />
        <TaxCodeSelect
          label={t("taxCode")}
          value={form.taxCodeId ? String(form.taxCodeId) : null}
          onChange={(v) => set({ taxCodeId: v ? Number(v) : null })}
          error={fields.taxCodeId}
        />
        <TextInput
          label={t("description")}
          value={form.description ?? ""}
          onChange={(e) => set({ description: e.currentTarget.value || null })}
        />
        <Checkbox
          label={t("active")}
          checked={form.active !== false}
          onChange={(e) => set({ active: e.currentTarget.checked })}
        />
        {save.error && Object.keys(fields).length === 0 && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, save.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => save.mutate()} loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
