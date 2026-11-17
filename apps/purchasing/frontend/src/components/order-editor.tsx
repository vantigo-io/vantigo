import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  createPurchaseOrder,
  type PurchaseOrder,
  purchasingBuyersQueryOptions,
  purchasingMetaQueryOptions,
  purchasingSitesQueryOptions,
  updatePurchaseOrder,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY, refusedLines } from "../api/request";
import type { PurchasingAccess } from "../lib/access";
import { markStaleOnConflict } from "../lib/conflict";
import { lineRefusalWords } from "../lib/errors";
import { today, usePurchasingFormat } from "../lib/format";
import { type EditorLine, fromSavedLine, fromVariant, lineInput, lineWarnings, newTextLine } from "../lib/lines";
import { purchaseOrderLinkOptions } from "../lib/routes";
import { SaveError, SupplierPicker, VariantPicker } from "./common";

/** A date picked in the calendar as the contract writes one. */
const dayOf = (value: string | Date | null): string | null => {
  if (value === null) return null;
  if (typeof value === "string") return value.slice(0, 10);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}`;
};

interface Header {
  supplierId: number | null;
  siteId: string | null;
  currency: string;
  language: "nb" | "en" | null;
  orderDate: string;
  expectedOn: string | null;
  buyerId: string | null;
  supplierContact: string;
  note: string;
  internalNote: string;
}

const headerOf = (order: PurchaseOrder | undefined, supplierId: number | undefined, userId?: string): Header => ({
  supplierId: order?.supplierId ?? supplierId ?? null,
  siteId: order ? String(order.siteId) : null,
  currency: order?.currency ?? "",
  language: order?.language ?? null,
  orderDate: order?.orderDate ?? today(),
  expectedOn: order?.expectedOn ?? null,
  buyerId: order ? (order.buyer?.id ?? null) : (userId ?? null),
  supplierContact: order?.supplierContact ?? "",
  note: order?.note ?? "",
  internalNote: order?.internalNote ?? "",
});

/**
 * The editor of a purchase order (P15, P16, P18): a new draft, a draft, or a
 * sent order's changes. A draft changes freely; a sent order keeps its
 * supplier and currency, keeps every saved line (closed on the order page,
 * never removed here) and takes new lines, a quantity down to what is
 * received, a price, a discount and an expected day. Picking a product fills
 * the line from the supplier's item; a quantity under its minimum or off its
 * multiple is a warning, never a refusal.
 */
export const OrderEditor = ({
  order,
  supplierId,
  access,
  onDone,
}: {
  order?: PurchaseOrder;
  supplierId?: number;
  access: PurchasingAccess;
  onDone?: () => void;
}) => {
  const { t } = usePurchasingFormat();
  if (!access.canManageOrders)
    return (
      <Alert color="yellow" variant="light">
        {t("ordersManageNeeded")}
      </Alert>
    );
  return <Editor order={order} supplierId={supplierId} access={access} onDone={onDone} />;
};

const Editor = ({
  order,
  supplierId,
  access,
  onDone,
}: {
  order?: PurchaseOrder;
  supplierId?: number;
  access: PurchasingAccess;
  onDone?: () => void;
}) => {
  const { t, money } = usePurchasingFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const sites = useQuery(purchasingSitesQueryOptions());
  const buyers = useQuery(purchasingBuyersQueryOptions());
  const meta = useQuery(purchasingMetaQueryOptions());
  const [header, setHeader] = useState<Header>(() => headerOf(order, supplierId, access.userId));
  const [lines, setLines] = useState<EditorLine[]>(() => (order?.lines ?? []).map(fromSavedLine));
  const draft = !order || order.status === "draft";
  const nothingReceived = (order?.lines ?? []).every((l) => Number(l.received) === 0);
  const set = <K extends keyof Header>(key: K, value: Header[K]) => setHeader((h) => ({ ...h, [key]: value }));
  const setLine = (key: string, next: Partial<EditorLine>) =>
    setLines((all) => all.map((l) => (l.key === key ? { ...l, ...next } : l)));

  const save = useMutation({
    mutationFn: async () => {
      const body = lines.map(lineInput);
      if (!order)
        return createPurchaseOrder({
          supplierId: header.supplierId as number,
          siteId: header.siteId ? Number(header.siteId) : null,
          currency: header.currency.trim() || null,
          language: header.language,
          orderDate: header.orderDate,
          expectedOn: header.expectedOn,
          buyerId: header.buyerId,
          supplierContact: header.supplierContact,
          note: header.note,
          internalNote: header.internalNote,
          lines: body,
        });
      return updatePurchaseOrder(order.id, {
        expectedUpdatedAt: order.updatedAt,
        supplierId: header.supplierId as number,
        siteId: Number(header.siteId),
        currency: header.currency.trim(),
        language: header.language ?? order.language,
        orderDate: header.orderDate,
        expectedOn: header.expectedOn,
        buyerId: header.buyerId,
        supplierContact: header.supplierContact,
        note: header.note,
        internalNote: header.internalNote,
        lines: body,
      });
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
      if (!order) await navigate(purchaseOrderLinkOptions(saved.id));
      else onDone?.();
    },
    onError: (error) => markStaleOnConflict(queryClient, error),
  });
  const problems = new Map(refusedLines(save.error).map((l) => [l.index, lineRefusalWords(l.code, l.detail, t)]));
  const siteOptions = (sites.data?.items ?? [])
    .filter((s) => s.active || String(s.id) === header.siteId)
    .map((s) => ({ value: String(s.id), label: s.name }));
  const buyerOptions = (buyers.data?.items ?? [])
    .filter((b) => b.id)
    .map((b) => ({ value: b.id as string, label: b.displayName }));
  if (order?.buyer?.id && !buyerOptions.some((o) => o.value === order.buyer?.id))
    buyerOptions.unshift({ value: order.buyer.id, label: order.buyer.displayName });
  const currency = header.currency || order?.currency || meta.data?.baseCurrency || "NOK";
  const canSave = header.supplierId !== null && lines.every((l) => l.orderQuantity.trim() !== "");

  return (
    <Stack gap="lg">
      {order && !draft && (
        <Alert color="blue" variant="light">
          {t("sentOrderHint")}
        </Alert>
      )}
      <PageSection>
        <Stack gap="md">
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
            <SupplierPicker
              label={t("supplier")}
              value={header.supplierId}
              onChange={(id) => set("supplierId", id)}
              disabled={!draft}
              initialLabel={order?.supplierName}
            />
            <Select
              label={t("site")}
              placeholder={meta.data?.defaultSiteId ? t("defaultSite") : t("pickSite")}
              data={siteOptions}
              value={header.siteId}
              onChange={(v) => set("siteId", v)}
              clearable={!order}
              disabled={!!order && !nothingReceived}
            />
            <Select
              label={t("buyer")}
              data={buyerOptions}
              value={header.buyerId}
              onChange={(v) => set("buyerId", v)}
              searchable
              clearable
            />
            <DateInput
              label={t("orderDate")}
              valueFormat="YYYY-MM-DD"
              value={header.orderDate}
              onChange={(v) => set("orderDate", dayOf(v) ?? today())}
            />
            <DateInput
              label={t("expectedOn")}
              valueFormat="YYYY-MM-DD"
              clearable
              value={header.expectedOn}
              onChange={(v) => set("expectedOn", dayOf(v))}
            />
            <TextInput
              label={t("supplierContact")}
              maxLength={200}
              value={header.supplierContact}
              onChange={(e) => set("supplierContact", e.currentTarget.value)}
            />
            <TextInput
              label={t("currency")}
              maxLength={3}
              placeholder={order?.currency}
              value={header.currency}
              disabled={!draft}
              onChange={(e) => set("currency", e.currentTarget.value.toUpperCase())}
            />
            <Select
              label={t("language")}
              data={(["nb", "en"] as const).map((l) => ({ value: l, label: t(`languageName.${l}`) }))}
              value={header.language}
              onChange={(v) => set("language", v as Header["language"])}
              clearable={!order}
            />
          </SimpleGrid>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Textarea
              label={t("note")}
              description={t("noteHint")}
              autosize
              minRows={2}
              maxLength={4000}
              value={header.note}
              onChange={(e) => set("note", e.currentTarget.value)}
            />
            <Textarea
              label={t("internalNote")}
              description={t("internalNoteHint")}
              autosize
              minRows={2}
              maxLength={4000}
              value={header.internalNote}
              onChange={(e) => set("internalNote", e.currentTarget.value)}
            />
          </SimpleGrid>
        </Stack>
      </PageSection>
      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("lines")}</Title>
          <Table.ScrollContainer minWidth={980}>
            <Table data-testid="editor-lines" verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("product")}</Table.Th>
                  <Table.Th>{t("supplierItemNumber")}</Table.Th>
                  <Table.Th>{t("packaging")}</Table.Th>
                  <Table.Th>{t("quantity")}</Table.Th>
                  <Table.Th>{t("unitPrice")}</Table.Th>
                  <Table.Th>{t("discount")}</Table.Th>
                  <Table.Th>{t("expectedOn")}</Table.Th>
                  <Table.Th ta="right">{t("lineNet")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {lines.map((line, index) => (
                  <LineRow
                    key={line.key}
                    line={line}
                    index={index}
                    currency={currency}
                    removable={draft || line.id === undefined}
                    problem={problems.get(index)}
                    onChange={(next) => setLine(line.key, next)}
                    onRemove={() => setLines((all) => all.filter((l) => l.key !== line.key))}
                    money={money}
                  />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          {lines.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("noLines")}
            </Text>
          )}
          <Group align="flex-end">
            <div style={{ minWidth: 320 }}>
              <VariantPicker
                label={t("addVariantLine")}
                size="sm"
                supplierId={header.supplierId ?? undefined}
                onPick={(variant) => setLines((all) => [...all, fromVariant(variant, header.orderDate)])}
              />
            </div>
            <Button
              variant="default"
              leftSection={<IconPlus size={16} />}
              onClick={() => setLines((all) => [...all, newTextLine()])}
            >
              {t("addTextLine")}
            </Button>
          </Group>
          <Text size="xs" c="dimmed">
            {t("amountsExcludeVat")}
          </Text>
        </Stack>
      </PageSection>
      {save.isError && <SaveError error={save.error} />}
      <Group justify="flex-end">
        {onDone && (
          <Button variant="default" onClick={onDone}>
            {t("stopEditing")}
          </Button>
        )}
        {order ? (
          <Button loading={save.isPending} disabled={!canSave} onClick={() => save.mutate()}>
            {t("saveOrder")}
          </Button>
        ) : (
          <Button
            loading={save.isPending}
            disabled={!canSave}
            onClick={() => save.mutate()}
            data-assist="purchasing.new-order.create"
          >
            {t("createOrder")}
          </Button>
        )}
      </Group>
    </Stack>
  );
};

const LineRow = ({
  line,
  index,
  currency,
  removable,
  problem,
  onChange,
  onRemove,
  money,
}: {
  line: EditorLine;
  index: number;
  currency: string;
  removable: boolean;
  problem?: string;
  onChange: (next: Partial<EditorLine>) => void;
  onRemove: () => void;
  money: (amount: string, currency: string) => string;
}) => {
  const { t, quantity } = usePurchasingFormat();
  const warnings = lineWarnings(line);
  const isNew = line.id === undefined;
  const label = (field: string) => `${t(field)} ${index + 1}`;
  if (line.closed)
    return (
      <Table.Tr>
        <Table.Td>
          <Text size="sm">{line.sku ? `${line.sku} · ${line.name}` : line.name}</Text>
        </Table.Td>
        <Table.Td colSpan={7}>
          <Badge variant="light" color="gray">
            {t("lineClosed")}
          </Badge>
        </Table.Td>
        <Table.Td />
      </Table.Tr>
    );
  return (
    <Table.Tr>
      <Table.Td>
        {line.kind === "text" ? (
          <Stack gap={4}>
            <TextInput
              size="xs"
              aria-label={label("description")}
              placeholder={t("description")}
              value={line.name}
              onChange={(e) => onChange({ name: e.currentTarget.value })}
            />
            <TextInput
              size="xs"
              aria-label={label("unit")}
              placeholder={t("unit")}
              maxLength={20}
              value={line.unit}
              onChange={(e) => onChange({ unit: e.currentTarget.value })}
            />
          </Stack>
        ) : (
          <Text size="sm">{`${line.sku} · ${line.name}`}</Text>
        )}
        {warnings.map((w) => (
          <Text key={w} size="xs" c="orange" data-testid="line-warning">
            {line.minimum !== undefined || line.multiple !== undefined
              ? t(`warning.${w}`, {
                  minimum: quantity(line.minimum ?? "0"),
                  multiple: quantity(line.multiple ?? "0"),
                })
              : t(`warning.${w}.short`)}
          </Text>
        ))}
        {problem && (
          <Text size="xs" c="red">
            {problem}
          </Text>
        )}
      </Table.Td>
      <Table.Td>
        {line.kind === "variant" && (
          <TextInput
            size="xs"
            aria-label={label("supplierItemNumber")}
            maxLength={60}
            value={line.supplierItemNumber}
            onChange={(e) => onChange({ supplierItemNumber: e.currentTarget.value })}
          />
        )}
      </Table.Td>
      <Table.Td>
        {line.kind === "variant" &&
          (isNew ? (
            <Select
              size="xs"
              aria-label={label("packaging")}
              data={[
                { value: "", label: t("packagingUnit", { unit: line.unit }) },
                ...line.packagings.map((p) => ({
                  value: String(p.id),
                  label: t("packagingOption", { name: p.name, quantity: quantity(p.quantity), unit: line.unit }),
                })),
              ]}
              value={line.packagingId === null ? "" : String(line.packagingId)}
              onChange={(v) => onChange({ packagingId: v ? Number(v) : null })}
              allowDeselect={false}
            />
          ) : (
            <Text size="sm">{line.packagingName ?? line.unit}</Text>
          ))}
      </Table.Td>
      <Table.Td>
        <TextInput
          size="xs"
          w={90}
          aria-label={label("quantity")}
          inputMode="decimal"
          value={line.orderQuantity}
          onChange={(e) => onChange({ orderQuantity: e.currentTarget.value })}
        />
      </Table.Td>
      <Table.Td>
        <TextInput
          size="xs"
          w={110}
          aria-label={label("unitPrice")}
          inputMode="decimal"
          placeholder={line.kind === "variant" && isNew ? t("fromSupplierItem") : undefined}
          value={line.unitPrice}
          onChange={(e) => onChange({ unitPrice: e.currentTarget.value })}
        />
      </Table.Td>
      <Table.Td>
        <TextInput
          size="xs"
          w={70}
          aria-label={label("discount")}
          inputMode="decimal"
          value={line.discountPercent}
          onChange={(e) => onChange({ discountPercent: e.currentTarget.value })}
        />
      </Table.Td>
      <Table.Td>
        <DateInput
          size="xs"
          w={130}
          aria-label={label("expectedOn")}
          valueFormat="YYYY-MM-DD"
          clearable
          value={line.expectedOn}
          onChange={(v) => onChange({ expectedOn: dayOf(v) })}
        />
      </Table.Td>
      <Table.Td ta="right">{line.lineNet !== undefined ? money(line.lineNet, currency) : "—"}</Table.Td>
      <Table.Td>
        {removable && (
          <ActionIcon variant="subtle" color="red" aria-label={label("removeLine")} onClick={onRemove}>
            <IconTrash size={16} />
          </ActionIcon>
        )}
      </Table.Td>
    </Table.Tr>
  );
};
