import {
  Alert,
  Button,
  Card,
  Checkbox,
  FileInput,
  Group,
  Modal,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  BANK_FORMATS,
  BANK_REASONS,
  BANK_STATUSES,
  type BankAccount,
  type BankFile,
  type BankImportResult,
  type BankTransactionFilters,
  type BankTransactionReason,
  type BankTransactionStatus,
  bankAccountsQueryOptions,
  bankFileChoicesQueryOptions,
  bankFilesQueryOptions,
  bankTransactionsQueryOptions,
  matchRest,
  setAccountFormat,
  uploadBankFile,
} from "../api/bank";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { BankFileLink, BankTransactionTable } from "../components/bank-transaction-table";
import "../i18n";
import { accountNumber, useWho } from "../lib/bank";
import { fieldRefusals, refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface PaymentsPageProps {
  /** The signed-in user's id, so an import or an event of theirs says "you". */
  currentUserId?: string;
}

/**
 * The Payments area (invoices payments and reminders design D22): import a
 * bank file and read what became of it, the accounts with their formats —
 * changed by `invoices:manage`, the cutover explained — the imported files
 * with "Match the rest" where matching stopped early, and the exception
 * queue with its filters and actions. The route is guarded by
 * `invoices:payments`; meta's `canImportBankFiles` says the same.
 */
export const PaymentsPage = ({ currentUserId }: PaymentsPageProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  // The last answer shown: an upload's, or a "Match the rest"'s — whose
  // amounts are what that request matched, not the file's.
  const [result, setResult] = useState<{ answer: BankImportResult; fromMatch: boolean } | null>(null);
  const uploaded = (answer: BankImportResult) => setResult({ answer, fromMatch: false });
  const matched = (answer: BankImportResult) => setResult({ answer, fromMatch: true });
  const canAct = Boolean(meta.data?.capabilities.canImportBankFiles);
  const canManage = Boolean(meta.data?.capabilities.canManage);
  const currency = meta.data?.currency ?? "NOK";
  return (
    <Stack gap="lg">
      <PageHeader title={t("bank.title")} description={t("bank.description")} />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {meta.data && !canAct && (
        <Alert color="gray" icon={<IconAlertCircle size={16} />}>
          {t("bank.needsPayments")}
        </Alert>
      )}
      {meta.data && canAct && (
        <>
          <UploadCard currency={currency} currentUserId={currentUserId} onResult={uploaded} />
          {result && (
            <ImportResultCard
              file={result.answer.file}
              matchedAmount={result.answer.matchedAmount}
              exceptionsAmount={result.answer.exceptionsAmount}
              fromMatch={result.fromMatch}
              currency={currency}
              canAct={canAct}
              onMatched={matched}
              linkToFile
            />
          )}
          <AccountsCard canManage={canManage} />
          <FilesCard canAct={canAct} currentUserId={currentUserId} onMatched={matched} />
          <QueueCard currency={currency} canAct={canAct} currentUserId={currentUserId} />
        </>
      )}
    </Stack>
  );
};

/** An upload's refusal in words, and the earlier import a duplicate names. */
interface UploadRefusal {
  words: string;
  where?: string;
  earlier?: { id: number; uploadedBy?: string };
}

const UploadCard = ({
  currency,
  currentUserId,
  onResult,
}: {
  currency: string;
  currentUserId?: string;
  onResult: (result: BankImportResult) => void;
}) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [refusal, setRefusal] = useState<UploadRefusal | null>(null);
  const upload = useMutation({
    mutationFn: (chosen: File) => uploadBankFile(chosen),
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bank.imported") });
      setFile(null);
      onResult(answer);
    },
    onError: (error) => {
      // A file its own rules refuse is a 400 on `file`, whose message names
      // the record or the element: said in words, the place kept beside them.
      if (error instanceof ApiValidationError) {
        setRefusal({ words: t("bank.fileRefused"), where: error.errors.file?.[0] });
        return;
      }
      if ((error as { status?: unknown } | null)?.status === 400) {
        setRefusal({ words: t("bank.fileRefused") });
        return;
      }
      const code = refusalCode(error);
      if (code === "storage_unavailable") {
        setRefusal({ words: t("bank.uploadRefusal.storage_unavailable") });
        return;
      }
      const words = refusalMessage(error, t, date, (value) => money(value, currency), dateTime);
      const problem = refusalProblem(error);
      if (code === "bank_file_duplicate" && typeof problem.bankFileId === "number") {
        setRefusal({
          words,
          earlier: {
            id: problem.bankFileId,
            uploadedBy: typeof problem.uploadedBy === "string" ? problem.uploadedBy : undefined,
          },
        });
        return;
      }
      setRefusal({ words });
    },
  });
  return (
    <Card withBorder>
      <Stack gap="sm">
        <Title order={4}>{t("bank.upload")}</Title>
        <Text size="sm" c="dimmed">
          {t("bank.uploadDescription")}
        </Text>
        <Group align="flex-end">
          <FileInput
            label={t("bank.file")}
            placeholder={t("bank.filePlaceholder")}
            accept=".txt,.ocr,.xml,.dat,text/plain,application/xml,text/xml"
            leftSection={<IconFileUpload size={16} />}
            clearable
            value={file}
            onChange={(chosen) => {
              setFile(chosen);
              setRefusal(null);
            }}
            miw={320}
          />
          <Button
            disabled={!file || upload.isPending}
            loading={upload.isPending}
            onClick={() => file && upload.mutate(file)}
          >
            {t("bank.import")}
          </Button>
        </Group>
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.couldNotImport")}>
            <Stack gap={4}>
              <Text size="sm">{refusal.words}</Text>
              {refusal.where && (
                <Text size="sm" c="dimmed">
                  {t("bank.fileRefusedWhere", { detail: refusal.where })}
                </Text>
              )}
              {refusal.earlier && (
                <Text size="sm">
                  {t("bank.duplicateUploadedBy", { who: who(refusal.earlier.uploadedBy) })}{" "}
                  <BankFileLink bankFileId={refusal.earlier.id}>
                    {t("bank.openEarlierFile", { id: refusal.earlier.id })}
                  </BankFileLink>
                </Text>
              )}
            </Stack>
          </Alert>
        )}
      </Stack>
    </Card>
  );
};

/** "Match the rest" of a file whose matching stopped early, the caller registering the payments. */
export const MatchRestButton = ({
  bankFileId,
  onMatched,
}: {
  bankFileId: number;
  onMatched?: (result: BankImportResult) => void;
}) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const match = useMutation({
    mutationFn: () => matchRest(bankFileId),
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({
        color: "green",
        message: t("bank.matchedRest", {
          matched: answer.matched,
          exceptions: answer.exceptions,
          pending: answer.pending,
        }),
      });
      onMatched?.(answer);
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("bank.couldNotMatch"), message: refusalMessage(error, t, date) }),
  });
  return (
    <Button
      size="xs"
      aria-label={t("bank.matchRestOf", { id: bankFileId })}
      disabled={match.isPending}
      loading={match.isPending}
      onClick={() => match.mutate()}
    >
      {t("bank.matchRest")}
    </Button>
  );
};

export interface ImportResultCardProps {
  file: BankFile;
  /** The upload's and "Match the rest"'s answer carries the amounts; a file's row does not. */
  matchedAmount?: number;
  exceptionsAmount?: number;
  /** The amounts are a "Match the rest"'s — what that request matched and queued, not the whole file's. */
  fromMatch?: boolean;
  currency: string;
  canAct: boolean;
  onMatched?: (result: BankImportResult) => void;
  /** Whether the card links to the file's own page — the upload's result does. */
  linkToFile?: boolean;
}

/**
 * What became of a file's lines (D3 step 9): its payments, matched, queued,
 * already imported, not yet matched and ignored by kind — with "Match the
 * rest" while some are not yet matched.
 */
export const ImportResultCard = ({
  file,
  matchedAmount,
  exceptionsAmount,
  fromMatch = false,
  currency,
  canAct,
  onMatched,
  linkToFile = false,
}: ImportResultCardProps) => {
  const { t, money } = useInvoiceFormat();
  const counts: [string, number][] = [
    ["bank.count.transactions", file.transactions],
    ["bank.count.matched", file.matched],
    ["bank.count.exceptions", file.exceptions],
    ["bank.count.duplicates", file.duplicates],
    ["bank.count.pending", file.pending],
    ["bank.count.ignored", file.ignored],
  ];
  const ignoredKinds = (["debit", "notBooked", "cardInformation", "zeroAmount"] as const).filter(
    (kind) => file.ignoredKinds[kind] > 0,
  );
  return (
    <Card withBorder data-testid="import-result">
      <Stack gap="sm">
        <Group justify="space-between">
          <Title order={4}>{t("bank.result")}</Title>
          {linkToFile && <BankFileLink bankFileId={file.id}>{t("bank.openFile")}</BankFileLink>}
        </Group>
        <SimpleGrid cols={{ base: 2, sm: 3, md: 6 }}>
          {counts.map(([key, value]) => (
            <Stack key={key} gap={0}>
              <Text size="xs" c="dimmed">
                {t(key)}
              </Text>
              <Text fw={600} data-testid={`count-${key.slice("bank.count.".length)}`}>
                {value}
              </Text>
            </Stack>
          ))}
        </SimpleGrid>
        {matchedAmount !== undefined && (
          <Text size="sm">
            {t(fromMatch ? "bank.matchedAmountByMatch" : "bank.matchedAmount", {
              amount: money(matchedAmount, currency),
            })}
          </Text>
        )}
        {exceptionsAmount !== undefined && exceptionsAmount > 0 && (
          <Text size="sm">
            {t(fromMatch ? "bank.exceptionsAmountByMatch" : "bank.exceptionsAmount", {
              amount: money(exceptionsAmount, currency),
            })}
          </Text>
        )}
        {ignoredKinds.map((kind) => (
          <Text key={kind} size="sm" c="dimmed">
            {t(`bank.ignored.${kind}`, { count: file.ignoredKinds[kind] })}
          </Text>
        ))}
        {file.pending > 0 && (
          <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
            <Stack gap="xs">
              <Text size="sm">{t("bank.pendingNote")}</Text>
              {canAct && (
                <Group>
                  <MatchRestButton bankFileId={file.id} onMatched={onMatched} />
                </Group>
              )}
            </Stack>
          </Alert>
        )}
      </Stack>
    </Card>
  );
};

const formatLabel = (t: (key: string) => string, format: string): string =>
  (BANK_FORMATS as readonly string[]).includes(format) ? t(`bank.format.${format}`) : format;

const AccountsCard = ({ canManage }: { canManage: boolean }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const accounts = useQuery(bankAccountsQueryOptions());
  const [changing, setChanging] = useState<BankAccount | null>(null);
  return (
    <Card withBorder data-testid="bank-accounts">
      <Stack gap="sm">
        <Title order={4}>{t("bank.accounts")}</Title>
        <Text size="sm" c="dimmed">
          {t("bank.accountsDescription")}
        </Text>
        {accounts.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.failedToLoad")}>
            {refusalMessage(accounts.error, t, date)}
          </Alert>
        )}
        {accounts.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
        {accounts.data && accounts.data.data.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("bank.noAccounts")}
          </Text>
        )}
        {accounts.data && accounts.data.data.length > 0 && (
          <Table.ScrollContainer minWidth={640}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("bank.account")}</Table.Th>
                  <Table.Th>{t("bank.format")}</Table.Th>
                  <Table.Th>{t("bank.lastFile")}</Table.Th>
                  <Table.Th>{t("bank.lastBookedOn")}</Table.Th>
                  {canManage && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {accounts.data.data.map((a) => (
                  <Table.Tr key={a.account}>
                    <Table.Td>{accountNumber(a.account)}</Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        <Text size="sm">{formatLabel(t, a.format)}</Text>
                        {a.previousFormat && (
                          <Text size="xs" c="dimmed">
                            {a.cutoverThrough
                              ? t("bank.cutover", {
                                  format: formatLabel(t, a.previousFormat),
                                  date: date(a.cutoverThrough),
                                })
                              : t("bank.cutoverNone", { format: formatLabel(t, a.previousFormat) })}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {a.lastFileId !== undefined ? (
                        <BankFileLink bankFileId={a.lastFileId}>
                          {t("bank.fileName", { id: a.lastFileId })}
                          {a.lastUploadedAt && `, ${dateTime(a.lastUploadedAt)}`}
                        </BankFileLink>
                      ) : (
                        t("notAvailable")
                      )}
                    </Table.Td>
                    <Table.Td>{a.lastBookedOn ? date(a.lastBookedOn) : t("notAvailable")}</Table.Td>
                    {canManage && (
                      <Table.Td ta="right">
                        <Button
                          size="xs"
                          variant="default"
                          aria-label={t("bank.changeFormatOf", { account: accountNumber(a.account) })}
                          onClick={() => setChanging(a)}
                        >
                          {t("bank.changeFormat")}
                        </Button>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {!canManage && accounts.data && accounts.data.data.length > 0 && (
          <Text size="xs" c="dimmed">
            {t("bank.formatNeedsManage")}
          </Text>
        )}
      </Stack>
      {changing && <FormatModal account={changing} onClose={() => setChanging(null)} />}
    </Card>
  );
};

/**
 * Changes an account's format (`invoices:manage`), the cutover explained
 * before the change is made: the old format kept as the previous one, and a
 * payment of the new format booked on or before its last booking day held back
 * as a possible duplicate.
 */
const FormatModal = ({ account, onClose }: { account: BankAccount; onClose: () => void }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const other = BANK_FORMATS.find((f) => f !== account.format) ?? BANK_FORMATS[0];
  const [format, setFormat] = useState<string>(other);
  const [refusal, setRefusal] = useState<string | null>(null);
  const change = useMutation({
    mutationFn: () => setAccountFormat(account.account, format),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bank.formatChanged") });
      onClose();
    },
    onError: (error) =>
      setRefusal(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "bankFormat").elsewhere.join(" ")
          : refusalMessage(error, t, date),
      ),
  });
  return (
    <Modal opened onClose={onClose} title={t("bank.changeFormatOf", { account: accountNumber(account.account) })}>
      <Stack>
        <Select
          label={t("bank.newFormat")}
          data={BANK_FORMATS.map((f) => ({ value: f, label: t(`bank.format.${f}`) }))}
          value={format}
          allowDeselect={false}
          onChange={(value) => {
            if (value) setFormat(value);
            setRefusal(null);
          }}
        />
        <Text size="sm">{t("bank.cutoverExplained")}</Text>
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.couldNotChangeFormat")}>
            {refusal}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={change.isPending || format === account.format}
            loading={change.isPending}
            onClick={() => change.mutate()}
          >
            {t("bank.changeFormat")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

const FilesCard = ({
  canAct,
  currentUserId,
  onMatched,
}: {
  canAct: boolean;
  currentUserId?: string;
  onMatched: (result: BankImportResult) => void;
}) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [page, setPage] = useState(1);
  const files = useQuery(bankFilesQueryOptions(page));
  return (
    <Card withBorder data-testid="bank-files">
      <Stack gap="sm">
        <Title order={4}>{t("bank.files")}</Title>
        {files.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.failedToLoad")}>
            {refusalMessage(files.error, t, date)}
          </Alert>
        )}
        {files.isPending && <ContentSkeleton rows={3} rowHeight={32} />}
        {files.data && files.data.data.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("bank.noFiles")}
          </Text>
        )}
        {files.data && files.data.data.length > 0 && (
          <Table.ScrollContainer minWidth={900}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("bank.filter.file")}</Table.Th>
                  <Table.Th>{t("bank.format")}</Table.Th>
                  <Table.Th>{t("bank.uploadedAt")}</Table.Th>
                  <Table.Th>{t("bank.bookingDays")}</Table.Th>
                  <Table.Th ta="right">{t("bank.count.transactions")}</Table.Th>
                  <Table.Th ta="right">{t("bank.count.matched")}</Table.Th>
                  <Table.Th ta="right">{t("bank.count.exceptions")}</Table.Th>
                  <Table.Th ta="right">{t("bank.count.duplicates")}</Table.Th>
                  <Table.Th ta="right">{t("bank.count.pending")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {files.data.data.map((f) => (
                  <Table.Tr key={f.id}>
                    <Table.Td>
                      <BankFileLink bankFileId={f.id}>{t("bank.fileName", { id: f.id })}</BankFileLink>
                    </Table.Td>
                    <Table.Td>{formatLabel(t, f.format)}</Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        <Text size="sm">{dateTime(f.uploadedAt)}</Text>
                        <Text size="xs" c="dimmed">
                          {who(f.uploadedBy)}
                        </Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {f.firstBookedOn && f.lastBookedOn
                        ? t("bank.bookingDaysRange", { from: date(f.firstBookedOn), to: date(f.lastBookedOn) })
                        : t("notAvailable")}
                    </Table.Td>
                    <Table.Td ta="right">{f.transactions}</Table.Td>
                    <Table.Td ta="right">{f.matched}</Table.Td>
                    <Table.Td ta="right">{f.exceptions}</Table.Td>
                    <Table.Td ta="right">{f.duplicates}</Table.Td>
                    <Table.Td ta="right">
                      {f.pending > 0 && canAct ? (
                        <Group gap="xs" justify="flex-end" wrap="nowrap">
                          <Text size="sm">{f.pending}</Text>
                          <MatchRestButton bankFileId={f.id} onMatched={onMatched} />
                        </Group>
                      ) : (
                        f.pending
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {files.data && files.data.pagination.totalPages > 1 && (
          <Pagination total={files.data.pagination.totalPages} value={page} onChange={setPage} />
        )}
      </Stack>
    </Card>
  );
};

const QueueCard = ({
  currency,
  canAct,
  currentUserId,
}: {
  currency: string;
  canAct: boolean;
  currentUserId?: string;
}) => {
  const { t, date } = useInvoiceFormat();
  // The queue opens on what waits for a person: the exceptions.
  const [status, setStatus] = useState<BankTransactionStatus | "">("exception");
  const [reason, setReason] = useState<BankTransactionReason | "">("");
  const [bankFileId, setBankFileId] = useState<string>("");
  const [unapplied, setUnapplied] = useState(false);
  const [page, setPage] = useState(1);
  const files = useQuery(bankFileChoicesQueryOptions());
  const filters: BankTransactionFilters = {
    ...(status ? { status } : {}),
    ...(reason ? { reason } : {}),
    ...(bankFileId ? { bankFileId: Number(bankFileId) } : {}),
    ...(unapplied ? { unapplied: true } : {}),
    page,
  };
  const lines = useQuery(bankTransactionsQueryOptions(filters));
  return (
    <Card withBorder data-testid="bank-queue">
      <Stack gap="sm">
        <Title order={4}>{t("bank.queue")}</Title>
        <Text size="sm" c="dimmed">
          {t("bank.queueDescription")}
        </Text>
        <Text size="xs" c="dimmed">
          {t("bank.queueDuplicatesHint")}
        </Text>
        <Group align="flex-end">
          <Select
            label={t("bank.filter.status")}
            data={[
              { value: "", label: t("bank.filter.anyStatus") },
              ...BANK_STATUSES.map((s) => ({ value: s, label: t(`bank.status.${s}`) })),
            ]}
            value={status}
            allowDeselect={false}
            onChange={(value) => {
              setStatus((value ?? "") as BankTransactionStatus | "");
              setPage(1);
            }}
          />
          <Select
            label={t("bank.filter.reason")}
            data={[
              { value: "", label: t("bank.filter.anyReason") },
              ...BANK_REASONS.map((r) => ({ value: r, label: t(`bank.reason.${r}`) })),
            ]}
            value={reason}
            allowDeselect={false}
            onChange={(value) => {
              setReason((value ?? "") as BankTransactionReason | "");
              setPage(1);
            }}
          />
          <Select
            label={t("bank.filter.file")}
            searchable
            nothingFoundMessage={t("bank.filter.noFile")}
            data={[
              { value: "", label: t("bank.filter.anyFile") },
              ...(files.data?.data ?? []).map((f) => ({
                value: String(f.id),
                label: t("bank.fileName", { id: f.id }),
              })),
            ]}
            value={bankFileId}
            allowDeselect={false}
            onChange={(value) => {
              setBankFileId(value ?? "");
              setPage(1);
            }}
          />
          <Checkbox
            label={t("bank.filter.unapplied")}
            checked={unapplied}
            onChange={(event) => {
              const on = event.currentTarget.checked;
              setUnapplied(on);
              // An unapplied rest is on a matched or resolved line, never on an
              // exception: the status filter is let go.
              if (on) setStatus("");
              setPage(1);
            }}
          />
        </Group>
        {lines.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.failedToLoad")}>
            {refusalMessage(lines.error, t, date)}
          </Alert>
        )}
        {lines.isPending && <ContentSkeleton rows={3} rowHeight={40} />}
        {lines.data && lines.data.data.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("bank.noLines")}
          </Text>
        )}
        {lines.data && lines.data.data.length > 0 && (
          <BankTransactionTable
            lines={lines.data.data}
            currency={currency}
            canAct={canAct}
            currentUserId={currentUserId}
            showFile
          />
        )}
        {lines.data && lines.data.pagination.totalPages > 1 && (
          <Pagination total={lines.data.pagination.totalPages} value={page} onChange={setPage} />
        )}
      </Stack>
    </Card>
  );
};
