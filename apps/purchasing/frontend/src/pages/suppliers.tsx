import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  FileInput,
  Group,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconAlertTriangle,
  IconEdit,
  IconExternalLink,
  IconPlus,
  IconSearch,
  IconTrash,
  IconUpload,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  createSupplierItem,
  deleteSupplierItem,
  importSupplierItems,
  type PurchasingImportResult,
  type PurchasingPackaging,
  type PurchasingSupplier,
  type PurchasingSupplierItem,
  type PurchasingVariantOption,
  purchasingMetaQueryOptions,
  purchasingSupplierQueryOptions,
  purchasingSuppliersQueryOptions,
  searchPurchasingVariants,
  supplierItemsQueryOptions,
  updateSupplierItem,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY } from "../api/request";
import { ConfirmDialog, VariantPicker } from "../components/common";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";
import {
  accountingSupplierLinkOptions,
  newPurchaseOrderLinkOptions,
  productLinkOptions,
  supplierLinkOptions,
} from "../lib/routes";

/** What the host's supplier form is handed: a supplier to change, or none for a new one. */
export interface SupplierFormSlotProps {
  supplierId?: number;
  onClose: () => void;
  onSaved: (supplierId: number) => void;
}

/**
 * The register's supplier form, which the host composes from Accounting's
 * package (P13, A12): Purchasing writes no supplier itself and imports no
 * other module's screens.
 */
export type RenderSupplierForm = (props: SupplierFormSlotProps) => ReactNode;

/**
 * Suppliers (P13): the register's suppliers, searched by name, number or
 * organisation number, inactive ones too, each with its supplier items and
 * open orders counted. *New supplier* is offered when the host hands a
 * supplier form and the caller may write the register (meta's
 * canManageSuppliers: accounting:suppliers-manage, or accounting:post with
 * accounting:read).
 */
export const SuppliersPage = ({ renderSupplierForm }: { renderSupplierForm?: RenderSupplierForm }) => {
  const { t } = usePurchasingFormat();
  const navigate = useNavigate();
  const meta = useQuery(purchasingMetaQueryOptions());
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const [adding, setAdding] = useState(false);
  const list = useQuery(purchasingSuppliersQueryOptions(debounced.trim() || undefined));
  const canAdd = meta.data?.canManageSuppliers === true && renderSupplierForm !== undefined;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("suppliersTitle")}
        description={t("suppliersDescription")}
        actions={
          canAdd && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setAdding(true)}>
              {t("newSupplier")}
            </Button>
          )
        }
      />
      <PageSection>
        <Stack gap="md">
          <TextInput
            leftSection={<IconSearch size={16} />}
            label={t("search")}
            placeholder={t("searchSuppliers")}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            w={320}
          />
          {list.isError ? (
            <Alert color="red" title={t("couldNotLoad")}>
              {refusalMessage(list.error, t)}
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
                  <Table.Th ta="right">{t("supplierItemCount")}</Table.Th>
                  <Table.Th ta="right">{t("openOrderCount")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.data.items.map((s) => (
                  <Table.Tr key={s.id}>
                    <Table.Td>{s.number}</Table.Td>
                    <Table.Td>
                      <Anchor renderRoot={(props) => <Link {...supplierLinkOptions(s.id)} {...props} />}>
                        {s.name}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>{s.organisationNumber}</Table.Td>
                    <Table.Td ta="right">{s.supplierItemCount}</Table.Td>
                    <Table.Td ta="right">{s.openOrderCount}</Table.Td>
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
        </Stack>
      </PageSection>
      {adding &&
        renderSupplierForm?.({
          onClose: () => setAdding(false),
          onSaved: (id) => {
            setAdding(false);
            void navigate(supplierLinkOptions(id));
          },
        })}
    </Stack>
  );
};

/**
 * A supplier's page (P13): the register's entry, read-only, with a link to
 * Accounting's page for a holder of accounting:read, and *Edit supplier*
 * through the host's form when the caller may write the register and read it
 * (the change names the register's revision, which Accounting's read gives);
 * *New purchase order* with purchasing:orders-manage; and its supplier items,
 * written and imported with purchasing:supplier-items-manage.
 */
export const SupplierPage = ({
  supplierId,
  access = noPurchasingAccess,
  renderSupplierForm,
}: {
  supplierId: number;
  access?: PurchasingAccess;
  renderSupplierForm?: RenderSupplierForm;
}) => {
  const { t } = usePurchasingFormat();
  const meta = useQuery(purchasingMetaQueryOptions());
  const supplier = useQuery(purchasingSupplierQueryOptions(supplierId));
  const [editing, setEditing] = useState(false);
  const queryClient = useQueryClient();
  if (supplier.isPending) return <ContentSkeleton rows={6} />;
  if (supplier.isError)
    return (
      <Alert color="red" title={t("couldNotLoad")}>
        {refusalMessage(supplier.error, t)}
      </Alert>
    );
  const s = supplier.data;
  const canOpenAccounting = meta.data?.canOpenAccountingSuppliers === true;
  const canEdit =
    renderSupplierForm !== undefined && meta.data?.canManageSuppliers === true && canOpenAccounting && !s.anonymised;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("supplierNumberName", { number: s.number, name: s.name })}
        description={s.organisationNumber}
        actions={
          <Group gap="xs">
            {access.canManageOrders && s.active && (
              <Button
                leftSection={<IconPlus size={16} />}
                renderRoot={(props) => <Link {...newPurchaseOrderLinkOptions(s.id)} {...props} />}
              >
                {t("newOrderForSupplier")}
              </Button>
            )}
            {canEdit && (
              <Button variant="default" onClick={() => setEditing(true)}>
                {t("editSupplier")}
              </Button>
            )}
            {canOpenAccounting && (
              <Button
                variant="default"
                leftSection={<IconExternalLink size={16} />}
                renderRoot={(props) => <Link {...accountingSupplierLinkOptions(s.id)} {...props} />}
              >
                {t("openInAccounting")}
              </Button>
            )}
          </Group>
        }
      />
      <RegisterEntry supplier={s} />
      <SupplierItems supplier={s} canManage={access.canManageSupplierItems} />
      {editing &&
        renderSupplierForm?.({
          supplierId: s.id,
          onClose: () => setEditing(false),
          onSaved: () => {
            setEditing(false);
            void queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY, "suppliers"] });
          },
        })}
    </Stack>
  );
};

const RegisterEntry = ({ supplier: s }: { supplier: PurchasingSupplier }) => {
  const { t } = usePurchasingFormat();
  const address = [s.addressLine, [s.postalCode, s.city].filter(Boolean).join(" "), s.country]
    .filter(Boolean)
    .join(", ");
  return (
    <PageSection>
      <Stack gap="sm">
        <Group gap="xs">
          <Title order={4}>{t("registerEntry")}</Title>
          {!s.active && (
            <Badge variant="light" color="gray">
              {t("inactive")}
            </Badge>
          )}
          {s.isPerson && (
            <Badge variant="light" color="blue">
              {t("person")}
            </Badge>
          )}
          {s.anonymised && (
            <Badge variant="light" color="gray">
              {t("anonymised")}
            </Badge>
          )}
        </Group>
        <Text size="xs" c="dimmed">
          {t("registerEntryHint")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} data-testid="register-entry">
          <Entry label={t("organisationNumber")} value={s.organisationNumber} />
          <Entry label={t("address")} value={address} />
          <Entry label={t("email")} value={s.email} />
          <Entry label={t("currency")} value={s.currency} />
          <Entry label={t("paymentTerms")} value={t("paymentTermsDays", { count: s.paymentTermsDays })} />
          <Entry label={t("openOrderCount")} value={String(s.openOrderCount)} />
        </SimpleGrid>
      </Stack>
    </PageSection>
  );
};

const Entry = ({ label, value }: { label: string; value: string }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{value || "—"}</Text>
  </Stack>
);

const SupplierItems = ({ supplier, canManage }: { supplier: PurchasingSupplier; canManage: boolean }) => {
  const { t, unitPrice, quantity, date } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const items = useQuery(
    supplierItemsQueryOptions({ supplierId: supplier.id, q: debounced.trim() || undefined, pageSize: 200 }),
  );
  const [form, setForm] = useState<PurchasingSupplierItem | "new" | null>(null);
  const [deleting, setDeleting] = useState<PurchasingSupplierItem | null>(null);
  const [importing, setImporting] = useState(false);
  const remove = useMutation({
    mutationFn: (id: number) => deleteSupplierItem(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      setDeleting(null);
      notifications.show({ color: "green", message: t("deleted") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(error, t) }),
  });
  return (
    <PageSection>
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t("supplierItems")}</Title>
          {canManage && (
            <Group gap="xs">
              <Button variant="default" leftSection={<IconUpload size={16} />} onClick={() => setImporting(true)}>
                {t("importPriceList")}
              </Button>
              <Button leftSection={<IconPlus size={16} />} onClick={() => setForm("new")}>
                {t("newSupplierItem")}
              </Button>
            </Group>
          )}
        </Group>
        <TextInput
          leftSection={<IconSearch size={16} />}
          aria-label={t("search")}
          placeholder={t("searchVariants")}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          w={320}
        />
        {items.isError ? (
          <Alert color="red" title={t("couldNotLoad")}>
            {refusalMessage(items.error, t)}
          </Alert>
        ) : items.isPending ? (
          <ContentSkeleton rows={3} />
        ) : items.data.items.length === 0 ? (
          <EmptyState title={t("noSupplierItems")} description={canManage ? t("noSupplierItemsHint") : undefined} />
        ) : (
          <Table.ScrollContainer minWidth={900}>
            <Table striped data-testid="supplier-items">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("sku")}</Table.Th>
                  <Table.Th>{t("product")}</Table.Th>
                  <Table.Th>{t("supplierItemNumber")}</Table.Th>
                  <Table.Th>{t("packaging")}</Table.Th>
                  <Table.Th ta="right">{t("price")}</Table.Th>
                  <Table.Th>{t("leadTime")}</Table.Th>
                  <Table.Th ta="right">{t("minimumQuantity")}</Table.Th>
                  <Table.Th ta="right">{t("multiple")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {items.data.items.map((item) => (
                  <Table.Tr key={item.id}>
                    <Table.Td>{item.sku}</Table.Td>
                    <Table.Td>
                      <Group gap={6} wrap="nowrap">
                        <Anchor
                          renderRoot={(props) => <Link {...productLinkOptions(item.productId)} {...props} />}
                          c="inherit"
                        >
                          {item.productName}
                        </Anchor>
                        {item.preferred && (
                          <Badge size="xs" variant="light">
                            {t("preferred")}
                          </Badge>
                        )}
                      </Group>
                      {item.supplierItemName && (
                        <Text size="xs" c="dimmed">
                          {item.supplierItemName}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>{item.supplierItemNumber ?? ""}</Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        {item.packagingName ?? item.unit}
                        {item.packagingMissing && (
                          <Tooltip label={t("packagingMissing")}>
                            <IconAlertTriangle size={14} color="orange" />
                          </Tooltip>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td ta="right">
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        {item.price !== null && item.price !== undefined ? unitPrice(item.price, item.currency) : "—"}
                        {item.currencyDiffers && (
                          <Tooltip label={t("currencyDiffers", { currency: item.currency })}>
                            <IconAlertTriangle size={14} color="orange" data-testid="currency-differs" />
                          </Tooltip>
                        )}
                      </Group>
                      {item.priceSetOn && (
                        <Text size="xs" c="dimmed">
                          {t("priceSetOn", { date: date(item.priceSetOn) })}
                          {item.priceSetBy ? ` ${t(`priceSetBy.${item.priceSetBy}`)}` : ""}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      {item.leadTimeDays !== null && item.leadTimeDays !== undefined
                        ? t("leadTimeValue", { count: item.leadTimeDays })
                        : ""}
                    </Table.Td>
                    <Table.Td ta="right">{item.minimumQuantity ? quantity(item.minimumQuantity) : ""}</Table.Td>
                    <Table.Td ta="right">{item.multiple ? quantity(item.multiple) : ""}</Table.Td>
                    <Table.Td>
                      {canManage && (
                        <Group gap={2} wrap="nowrap">
                          <ActionIcon
                            variant="subtle"
                            aria-label={`${t("editSupplierItem")} ${item.sku}`}
                            onClick={() => setForm(item)}
                          >
                            <IconEdit size={16} />
                          </ActionIcon>
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            aria-label={`${t("deleteSupplierItem")} ${item.sku}`}
                            onClick={() => setDeleting(item)}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Group>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      {form !== null && (
        <SupplierItemForm supplier={supplier} item={form === "new" ? undefined : form} onClose={() => setForm(null)} />
      )}
      {deleting && (
        <ConfirmDialog
          title={t("deleteSupplierItem")}
          body={t("deleteSupplierItemConfirm")}
          confirm={t("delete")}
          color="red"
          loading={remove.isPending}
          onClose={() => setDeleting(null)}
          onConfirm={() => remove.mutate(deleting.id)}
        />
      )}
      {importing && <ImportDialog supplier={supplier} onClose={() => setImporting(false)} />}
    </PageSection>
  );
};

/** A blank text as the contract's null. */
const blank = (v: string) => (v.trim() === "" ? null : v.trim().replace(",", "."));

/**
 * A supplier item's form (P11): new for a product picked by SKU, name or
 * barcode, or changed — supplier and product stay. The packaging is the unit
 * or one of the product's packagings.
 */
const SupplierItemForm = ({
  supplier,
  item,
  onClose,
}: {
  supplier: PurchasingSupplier;
  item?: PurchasingSupplierItem;
  onClose: () => void;
}) => {
  const { t, quantity } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const [variant, setVariant] = useState<PurchasingVariantOption | null>(null);
  // A change reads the product's packagings through the variant search, by its SKU.
  const current = useQuery({
    queryKey: [PURCHASING_QUERY_KEY, "variants", item?.sku ?? "", supplier.id],
    queryFn: ({ signal }) => searchPurchasingVariants(item?.sku ?? "", supplier.id, signal),
    enabled: item !== undefined,
  });
  const packagings: PurchasingPackaging[] =
    variant?.packagings ?? current.data?.items.find((v) => v.variantId === item?.variantId)?.packagings ?? [];
  const unit = variant?.unit ?? item?.unit ?? "";
  const [f, setF] = useState({
    supplierItemNumber: item?.supplierItemNumber ?? "",
    supplierItemName: item?.supplierItemName ?? "",
    packagingId: item?.packagingId ?? null,
    price: item?.price ?? "",
    leadTimeDays: item?.leadTimeDays ?? ("" as number | string),
    minimumQuantity: item?.minimumQuantity ?? "",
    multiple: item?.multiple ?? "",
    preferred: item?.preferred ?? false,
  });
  const set = <K extends keyof typeof f>(key: K, value: (typeof f)[K]) => setF((x) => ({ ...x, [key]: value }));
  const save = useMutation({
    mutationFn: () => {
      const fields = {
        supplierItemNumber: f.supplierItemNumber.trim() || null,
        supplierItemName: f.supplierItemName.trim() || null,
        packagingId: f.packagingId,
        price: blank(f.price),
        leadTimeDays: f.leadTimeDays === "" ? null : Number(f.leadTimeDays),
        minimumQuantity: blank(f.minimumQuantity),
        multiple: blank(f.multiple),
        preferred: f.preferred,
      };
      return item
        ? updateSupplierItem(item.id, { ...fields, revision: item.revision })
        : createSupplierItem({
            ...fields,
            supplierId: supplier.id,
            variantId: (variant as PurchasingVariantOption).variantId,
          });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={item ? t("editSupplierItem") : t("newSupplierItem")} size="lg">
      <Stack>
        {item ? (
          <Text fw={500}>{`${item.sku} · ${item.productName}`}</Text>
        ) : variant ? (
          <Group justify="space-between">
            <Text fw={500}>{`${variant.sku} · ${variant.name}`}</Text>
            <Button size="compact-xs" variant="subtle" onClick={() => setVariant(null)}>
              {t("remove")}
            </Button>
          </Group>
        ) : (
          <VariantPicker label={t("product")} size="sm" onPick={setVariant} />
        )}
        <SimpleGrid cols={2}>
          <TextInput
            label={t("supplierItemNumber")}
            maxLength={60}
            value={f.supplierItemNumber}
            onChange={(e) => set("supplierItemNumber", e.currentTarget.value)}
          />
          <TextInput
            label={t("supplierItemName")}
            maxLength={255}
            value={f.supplierItemName}
            onChange={(e) => set("supplierItemName", e.currentTarget.value)}
          />
          <Select
            label={t("packaging")}
            data={[
              { value: "", label: t("packagingUnit", { unit }) },
              ...packagings.map((p) => ({
                value: String(p.id),
                label: t("packagingOption", { name: p.name, quantity: quantity(p.quantity), unit }),
              })),
              ...(f.packagingId !== null && !packagings.some((p) => p.id === f.packagingId)
                ? [{ value: String(f.packagingId), label: item?.packagingName ?? String(f.packagingId) }]
                : []),
            ]}
            value={f.packagingId === null ? "" : String(f.packagingId)}
            onChange={(v) => set("packagingId", v ? Number(v) : null)}
            allowDeselect={false}
          />
          <TextInput
            label={`${t("price")} (${supplier.currency})`}
            inputMode="decimal"
            value={f.price}
            onChange={(e) => set("price", e.currentTarget.value)}
          />
          <NumberInput
            label={t("leadTimeDays")}
            min={0}
            max={3650}
            allowDecimal={false}
            value={f.leadTimeDays}
            onChange={(v) => set("leadTimeDays", v)}
          />
          <TextInput
            label={t("minimumQuantity")}
            inputMode="decimal"
            value={f.minimumQuantity}
            onChange={(e) => set("minimumQuantity", e.currentTarget.value)}
          />
          <TextInput
            label={t("multiple")}
            inputMode="decimal"
            value={f.multiple}
            onChange={(e) => set("multiple", e.currentTarget.value)}
          />
        </SimpleGrid>
        <Checkbox
          label={t("preferred")}
          description={t("preferredHint")}
          checked={f.preferred}
          onChange={(e) => set("preferred", e.currentTarget.checked)}
        />
        {save.isError && (
          <Alert color="red" title={t("couldNotSave")}>
            {refusalMessage(save.error, t)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} disabled={!item && !variant} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * Import price list (P14): the file is checked first — a dry run that says
 * each row's outcome — and imported only when every row is right, every row
 * or none.
 */
const ImportDialog = ({ supplier, onClose }: { supplier: PurchasingSupplier; onClose: () => void }) => {
  const { t } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [checked, setChecked] = useState<PurchasingImportResult | null>(null);
  const check = useMutation({
    mutationFn: async () => importSupplierItems(supplier.id, await (file as File).text(), false),
    onSuccess: setChecked,
  });
  const commit = useMutation({
    mutationFn: async () => importSupplierItems(supplier.id, await (file as File).text(), true),
    onSuccess: async (result) => {
      if (!result.committed) return setChecked(result);
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({
        color: "green",
        message: t("importDone", { created: result.created, updated: result.updated }),
      });
      onClose();
    },
  });
  const error = check.error ?? commit.error;
  return (
    <Modal opened onClose={onClose} title={t("importTitle")} size="xl">
      <Stack>
        <Text size="sm">{t("importHint")}</Text>
        <FileInput
          label={t("importFile")}
          accept=".csv,text/csv"
          value={file}
          onChange={(next) => {
            setFile(next);
            setChecked(null);
          }}
          clearable
        />
        {error && (
          <Alert color="red" title={t("couldNotSave")}>
            {refusalMessage(error, t)}
          </Alert>
        )}
        {checked && (
          <Stack gap="xs" data-testid="import-result">
            {checked.ok ? (
              <Alert color="green" variant="light">
                {t("importSummary", { created: checked.created, updated: checked.updated })}
              </Alert>
            ) : (
              <Alert color="orange" variant="light">
                {t("importNotOk")}
              </Alert>
            )}
            <Table.ScrollContainer minWidth={500} mah={320}>
              <Table striped>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("importRow")}</Table.Th>
                    <Table.Th>{t("importCode")}</Table.Th>
                    <Table.Th>{t("importOutcome")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {checked.rows.map((row) => (
                    <Table.Tr key={row.row}>
                      <Table.Td>{row.row}</Table.Td>
                      <Table.Td>{row.code}</Table.Td>
                      <Table.Td c={row.outcome === "created" || row.outcome === "updated" ? undefined : "red"}>
                        {t(`importOutcome.${row.outcome}`)}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Stack>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="default" disabled={!file} loading={check.isPending} onClick={() => check.mutate()}>
            {t("importCheck")}
          </Button>
          <Button disabled={!checked?.ok} loading={commit.isPending} onClick={() => commit.mutate()}>
            {t("importCommit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
