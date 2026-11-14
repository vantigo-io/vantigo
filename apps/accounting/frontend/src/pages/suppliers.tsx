import {
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload, IconPlus, IconSearch } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingBrregEntity,
  type AccountingSupplier,
  type AccountingSupplierInput,
  createSupplier,
  deleteSupplier,
  exportSupplier,
  lookupBrreg,
  supplierInvoicesQueryOptions,
  supplierQueryOptions,
  suppliersQueryOptions,
  updateSupplier,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { SUPPLIERS_ROUTE_PATH, supplierInvoicePath, supplierPath } from "../lib/routes";

/**
 * Suppliers (A17): the module's own parties, numbered as they are made,
 * looked up in Brønnøysundregisteret, never deleted once the books name them.
 * Keeping them needs accounting:post.
 */
export const SuppliersPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navSuppliers")} description={t("suppliersDescription")} />
          <SupplierList canPost={meta.canPost} />
        </Stack>
      )}
    </BooksGate>
  );
};

const SupplierList = ({ canPost }: { canPost: boolean }) => {
  const { t, money } = useAccountingFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const [adding, setAdding] = useState(false);
  const list = useQuery(suppliersQueryOptions({ q: debounced.trim() || undefined }));
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <TextInput
          leftSection={<IconSearch size={16} />}
          placeholder={t("supplierSearch")}
          aria-label={t("supplierSearch")}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
        />
        {canPost && (
          <Button leftSection={<IconPlus size={16} />} onClick={() => setAdding(true)}>
            {t("addSupplier")}
          </Button>
        )}
      </Group>
      {list.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {list.error.message}
        </Alert>
      ) : list.isPending ? (
        <ContentSkeleton rows={4} />
      ) : list.data.items.length === 0 ? (
        <EmptyState title={t("noSuppliers")} description={t("noSuppliersHint")} />
      ) : (
        <Table striped highlightOnHover data-testid="suppliers">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("supplierNumber")}</Table.Th>
              <Table.Th>{t("name")}</Table.Th>
              <Table.Th>{t("organisationNumber")}</Table.Th>
              <Table.Th ta="right">{t("balance")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {list.data.items.map((s) => (
              <Table.Tr key={s.id}>
                <Table.Td>{s.number}</Table.Td>
                <Table.Td>
                  <Anchor component={Link} to={supplierPath(s.id)}>
                    {s.name}
                  </Anchor>
                </Table.Td>
                <Table.Td>{s.organisationNumber}</Table.Td>
                <Table.Td ta="right">{money(s.balance)}</Table.Td>
                <Table.Td>
                  {!s.active && (
                    <Badge variant="light" color="gray">
                      {t("inactive")}
                    </Badge>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {adding && <SupplierFormModal onClose={() => setAdding(false)} />}
    </Stack>
  );
};

/** A supplier's page: its record, its invoices, the export and the change. */
export const SupplierPage = () => {
  const { supplierId } = useParams({ strict: false }) as { supplierId: string };
  return (
    <BooksGate>
      {(meta) => <SupplierView id={Number(supplierId)} canPost={meta.canPost} canExport={meta.canExport} />}
    </BooksGate>
  );
};

const SupplierView = ({ id, canPost, canExport }: { id: number; canPost: boolean; canExport: boolean }) => {
  const { t, money, day } = useAccountingFormat();
  const supplier = useQuery(supplierQueryOptions(id));
  const invoices = useQuery(supplierInvoicesQueryOptions({ supplierId: id }));
  const [editing, setEditing] = useState(false);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: () => deleteSupplier(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY, "suppliers"] });
      notifications.show({ color: "green", message: t("deleted") });
      await navigate({ to: SUPPLIERS_ROUTE_PATH });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const download = useMutation({
    mutationFn: () => exportSupplier(id),
    onSuccess: (data) => {
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `supplier-${data.supplier.number}.json`;
      a.click();
      URL.revokeObjectURL(url);
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  if (supplier.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {supplier.error.message}
      </Alert>
    );
  if (supplier.isPending) return <ContentSkeleton rows={6} />;
  const s = supplier.data;
  return (
    <Stack gap="lg">
      <PageHeader
        title={`${s.number} ${s.name}`}
        description={[s.organisationNumber, s.addressLine, [s.postalCode, s.city].filter(Boolean).join(" ")]
          .filter(Boolean)
          .join(" · ")}
      />
      <Group>
        {canPost && <Button onClick={() => setEditing(true)}>{t("changeSupplier")}</Button>}
        {canExport && (
          <Button
            variant="default"
            leftSection={<IconDownload size={16} />}
            loading={download.isPending}
            onClick={() => download.mutate()}
          >
            {t("exportSupplier")}
          </Button>
        )}
        {canPost && !s.hasPostings && (
          <Button variant="subtle" color="red" loading={remove.isPending} onClick={() => remove.mutate()}>
            {t("delete")}
          </Button>
        )}
      </Group>
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        <Text size="sm">
          {t("balance")}: {money(s.balance)}
        </Text>
        <Text size="sm">
          {t("bankAccount")}: {s.bankAccount ?? s.iban ?? "—"}
        </Text>
        <Text size="sm">
          {t("paymentTerms")}: {t("paymentTermsDays", { days: s.paymentTermsDays })}
        </Text>
      </SimpleGrid>
      {s.isPerson && (
        <Alert color="blue" title={t("supplierIsPerson")}>
          {s.retainedUntil ? t("supplierRetainedUntil", { date: day(s.retainedUntil) }) : t("supplierRetainedHint")}
        </Alert>
      )}
      <Text fw={600}>{t("navSupplierInvoices")}</Text>
      {invoices.data && invoices.data.items.length > 0 ? (
        <Table striped data-testid="supplier-invoices-of-supplier">
          <Table.Tbody>
            {invoices.data.items.map((inv) => (
              <Table.Tr key={inv.id}>
                <Table.Td>
                  <Anchor component={Link} to={supplierInvoicePath(inv.id)}>
                    {inv.invoiceNumber}
                  </Anchor>
                </Table.Td>
                <Table.Td>{day(inv.invoiceDate)}</Table.Td>
                <Table.Td>{t(`supplierInvoiceState_${inv.state}`)}</Table.Td>
                <Table.Td ta="right">{money(inv.totalNok)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      ) : (
        <Text size="sm" c="dimmed">
          {t("noSupplierInvoices")}
        </Text>
      )}
      {editing && <SupplierFormModal supplier={s} onClose={() => setEditing(false)} />}
    </Stack>
  );
};

/** The fields of a supplier's form, as text. */
interface SupplierFields {
  name: string;
  organisationNumber: string;
  country: string;
  addressLine: string;
  postalCode: string;
  city: string;
  email: string;
  bankAccount: string;
  iban: string;
  bic: string;
  paymentTermsDays: number | string;
  currency: string;
  defaultAccountId: string | null;
  defaultTaxCodeId: string | null;
  isPerson: boolean;
  active: boolean;
}

const fieldsOf = (s?: AccountingSupplier): SupplierFields => ({
  name: s?.name ?? "",
  organisationNumber: s?.organisationNumber ?? "",
  country: s?.country ?? "NO",
  addressLine: s?.addressLine ?? "",
  postalCode: s?.postalCode ?? "",
  city: s?.city ?? "",
  email: s?.email ?? "",
  bankAccount: s?.bankAccount ?? "",
  iban: s?.iban ?? "",
  bic: s?.bic ?? "",
  paymentTermsDays: s?.paymentTermsDays ?? 14,
  currency: s?.currency ?? "NOK",
  defaultAccountId: s?.defaultAccountId ? String(s.defaultAccountId) : null,
  defaultTaxCodeId: s?.defaultTaxCodeId ? String(s.defaultTaxCodeId) : null,
  isPerson: s?.isPerson ?? false,
  active: s?.active ?? true,
});

const blank = (v: string) => (v.trim() === "" ? null : v.trim());

/** A supplier's form, new or changed, with the Brønnøysund lookup. */
export const SupplierFormModal = ({
  supplier,
  onClose,
  onSaved,
}: {
  supplier?: AccountingSupplier;
  onClose: () => void;
  onSaved?: (s: AccountingSupplier) => void;
}) => {
  const { t } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [f, setF] = useState<SupplierFields>(fieldsOf(supplier));
  const set = <K extends keyof SupplierFields>(key: K, value: SupplierFields[K]) =>
    setF((x) => ({ ...x, [key]: value }));
  const save = useMutation({
    mutationFn: () => {
      const body: AccountingSupplierInput = {
        name: f.name.trim(),
        organisationNumber: blank(f.organisationNumber),
        country: blank(f.country),
        addressLine: blank(f.addressLine),
        postalCode: blank(f.postalCode),
        city: blank(f.city),
        email: blank(f.email),
        bankAccount: blank(f.bankAccount),
        iban: blank(f.iban),
        bic: blank(f.bic),
        paymentTermsDays: Number(f.paymentTermsDays),
        currency: blank(f.currency),
        defaultAccountId: f.defaultAccountId ? Number(f.defaultAccountId) : null,
        defaultTaxCodeId: f.defaultTaxCodeId ? Number(f.defaultTaxCodeId) : null,
        isPerson: f.isPerson,
        ...(supplier ? { active: f.active, revision: supplier.revision } : {}),
      };
      return supplier ? updateSupplier(supplier.id, body) : createSupplier(body);
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
      onSaved?.(saved);
      onClose();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const fill = (e: AccountingBrregEntity) =>
    setF((x) => ({
      ...x,
      name: e.name,
      organisationNumber: e.organisationNumber,
      addressLine: e.addressLine ?? x.addressLine,
      postalCode: e.postalCode ?? x.postalCode,
      city: e.city ?? x.city,
      country: e.country ?? x.country,
    }));
  return (
    <Modal opened onClose={onClose} title={supplier ? t("changeSupplier") : t("addSupplier")} size="lg">
      <Stack>
        <BrregLookup onPick={fill} />
        <TextInput
          label={t("name")}
          required
          maxLength={200}
          value={f.name}
          onChange={(e) => set("name", e.currentTarget.value)}
        />
        <SimpleGrid cols={2}>
          <TextInput
            label={t("organisationNumber")}
            value={f.organisationNumber}
            onChange={(e) => set("organisationNumber", e.currentTarget.value)}
          />
          <TextInput
            label={t("country")}
            maxLength={2}
            value={f.country}
            onChange={(e) => set("country", e.currentTarget.value.toUpperCase())}
          />
        </SimpleGrid>
        <TextInput
          label={t("address")}
          value={f.addressLine}
          onChange={(e) => set("addressLine", e.currentTarget.value)}
        />
        <SimpleGrid cols={2}>
          <TextInput
            label={t("postalCode")}
            value={f.postalCode}
            onChange={(e) => set("postalCode", e.currentTarget.value)}
          />
          <TextInput label={t("city")} value={f.city} onChange={(e) => set("city", e.currentTarget.value)} />
        </SimpleGrid>
        <TextInput label={t("email")} value={f.email} onChange={(e) => set("email", e.currentTarget.value)} />
        <SimpleGrid cols={3}>
          <TextInput
            label={t("bankAccount")}
            value={f.bankAccount}
            onChange={(e) => set("bankAccount", e.currentTarget.value)}
          />
          <TextInput label={t("iban")} value={f.iban} onChange={(e) => set("iban", e.currentTarget.value)} />
          <TextInput label={t("bic")} value={f.bic} onChange={(e) => set("bic", e.currentTarget.value)} />
        </SimpleGrid>
        <SimpleGrid cols={2}>
          <NumberInput
            label={t("paymentTerms")}
            min={0}
            max={365}
            allowDecimal={false}
            value={f.paymentTermsDays}
            onChange={(v) => set("paymentTermsDays", v)}
          />
          <TextInput
            label={t("currency")}
            maxLength={3}
            value={f.currency}
            onChange={(e) => set("currency", e.currentTarget.value.toUpperCase())}
          />
        </SimpleGrid>
        <SimpleGrid cols={2}>
          <AccountSelect
            label={t("defaultAccount")}
            accountFilter={(a) => a.kind === "expense" || a.kind === "asset"}
            value={f.defaultAccountId}
            onChange={(v) => set("defaultAccountId", v)}
          />
          <TaxCodeSelect
            label={t("defaultTaxCode")}
            value={f.defaultTaxCodeId}
            onChange={(v) => set("defaultTaxCodeId", v)}
          />
        </SimpleGrid>
        <Checkbox
          label={t("supplierIsPersonLabel")}
          description={t("supplierRetainedHint")}
          checked={f.isPerson}
          onChange={(e) => set("isPerson", e.currentTarget.checked)}
        />
        {supplier && (
          <Checkbox label={t("active")} checked={f.active} onChange={(e) => set("active", e.currentTarget.checked)} />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} disabled={!f.name.trim()} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** The Brønnøysund lookup: nine digits read the entity, a name searches. */
const BrregLookup = ({ onPick }: { onPick: (e: AccountingBrregEntity) => void }) => {
  const { t } = useAccountingFormat();
  const [query, setQuery] = useState("");
  const [debounced] = useDebouncedValue(query.trim(), 350);
  const digits = debounced.replace(/[\s.]/g, "");
  const byNumber = /^\d{9}$/.test(digits);
  const results = useQuery({
    queryKey: [ACCOUNTING_QUERY_KEY, "brreg", debounced],
    queryFn: ({ signal }) => lookupBrreg(byNumber ? { organisationNumber: digits } : { q: debounced }, signal),
    enabled: byNumber || debounced.length >= 2,
    retry: false,
    staleTime: 60_000,
  });
  const data = (results.data?.items ?? []).map((e) => ({
    value: e.organisationNumber,
    label: `${e.name} (${e.organisationNumber})`,
  }));
  return (
    <Select
      label={t("brregLookup")}
      description={results.error ? refusalMessage(t, results.error) : t("brregLookupHint")}
      searchable
      clearable
      data={data}
      searchValue={query}
      onSearchChange={setQuery}
      filter={({ options }) => options}
      nothingFoundMessage={debounced.length >= 2 ? t("brregNothing") : undefined}
      onChange={async (orgnr) => {
        if (!orgnr) return;
        const picked = results.data?.items.find((e) => e.organisationNumber === orgnr);
        // A name search answers no address: the entity itself does.
        const full = picked?.addressLine ? picked : (await lookupBrreg({ organisationNumber: orgnr })).items[0];
        if (full ?? picked) onPick((full ?? picked) as AccountingBrregEntity);
      }}
    />
  );
};
