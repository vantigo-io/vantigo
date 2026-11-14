import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  List,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconDownload, IconInfoCircle, IconReceiptTax } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingMeta,
  type AccountingVatCodeFigures,
  type AccountingVatPart,
  type AccountingVatReturn,
  type AccountingVatSpecification,
  type AccountingVatTermDetail,
  type AccountingVatTermItem,
  closeVatTerm,
  fileVatTerm,
  reopenVatTerm,
  unfileVatTerm,
  vatPostingsQueryOptions,
  vatReturnXmlUrl,
  vatTermQueryOptions,
  vatTermsQueryOptions,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { VAT_ROUTE_PATH, vatPostingsPath, vatTermPath, voucherPath } from "../lib/routes";
import type { VatPostingsSearch } from "../lib/search";

/** The books have no VAT terms: the company is not VAT-registered (A2), and the screen says so. */
const NotRegistered = () => {
  const { t } = useAccountingFormat();
  return <EmptyState icon={IconReceiptTax} title={t("vatNotRegistered")} description={t("vatNotRegisteredHint")} />;
};

/** Filing is by hand at Skatteetaten in this phase (A23), and the screens say so plainly. */
const FiledAtSkatteetaten = () => {
  const { t } = useAccountingFormat();
  return (
    <Alert color="blue" icon={<IconInfoCircle size={16} />} title={t("vatFilingByHand")}>
      {t("vatFilingByHandHint")}
    </Alert>
  );
};

/** A term's name: `2026-3 · mai–juni 2026`. */
const useTermName = () => {
  const { t } = useAccountingFormat();
  return (term: Pick<AccountingVatTermItem, "year" | "number" | "periodName">) =>
    t("vatTermName", { year: term.year, number: term.number, period: term.periodName });
};

/** A term's state as a badge: open, closed or filed. */
const TermState = ({ term }: { term: AccountingVatTermItem }) => {
  const { t } = useAccountingFormat();
  if (term.filedAt) return <Badge color="green">{t("vatStateFiled")}</Badge>;
  if (term.state === "closed") return <Badge color="blue">{t("vatStateClosed")}</Badge>;
  return <Badge color="gray">{t("vatStateOpen")}</Badge>;
};

/** A return's total: to pay, to receive or nothing, in whole kroner. */
const useTotal = () => {
  const { t, money } = useAccountingFormat();
  return (total: string) => {
    const n = Number(total);
    if (n > 0) return t("vatToPay", { amount: money(total) });
    if (n < 0) return t("vatToReceive", { amount: money(String(-n)) });
    return t("vatNothing");
  };
};

/** The VAT terms (A22): each with its state, due date and total, and what waits. */
export const VatTermsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navVat")} description={t("vatDescription")} />
          {meta.settings?.vatTerm === "none" ? <NotRegistered /> : <Terms />}
        </Stack>
      )}
    </BooksGate>
  );
};

const Terms = () => {
  const { t, day } = useAccountingFormat();
  const name = useTermName();
  const total = useTotal();
  const terms = useQuery(vatTermsQueryOptions());
  if (terms.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, terms.error)}
      </Alert>
    );
  if (terms.isPending) return <ContentSkeleton rows={6} />;
  const list = [...terms.data.terms].reverse();
  return (
    <Stack gap="md">
      <FiledAtSkatteetaten />
      <Table striped data-assist="accounting.vat.terms">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("vatTermColumn")}</Table.Th>
            <Table.Th>{t("vatPeriod")}</Table.Th>
            <Table.Th>{t("vatState")}</Table.Th>
            <Table.Th>{t("vatDue")}</Table.Th>
            <Table.Th ta="right">{t("vatReturnTotal")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {list.map((term) => (
            <Table.Tr key={`${term.year}-${term.number}`}>
              <Table.Td>
                <Anchor component={Link} to={vatTermPath(term.year, term.number)}>
                  {name(term)}
                </Anchor>
              </Table.Td>
              <Table.Td>
                {day(term.periodStart)} – {day(term.periodEnd)}
              </Table.Td>
              <Table.Td>
                <Group gap={6}>
                  <TermState term={term} />
                  {term.needsClosing && <Badge color="orange">{t("vatNeedsClosing")}</Badge>}
                  {term.needsFiling && <Badge color="red">{t("vatNeedsFiling")}</Badge>}
                </Group>
              </Table.Td>
              <Table.Td>{day(term.dueDate)}</Table.Td>
              <Table.Td ta="right">{total(term.total)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  );
};

/** A term's page: the return as figures, the specification, the reconciliation, and close, reopen, download and filing. */
export const VatTermPage = () => {
  const { t } = useAccountingFormat();
  const { year, number } = useParams({ strict: false }) as { year: string; number: string };
  return (
    <BooksGate>
      {(meta) =>
        meta.settings?.vatTerm === "none" ? (
          <Stack gap="lg">
            <PageHeader title={t("navVat")} />
            <NotRegistered />
          </Stack>
        ) : (
          <Term meta={meta} year={Number(year)} number={Number(number)} />
        )
      }
    </BooksGate>
  );
};

const Term = ({ meta, year, number }: { meta: AccountingMeta; year: number; number: number }) => {
  const { t, day } = useAccountingFormat();
  const name = useTermName();
  const total = useTotal();
  const term = useQuery(vatTermQueryOptions(year, number));
  const [dialog, setDialog] = useState<"close" | "reopen" | "file" | "unfile" | null>(null);
  if (term.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, term.error)}
      </Alert>
    );
  if (term.isPending) return <ContentSkeleton rows={8} />;
  const d = term.data;
  const it = d.term;
  const closed = it.state === "closed";
  return (
    <Stack gap="lg">
      <PageHeader
        title={name(it)}
        description={t("vatTermDescription", {
          from: day(it.periodStart),
          to: day(it.periodEnd),
          due: day(it.dueDate),
        })}
        actions={
          <Group gap="sm">
            {!closed && meta.canClose && (
              <Button data-assist="accounting.vat.close" onClick={() => setDialog("close")}>
                {t("vatClose")}
              </Button>
            )}
            {closed && (
              <Button
                variant="light"
                leftSection={<IconDownload size={16} />}
                component="a"
                href={vatReturnXmlUrl(year, number)}
                data-assist="accounting.vat.xml"
              >
                {t("vatDownloadXml")}
              </Button>
            )}
            {closed && !it.filedAt && meta.canPost && (
              <Button variant="light" onClick={() => setDialog("file")}>
                {t("vatMarkFiled")}
              </Button>
            )}
            {closed && !it.filedAt && meta.canClose && (
              <Button variant="default" color="red" onClick={() => setDialog("reopen")}>
                {t("vatReopen")}
              </Button>
            )}
            {it.filedAt && meta.canClose && (
              <Button variant="default" onClick={() => setDialog("unfile")}>
                {t("vatUnmarkFiled")}
              </Button>
            )}
          </Group>
        }
      />
      <Group gap="sm">
        <TermState term={it} />
        <Text fw={600}>{total(d.return.total)}</Text>
        {it.settlementVoucher && (
          <Text size="sm">
            {t("vatSettlementVoucher")}{" "}
            <Anchor component={Link} to={voucherPath(it.settlementVoucher.id)}>
              {it.settlementVoucher.display}
            </Anchor>
          </Text>
        )}
        {it.filedAt && (
          <Text size="sm" c="dimmed">
            {t("vatFiledOn", { date: day(it.filedAt), reference: it.filedReference ?? "" })}
          </Text>
        )}
      </Group>
      {closed && !it.lockCovers && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
          {t("vatLockNotCovering")}
        </Alert>
      )}
      <FiledAtSkatteetaten />
      <ReturnFigures ret={d.return} closed={closed} />
      <Specification spec={d.specification} />
      <Events detail={d} />
      {dialog === "close" && <CloseDialog detail={d} year={year} number={number} onClose={() => setDialog(null)} />}
      {dialog === "reopen" && (
        <ReasonDialog
          title={t("vatReopenTitle", { term: name(it) })}
          hint={t("vatReopenHint", { day: day(it.periodStart) })}
          action={t("vatReopen")}
          run={(reason) => reopenVatTerm(year, number, reason)}
          done={t("vatReopened")}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === "unfile" && (
        <ReasonDialog
          title={t("vatUnmarkFiledTitle", { term: name(it) })}
          hint={t("vatUnmarkFiledHint")}
          action={t("vatUnmarkFiled")}
          run={(reason) => unfileVatTerm(year, number, reason)}
          done={t("vatUnmarkedFiled")}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === "file" && (
        <FileDialog year={year} number={number} today={meta.today} onClose={() => setDialog(null)} />
      )}
    </Stack>
  );
};

/** The return laid out as Skatteetaten's (A23): the groups, each code's lines, whole kroner. */
const ReturnFigures = ({ ret, closed }: { ret: AccountingVatReturn; closed: boolean }) => {
  const { t, money, amount } = useAccountingFormat();
  const total = useTotal();
  const groups = [...new Set(ret.lines.map((l) => l.group))];
  return (
    <Card withBorder padding="md" data-testid="vat-return">
      <Title order={4} mb="xs">
        {closed ? t("vatReturnClosed") : t("vatReturnNow")}
      </Title>
      {ret.lines.length === 0 ? (
        <Text c="dimmed">{t("vatReturnEmpty")}</Text>
      ) : (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("vatCode")}</Table.Th>
              <Table.Th>{t("name")}</Table.Th>
              <Table.Th ta="right">{t("vatBase")}</Table.Th>
              <Table.Th ta="right">{t("vatRate")}</Table.Th>
              <Table.Th ta="right">{t("vatAmount")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {groups.map((group) => (
              <GroupRows key={group} group={group} ret={ret} />
            ))}
            <Table.Tr>
              <Table.Td colSpan={4} fw={600}>
                {total(ret.total)}
              </Table.Td>
              <Table.Td ta="right" fw={600}>
                {money(ret.total)}
              </Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>
      )}
      <Text size="sm" c="dimmed" mt="xs">
        {t("vatRounding", { exact: amount(ret.exactTotal), difference: amount(ret.roundingDifference) })}
      </Text>
      {ret.warnings.length > 0 && (
        <List size="sm" mt="xs">
          {ret.warnings.map((w) => (
            <List.Item key={w}>{w}</List.Item>
          ))}
        </List>
      )}
    </Card>
  );
};

const GroupRows = ({ group, ret }: { group: string; ret: AccountingVatReturn }) => {
  const { t, money } = useAccountingFormat();
  return (
    <>
      <Table.Tr>
        <Table.Td colSpan={5} fw={600}>
          {t(`vatGroup_${group}`)}
        </Table.Td>
      </Table.Tr>
      {ret.lines
        .filter((l) => l.group === group)
        .map((l, i) => (
          <Table.Tr key={`${l.code}-${l.side}-${l.specification ?? ""}-${i}`}>
            <Table.Td>{l.code}</Table.Td>
            <Table.Td>
              {l.specification
                ? `${t("vatLossLine")} · ${l.name}`
                : l.side === "deduction"
                  ? `${t("vatDeduction")} · ${l.name}`
                  : l.name}
              {l.remark && (
                <Badge ml="xs" size="xs" color="orange">
                  {l.remark}
                </Badge>
              )}
            </Table.Td>
            <Table.Td ta="right">{l.base === null ? "" : money(l.base)}</Table.Td>
            <Table.Td ta="right">{l.rate === null ? "" : `${l.rate} %`}</Table.Td>
            <Table.Td ta="right">{money(l.vat)}</Table.Td>
          </Table.Tr>
        ))}
    </>
  );
};

/** The specification (A21): per code, every figure one click from its postings; the reconciliation below it. */
const Specification = ({ spec }: { spec: AccountingVatSpecification }) => {
  const { t, amount } = useAccountingFormat();
  const r = spec.reconciliation;
  return (
    <Card withBorder padding="md" data-testid="vat-specification">
      <Title order={4} mb="xs">
        {t("vatSpecification")}
      </Title>
      <FiguresTable spec={spec} figures={spec.codes} />
      {spec.notReported.length > 0 && (
        <>
          <Text fw={600} mt="md">
            {t("vatNotReported")}
          </Text>
          <Text size="sm" c="dimmed">
            {t("vatNotReportedHint")}
          </Text>
          <FiguresTable spec={spec} figures={spec.notReported} />
        </>
      )}
      <Title order={5} mt="md">
        {t("vatReconciliation")}
      </Title>
      {r.reconciled ? (
        <Text size="sm">{t("vatReconciled", { movement: amount(r.movement) })}</Text>
      ) : (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("vatNotReconciled")} mt="xs">
          <Stack gap="xs">
            <Text size="sm">
              {t("vatNotReconciledHint", {
                movement: amount(r.movement),
                counted: amount(r.counted),
                difference: amount(r.difference),
              })}
            </Text>
            <Table>
              <Table.Tbody>
                {r.postings.map((p) => (
                  <Table.Tr key={`${p.voucher.id}-${p.lineNo}`}>
                    <Table.Td>
                      <Anchor component={Link} to={voucherPath(p.voucher.id)}>
                        {p.voucher.display}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>
                      {p.accountNumber} {p.accountName}
                    </Table.Td>
                    <Table.Td>{p.voucherDescription}</Table.Td>
                    <Table.Td ta="right">{amount(p.amount)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        </Alert>
      )}
    </Card>
  );
};

const FiguresTable = ({
  spec,
  figures,
}: {
  spec: AccountingVatSpecification;
  figures: readonly AccountingVatCodeFigures[];
}) => {
  const { t, amount } = useAccountingFormat();
  const cell = (f: AccountingVatCodeFigures, value: string, part: AccountingVatPart) =>
    Number(value) === 0 ? (
      ""
    ) : (
      <Anchor component={Link} to={vatPostingsPath({ from: spec.from, to: spec.to, taxCodeId: f.taxCodeId, part })}>
        {amount(value)}
      </Anchor>
    );
  return (
    <Table striped>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t("vatCode")}</Table.Th>
          <Table.Th>{t("name")}</Table.Th>
          <Table.Th ta="right">{t("vatBase")}</Table.Th>
          <Table.Th ta="right">{t("vatOutput")}</Table.Th>
          <Table.Th ta="right">{t("vatCalculated")}</Table.Th>
          <Table.Th ta="right">{t("vatDeducted")}</Table.Th>
          <Table.Th ta="right">{t("vatLoss")}</Table.Th>
          <Table.Th ta="right">{t("vatNotDeducted")}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {figures.map((f) => (
          <Table.Tr key={`${f.taxCodeId}-${f.ratePercent ?? ""}-${f.specification ?? ""}`}>
            <Table.Td>{f.code}</Table.Td>
            <Table.Td>{f.specification ? `${f.name} · ${t("vatLossLine")}` : f.name}</Table.Td>
            <Table.Td ta="right">{cell(f, f.base, f.specification ? "loss_base" : "base")}</Table.Td>
            <Table.Td ta="right">{cell(f, f.outputVat, "output")}</Table.Td>
            <Table.Td ta="right">{cell(f, f.calculatedVat, "calculated")}</Table.Td>
            <Table.Td ta="right">{cell(f, f.deductedVat, "deducted")}</Table.Td>
            <Table.Td ta="right">{cell(f, f.lossVat, "loss")}</Table.Td>
            <Table.Td ta="right">{cell(f, f.notDeductedVat, "not_deducted")}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
};

/** What happened to the term: closes, reopenings, filings and their removal. */
const Events = ({ detail }: { detail: AccountingVatTermDetail }) => {
  const { t, stamp, day } = useAccountingFormat();
  if (detail.events.length === 0) return null;
  return (
    <Card withBorder padding="md">
      <Title order={5} mb="xs">
        {t("vatHistory")}
      </Title>
      <Stack gap={4}>
        {detail.events.map((e) => (
          <Text size="sm" key={`${e.kind}-${e.at}`}>
            <b>{t(`vatEvent_${e.kind}`)}</b> · {stamp(e.at)}
            {e.voucher && (
              <>
                {" · "}
                <Anchor component={Link} to={voucherPath(e.voucher.id)}>
                  {e.voucher.display}
                </Anchor>
              </>
            )}
            {e.filedAt && ` · ${day(e.filedAt)} ${e.filedReference ?? ""}`}
            {e.reason && ` · ${e.reason}`}
          </Text>
        ))}
      </Stack>
    </Card>
  );
};

/** The close's confirmation (A22, R10): what it will do, and each refusal it would meet now, explained. */
const CloseDialog = ({
  detail,
  year,
  number,
  onClose,
}: {
  detail: AccountingVatTermDetail;
  year: number;
  number: number;
  onClose: () => void;
}) => {
  const { t, day, money, amount } = useAccountingFormat();
  const name = useTermName();
  const queryClient = useQueryClient();
  const c = detail.checks;
  const it = detail.term;
  const blockers = [
    !c.ended && t("vatCheckNotEnded", { day: day(it.periodEnd) }),
    c.earlierOpen && t("vatCheckEarlierOpen", { term: name(c.earlierOpen) }),
    c.attention > 0 && t("vatCheckAttention", { count: c.attention }),
    c.lockAfterEnd && t("vatCheckLockAfterEnd", { day: day(it.periodEnd) }),
    !c.reconciled && t("vatCheckUnreconciled"),
  ].filter((b): b is string => typeof b === "string");
  const close = useMutation({
    mutationFn: () => closeVatTerm(year, number),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("vatClosed", { term: name(it) }) });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("vatCloseTitle", { term: name(it) })} size="lg">
      <Stack gap="sm">
        <Text size="sm">{t("vatCloseWhat")}</Text>
        <List size="sm">
          <List.Item>{t("vatCloseSnapshot", { total: money(detail.return.total) })}</List.Item>
          <List.Item>
            {t("vatCloseSettlement", { day: day(it.periodEnd), difference: amount(detail.return.roundingDifference) })}
          </List.Item>
          <List.Item>{t("vatCloseLock", { day: day(it.periodEnd) })}</List.Item>
          <List.Item>{t("vatCloseXml")}</List.Item>
        </List>
        {blockers.length > 0 && (
          <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("vatCloseBlocked")}>
            <List size="sm">
              {blockers.map((b) => (
                <List.Item key={b}>{b}</List.Item>
              ))}
            </List>
          </Alert>
        )}
        {close.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("vatCouldNotClose")}>
            {refusalMessage(t, close.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => close.mutate()} loading={close.isPending} disabled={blockers.length > 0}>
            {t("vatClose")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** A reopening's or an unfiling's reason. */
const ReasonDialog = ({
  title,
  hint,
  action,
  run,
  done,
  onClose,
}: {
  title: string;
  hint: string;
  action: string;
  run: (reason: string) => Promise<unknown>;
  done: string;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const mutation = useMutation({
    mutationFn: () => run(reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: done });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={title}>
      <Stack gap="sm">
        <Text size="sm">{hint}</Text>
        <TextInput required label={t("reason")} value={reason} onChange={(e) => setReason(e.currentTarget.value)} />
        {mutation.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, mutation.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color="red" onClick={() => mutation.mutate()} loading={mutation.isPending} disabled={!reason.trim()}>
            {action}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Mark as filed: the day it was filed at Skatteetaten and its reference. */
const FileDialog = ({
  year,
  number,
  today,
  onClose,
}: {
  year: number;
  number: number;
  today: string;
  onClose: () => void;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [filedAt, setFiledAt] = useState(today);
  const [reference, setReference] = useState("");
  const file = useMutation({
    mutationFn: () => fileVatTerm(year, number, { filedAt, reference: reference.trim() }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("vatMarkedFiled") });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("vatMarkFiled")}>
      <Stack gap="sm">
        <Text size="sm">{t("vatMarkFiledHint")}</Text>
        <TextInput
          type="date"
          label={t("vatFiledAt")}
          value={filedAt}
          onChange={(e) => setFiledAt(e.currentTarget.value)}
        />
        <TextInput
          required
          label={t("vatFiledReference")}
          value={reference}
          onChange={(e) => setReference(e.currentTarget.value)}
        />
        {file.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, file.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => file.mutate()} loading={file.isPending} disabled={!reference.trim() || !filedAt}>
            {t("vatMarkFiled")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** The postings behind a VAT figure (A21): a period, and a code and a part, or the ones that keep the accounts from reconciling. */
export const VatPostingsPage = () => {
  const { t } = useAccountingFormat();
  const search = useSearch({ strict: false }) as VatPostingsSearch;
  return (
    <BooksGate>
      {() => (
        <Stack gap="lg">
          <PageHeader
            title={t("vatPostings")}
            description={
              search.from && search.to ? t("vatPostingsDescription", { from: search.from, to: search.to }) : undefined
            }
            actions={
              <Button variant="default" renderRoot={(props) => <Link to={VAT_ROUTE_PATH} {...props} />}>
                {t("navVat")}
              </Button>
            }
          />
          {search.from && search.to ? (
            <Postings search={{ ...search, from: search.from, to: search.to }} />
          ) : (
            <Text c="dimmed">{t("vatPostingsChoose")}</Text>
          )}
        </Stack>
      )}
    </BooksGate>
  );
};

const PAGE = 100;

const Postings = ({ search }: { search: VatPostingsSearch & { from: string; to: string } }) => {
  const { t, day, amount } = useAccountingFormat();
  const [offset, setOffset] = useState(0);
  const list = useQuery(vatPostingsQueryOptions({ ...search, offset, limit: PAGE }));
  if (list.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, list.error)}
      </Alert>
    );
  if (list.isPending) return <ContentSkeleton rows={6} />;
  const sum = list.data.items.reduce((s, p) => s + Math.round(Number(p.amount) * 100), 0) / 100;
  return (
    <Stack gap="sm">
      <Table striped>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("voucher")}</Table.Th>
            <Table.Th>{t("date")}</Table.Th>
            <Table.Th>{t("account")}</Table.Th>
            <Table.Th>{t("vatCode")}</Table.Th>
            <Table.Th>{t("vatPart")}</Table.Th>
            <Table.Th>{t("text")}</Table.Th>
            <Table.Th ta="right">{t("amount")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {list.data.items.map((p) => (
            <Table.Tr key={`${p.voucher.id}-${p.lineNo}`}>
              <Table.Td>
                <Anchor component={Link} to={voucherPath(p.voucher.id)}>
                  {p.voucher.display}
                </Anchor>
              </Table.Td>
              <Table.Td>{day(p.voucherDate)}</Table.Td>
              <Table.Td>
                {p.accountNumber} {p.accountName}
              </Table.Td>
              <Table.Td>{p.taxCode ?? ""}</Table.Td>
              <Table.Td>{t(`vatPart_${p.part}`)}</Table.Td>
              <Table.Td>{p.description || p.voucherDescription}</Table.Td>
              <Table.Td ta="right">{amount(p.amount)}</Table.Td>
            </Table.Tr>
          ))}
          <Table.Tr>
            <Table.Td colSpan={6} fw={600}>
              {t("vatPostingsTotal", { count: list.data.total })}
            </Table.Td>
            <Table.Td ta="right" fw={600}>
              {amount(sum.toFixed(2))}
            </Table.Td>
          </Table.Tr>
        </Table.Tbody>
      </Table>
      <Group>
        <Button variant="default" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
          {t("previous")}
        </Button>
        <Button variant="default" disabled={offset + PAGE >= list.data.total} onClick={() => setOffset(offset + PAGE)}>
          {t("next")}
        </Button>
      </Group>
    </Stack>
  );
};
