import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  FileInput,
  Group,
  List,
  Select,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconFileImport, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import {
  type AccountingSaftImport,
  type AccountingVatTerm,
  applySaftImport,
  linkSaftImportCustomer,
  matchSaftImport,
  saftImportAttentionQueryOptions,
  saftImportQueryOptions,
  uploadSaftImport,
  vatTerms,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { CustomerSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { ACCOUNTING_HOME_ROUTE_PATH } from "../lib/routes";

/**
 * Coming from another system? (A25): the other way to set up the books. The
 * person uploads the old system's SAF-T file, reads what it would write —
 * the chart, the problems with their fixes, the roles, the tax codes, the
 * suppliers, the customers matched in Customers, the opening balances — links
 * a customer Customers does not match, and applies it.
 */
export const SaftImportPanel = () => {
  const { t } = useAccountingFormat();
  const [file, setFile] = useState<File | null>(null);
  const [importId, setImportId] = useState<number | null>(null);
  const upload = useMutation({
    mutationFn: (f: File) => uploadSaftImport(f),
    onSuccess: (preview) => setImportId(preview.id),
  });
  return (
    <Card withBorder padding="lg" data-assist="accounting.saft.import">
      <Stack gap="md">
        <Title order={4}>{t("saftImportTitle")}</Title>
        <Text size="sm">{t("saftImportHint")}</Text>
        {importId === null ? (
          <>
            <FileInput
              label={t("saftImportFile")}
              accept=".xml,application/xml,text/xml"
              value={file}
              onChange={setFile}
              leftSection={<IconFileImport size={16} />}
            />
            {upload.error && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("saftImportRefused")}>
                {refusalMessage(t, upload.error)}
              </Alert>
            )}
            <Button disabled={!file} loading={upload.isPending} onClick={() => file && upload.mutate(file)}>
              {t("saftImportRead")}
            </Button>
          </>
        ) : (
          <Preview id={importId} />
        )}
      </Stack>
    </Card>
  );
};

const matchColor: Record<string, string> = {
  matched: "green",
  linked: "green",
  not_needed: "gray",
  ambiguous: "red",
  unmatched: "red",
};

const Preview = ({ id }: { id: number }) => {
  const { t, money, day, locale } = useAccountingFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const preview = useQuery(saftImportQueryOptions(id));
  const [vatTerm, setVatTerm] = useState<string | null>("bimonthly");
  const [language, setLanguage] = useState<string | null>(locale === "nb" ? "nb" : "en");
  const [month, setMonth] = useState<string | null>(null);
  const [otherCompany, setOtherCompany] = useState(false);
  const [answer, setAnswer] = useState<AccountingSaftImport | null>(null);
  const attention = useQuery(saftImportAttentionQueryOptions(id));
  const refresh = async () => {
    setAnswer(null);
    await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "saft", "import", id] });
  };
  const linkCustomer = useMutation({
    mutationFn: ({ position, customerId }: { position: number; customerId: number }) =>
      linkSaftImportCustomer(id, position, customerId),
    onSuccess: refresh,
  });
  const match = useMutation({ mutationFn: () => matchSaftImport(id), onSuccess: refresh });
  const apply = useMutation({
    mutationFn: () =>
      applySaftImport(id, {
        vatTerm: (vatTerm ?? "bimonthly") as AccountingVatTerm,
        language: language === "nb" ? "nb" : "en",
        fiscalYearStartMonth: Number(month ?? preview.data?.fiscalYearStartMonth ?? 1),
        ...(otherCompany ? { confirmOtherCompany: true } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saftImportApplied") });
      await navigate({ to: ACCOUNTING_HOME_ROUTE_PATH });
    },
    onError: async () => setAnswer((await preview.refetch()).data ?? null),
  });
  if (preview.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, preview.error)}
      </Alert>
    );
  if (!preview.data) return <Text c="dimmed">…</Text>;
  const p = answer ?? preview.data;
  const needsLink = attention.data?.items ?? [];
  const months = Array.from({ length: 12 }, (_, i) => ({ value: String(i + 1), label: t(`month_${i + 1}`) }));
  return (
    <Stack gap="md">
      <Text size="sm">
        {t("saftImportSummary", {
          company: p.companyName,
          number: p.companyOrganisationNumber,
          version: p.auditFileVersion,
          from: day(p.selectionStart),
          to: day(p.selectionEnd),
          start: day(p.booksStart),
        })}
      </Text>
      {p.notes.length > 0 && (
        <Alert color="blue" icon={<IconInfoCircle size={16} />}>
          <List size="sm">
            {p.notes.map((n) => (
              <List.Item key={n}>{n}</List.Item>
            ))}
          </List>
        </Alert>
      )}
      {p.blockers.length > 0 && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("saftImportBlocked")}>
          <List size="sm">
            {p.blockers.map((b) => (
              <List.Item key={b}>{b}</List.Item>
            ))}
          </List>
        </Alert>
      )}
      <Title order={5}>{t("saftImportAccounts")}</Title>
      <Table striped>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("saftImportFileAccount")}</Table.Th>
            <Table.Th>{t("accountNumber")}</Table.Th>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("kind")}</Table.Th>
            <Table.Th>{t("grouping")}</Table.Th>
            <Table.Th>{t("saftImportClosing")}</Table.Th>
            <Table.Th>{t("saftImportProblem")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {p.accounts.map((a) => (
            <Table.Tr key={a.fileAccountId}>
              <Table.Td>{a.fileAccountId}</Table.Td>
              <Table.Td>{a.number ?? t(`saftImportAction_${a.action}`)}</Table.Td>
              <Table.Td>{a.name}</Table.Td>
              <Table.Td>{a.kind ? t(`kind_${a.kind}`) : ""}</Table.Td>
              <Table.Td>{a.groupingCode ?? "NA"}</Table.Td>
              <Table.Td>{money(a.closingBalance)}</Table.Td>
              <Table.Td>{a.problem ?? ""}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {p.addedAccounts.length > 0 && (
        <>
          <Title order={5}>{t("saftImportAdded")}</Title>
          <List size="sm">
            {p.addedAccounts.map((a) => (
              <List.Item key={a.number}>
                {a.number} {a.name} — {a.reason}
              </List.Item>
            ))}
          </List>
        </>
      )}
      <Title order={5}>{t("saftImportRoles")}</Title>
      <List size="sm">
        {p.roles.map((r) => (
          <List.Item key={r.role}>
            {t(`role_${r.role}`)}: {r.accountNumber}
            {r.added ? ` (${t("saftImportFromDefault")})` : ""}
          </List.Item>
        ))}
      </List>
      <Title order={5}>{t("saftImportTaxCodes")}</Title>
      <List size="sm">
        {p.taxCodes.map((c) => (
          <List.Item key={c.fileTaxCode}>
            {c.fileTaxCode} {c.description} → {c.code ?? t("saftImportUnmappedCode")}
          </List.Item>
        ))}
      </List>
      <Title order={5}>{t("saftImportSuppliers", { count: p.suppliers.length })}</Title>
      <List size="sm">
        {p.suppliers.map((s) => (
          <List.Item key={s.fileSupplierId}>
            {s.name} {s.organisationNumber ?? ""} — {money(s.closingBalance)}
            {s.notes.length > 0 ? ` (${s.notes.join(" ")})` : ""}
          </List.Item>
        ))}
      </List>
      <Title order={5}>{t("saftImportCustomers")}</Title>
      <Table striped>
        <Table.Tbody>
          {p.customers.map((c) => (
            <Table.Tr key={c.fileCustomerId}>
              <Table.Td>
                {c.fileCustomerId} {c.name} {c.organisationNumber ?? ""}
              </Table.Td>
              <Table.Td>{money(c.closingBalance)}</Table.Td>
              <Table.Td>
                <Badge color={matchColor[c.match]}>{t(`saftMatch_${c.match}`)}</Badge> {c.customerName ?? ""}{" "}
                {c.customerOrganisationNumber ?? ""}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Group>
        <Button variant="light" size="xs" loading={match.isPending} onClick={() => match.mutate()}>
          {t("saftImportMatchAgain")}
        </Button>
        {p.customersTotal > p.customers.length && (
          <Text size="sm" c="dimmed">
            {t("saftImportShowing", { shown: p.customers.length, total: p.customersTotal })}
          </Text>
        )}
      </Group>
      {needsLink.length > 0 && (
        <Stack gap="xs">
          <Text size="sm">{t("saftImportLinkHint")}</Text>
          {needsLink.map((c) => (
            <CustomerSelect
              key={c.position}
              label={t("saftImportLink", { name: c.name })}
              value={c.customerId ? String(c.customerId) : null}
              onChange={(value) => value && linkCustomer.mutate({ position: c.position, customerId: Number(value) })}
              w={360}
            />
          ))}
          {linkCustomer.error && <Text c="red">{refusalMessage(t, linkCustomer.error)}</Text>}
        </Stack>
      )}
      <Title order={5}>{t("saftImportOpening", { date: day(p.booksStart) })}</Title>
      <Table striped>
        <Table.Tbody>
          {p.opening.lines.map((l, i) => (
            <Table.Tr key={`${l.accountNumber}-${i}`}>
              <Table.Td>{l.accountNumber}</Table.Td>
              <Table.Td>{l.party ?? l.text}</Table.Td>
              <Table.Td>{money(l.amount)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {Number(p.opening.vatMoved) !== 0 && (
        <Text size="sm">
          {t("saftImportVatMoved", {
            amount: money(String(-Number(p.opening.vatMoved))),
            account: p.opening.vatSettlementAccount ?? "",
          })}
        </Text>
      )}
      {p.companyMismatch && (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("saftImportOtherCompany")}>
          <Stack gap="xs">
            <Text size="sm">
              {t("saftImportOtherCompanyHint", {
                file: p.companyOrganisationNumber,
                seller: p.sellerOrganisationNumber ?? "",
              })}
            </Text>
            <Checkbox
              label={t("saftImportConfirmOtherCompany")}
              checked={otherCompany}
              onChange={(event) => setOtherCompany(event.currentTarget.checked)}
            />
          </Stack>
        </Alert>
      )}
      <Select
        label={t("fiscalYearStartMonth")}
        description={p.needsFiscalYearStartMonth ? t("saftImportConfirmMonth") : undefined}
        data={months}
        value={month ?? String(p.fiscalYearStartMonth)}
        onChange={setMonth}
        allowDeselect={false}
        w={260}
      />
      <Select
        label={t("vatTerm")}
        data={vatTerms.map((term) => ({ value: term, label: t(`vatTerm_${term}`) }))}
        value={vatTerm}
        onChange={setVatTerm}
        allowDeselect={false}
        w={260}
      />
      <Select
        label={t("chartLanguage")}
        data={[
          { value: "nb", label: t("language_nb") },
          { value: "en", label: t("language_en") },
        ]}
        value={language}
        onChange={setLanguage}
        allowDeselect={false}
        w={260}
      />
      {apply.error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {refusalMessage(t, apply.error)}
        </Alert>
      )}
      <Text size="sm" c="dimmed">
        {t("saftImportFinal")}
      </Text>
      <Button
        loading={apply.isPending}
        disabled={p.state !== "preview" || (p.companyMismatch && !otherCompany)}
        onClick={() => apply.mutate()}
        data-assist="accounting.saft.apply"
      >
        {t("saftImportApply")}
      </Button>
    </Stack>
  );
};
