import { Alert, Anchor, Badge, Button, Checkbox, Group, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { IconShoppingCartPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import {
  createOrdersFromProposals,
  type PurchasingProposal,
  type PurchasingProposalOption,
  type PurchasingProposalOrdersInput,
  type PurchasingProposalOrdersResult,
  proposalsQueryOptions,
} from "../api/proposals";
import { PURCHASING_QUERY_KEY, refusalCode, refusedLines } from "../api/request";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { lineRefusalWords, refusalMessage } from "../lib/errors";
import { newRequestId, today, usePurchasingFormat } from "../lib/format";
import { proposalsLinkOptions, purchaseOrderLinkOptions } from "../lib/routes";

/** A row's key: one proposal per variant and site (P32). */
const keyOf = (row: PurchasingProposal) => `${row.variantId}:${row.siteId}`;

/** A quantity as the server reads one: more than 0, at most three decimals. */
const validQuantity = (text: string) => /^\d+(\.\d{1,3})?$/.test(text.trim()) && Number(text) > 0;

/** What a person chose on a row: ticked, the supplier item, the quantity in its packaging. */
interface Choice {
  ticked: boolean;
  itemId?: number;
  quantity?: string;
}

/**
 * Purchase proposals (purchasing design P31–P35, A4): what should be bought,
 * from the demand's one source — Manufacturing's buy suggestions where
 * Manufacturing is on, Inventory's low stock where it is not — less what is
 * on draft orders and what was ordered since, each rounded to the chosen
 * supplier's item. A person ticks rows, changes the supplier or the quantity,
 * and **Create orders** makes one draft per supplier and site, or adds to one
 * (`purchasing:orders-manage`). A product with no supplier item is listed
 * under *No supplier*. Nothing is sent from here: each draft is checked and
 * sent from its page (P34).
 */
export const ProposalsPage = ({
  access = noPurchasingAccess,
  variantId,
  siteId,
}: {
  access?: PurchasingAccess;
  variantId?: number;
  siteId?: number;
}) => {
  const { t, quantity, unitPrice, date } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const list = useQuery(proposalsQueryOptions({ variantId, siteId }));
  const [choices, setChoices] = useState<Record<string, Choice>>({});
  const [created, setCreated] = useState<PurchasingProposalOrdersResult | undefined>();
  const rows = list.data?.rows ?? [];
  const orderable = rows.filter((row) => row.options.length > 0);
  const unsupplied = rows.filter((row) => row.options.length === 0);
  const optionOf = (row: PurchasingProposal): PurchasingProposalOption => {
    const id = choices[keyOf(row)]?.itemId;
    return row.options.find((o) => o.supplierItemId === id) ?? row.options[0];
  };
  const quantityOf = (row: PurchasingProposal) => choices[keyOf(row)]?.quantity ?? optionOf(row).orderQuantity;
  const choose = (row: PurchasingProposal, next: Partial<Choice>) =>
    setChoices((current) => ({
      ...current,
      [keyOf(row)]: { ...current[keyOf(row)], ticked: current[keyOf(row)]?.ticked ?? false, ...next },
    }));
  const ticked = orderable.filter((row) => choices[keyOf(row)]?.ticked);
  const allValid = ticked.every((row) => validQuantity(quantityOf(row)));

  // One request id per set of rows sent (A11): a click again with the same rows — after an answer
  // that never came — replays the first request rather than refusing it as ordered.
  const attempt = useRef<{ signature: string; id: string } | undefined>(undefined);
  const create = useMutation({
    mutationFn: (input: PurchasingProposalOrdersInput) => createOrdersFromProposals(input),
    onSuccess: async (result) => {
      attempt.current = undefined;
      setCreated(result);
      setChoices({});
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
    },
    onError: async (error) => {
      if (refusalCode(error) === "proposal_gone")
        await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY, "proposals"] });
    },
  });
  const submit = () => {
    const rows = ticked.map((row) => ({
      variantId: row.variantId,
      siteId: row.siteId,
      supplierItemId: optionOf(row).supplierItemId,
      orderQuantity: quantityOf(row).trim(),
    }));
    const signature = JSON.stringify(rows);
    if (attempt.current?.signature !== signature) attempt.current = { signature, id: newRequestId() };
    create.mutate({ clientRequestId: attempt.current.id, rows });
  };
  // A refused row is named by its index in the request as sent, whatever the list shows since.
  const sentRows = create.variables?.rows ?? [];
  const problems = new Map(
    refusedLines(create.error).map((l) => [
      sentRows[l.index] ? `${sentRows[l.index].variantId}:${sentRows[l.index].siteId}` : "",
      lineRefusalWords(l.code, l.detail, t),
    ]),
  );

  const source =
    list.data?.source === "replenishment"
      ? t("proposalsFromReplenishment")
      : list.data?.source === "low_stock"
        ? t("proposalsFromLowStock")
        : undefined;
  const now = today();
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("proposalsTitle")}
        description={t("proposalsDescription")}
        actions={
          access.canManageOrders && (
            <Button
              leftSection={<IconShoppingCartPlus size={16} />}
              disabled={ticked.length === 0 || !allValid}
              loading={create.isPending}
              onClick={submit}
            >
              {ticked.length > 0 ? t("createOrdersCount", { count: ticked.length }) : t("createOrders")}
            </Button>
          )
        }
      />
      {(variantId !== undefined || siteId !== undefined) && (
        <Alert color="blue" variant="light">
          <Group justify="space-between">
            <Text size="sm">{t("proposalsFiltered")}</Text>
            <Anchor size="sm" renderRoot={(props) => <Link {...proposalsLinkOptions()} {...props} />}>
              {t("proposalsShowAll")}
            </Anchor>
          </Group>
        </Alert>
      )}
      {created && created.orders.length > 0 && (
        <Alert
          color="green"
          title={t("ordersCreated", { count: created.orders.length })}
          withCloseButton
          onClose={() => setCreated(undefined)}
        >
          <Stack gap={4}>
            {created.orders.map((o) => (
              <Anchor
                key={o.orderId}
                size="sm"
                renderRoot={(props) => <Link {...purchaseOrderLinkOptions(o.orderId)} {...props} />}
              >
                {t("ordersCreatedDraft", { supplier: o.supplierName })}
              </Anchor>
            ))}
            <Text size="sm" c="dimmed">
              {t("ordersCreatedHint")}
            </Text>
          </Stack>
        </Alert>
      )}
      {create.isError && <Alert color="red">{refusalMessage(create.error, t)}</Alert>}
      {list.isError && (
        <Alert color="red" title={t("proposalsFailed")}>
          {refusalMessage(list.error, t)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && rows.length === 0 && (
        <EmptyState title={t("noProposals")} description={t("noProposalsDescription")} />
      )}
      {orderable.length > 0 && (
        <PageSection>
          <Stack gap="xs">
            {source && (
              <Text size="sm" c="dimmed">
                {source}
              </Text>
            )}
            <Table.ScrollContainer minWidth={980}>
              <Table striped verticalSpacing="xs" data-testid="proposals">
                <Table.Thead>
                  <Table.Tr>
                    {access.canManageOrders && <Table.Th w={40} />}
                    <Table.Th>{t("proposalItem")}</Table.Th>
                    <Table.Th>{t("proposalSite")}</Table.Th>
                    <Table.Th>{t("proposalNeeded")}</Table.Th>
                    <Table.Th>{t("proposalSupplier")}</Table.Th>
                    <Table.Th>{t("proposalQuantity")}</Table.Th>
                    <Table.Th>{t("proposalPrice")}</Table.Th>
                    <Table.Th>{t("proposalOrderBy")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {orderable.map((row) => {
                    const key = keyOf(row);
                    const option = optionOf(row);
                    const value = quantityOf(row);
                    const names = { sku: row.sku, site: row.siteName };
                    const netted = Number(row.onDrafts) > 0 || Number(row.sentSince) > 0;
                    return (
                      <Table.Tr key={key}>
                        {access.canManageOrders && (
                          <Table.Td>
                            <Checkbox
                              aria-label={t("proposalSelect", names)}
                              checked={choices[key]?.ticked === true}
                              onChange={(event) => choose(row, { ticked: event.currentTarget.checked })}
                            />
                          </Table.Td>
                        )}
                        <Table.Td>
                          <Text size="sm">{row.sku}</Text>
                          <Text size="xs" c="dimmed">
                            {row.productName}
                          </Text>
                          {problems.get(key) && (
                            <Text size="xs" c="red">
                              {problems.get(key)}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>{row.siteName}</Table.Td>
                        <Table.Td>
                          <Text size="sm">{`${quantity(row.remaining)} ${row.unit}`}</Text>
                          {netted && (
                            <Text size="xs" c="dimmed">
                              {t("proposalNeededSentence", {
                                need: quantity(row.need),
                                drafts: quantity(row.onDrafts),
                                sent: quantity(row.sentSince),
                              })}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {access.canManageOrders && row.options.length > 1 ? (
                            <Select
                              size="xs"
                              w={200}
                              aria-label={t("proposalSupplierOf", names)}
                              allowDeselect={false}
                              value={String(option.supplierItemId)}
                              data={row.options.map((o) => ({
                                value: String(o.supplierItemId),
                                label: o.supplierName,
                              }))}
                              onChange={(id) => choose(row, { itemId: Number(id), quantity: undefined })}
                            />
                          ) : (
                            <Text size="sm">{option.supplierName}</Text>
                          )}
                          {option.supplierItemNumber && (
                            <Text size="xs" c="dimmed">
                              {option.supplierItemNumber}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {access.canManageOrders ? (
                            <TextInput
                              size="xs"
                              w={140}
                              aria-label={t("proposalQuantityOf", names)}
                              value={value}
                              error={validQuantity(value) ? undefined : t("refusal.quantity_invalid")}
                              rightSection={
                                <Text size="xs" c="dimmed">
                                  {option.packagingName ?? row.unit}
                                </Text>
                              }
                              rightSectionWidth={70}
                              onChange={(event) => choose(row, { quantity: event.currentTarget.value })}
                            />
                          ) : (
                            <Text size="sm">{`${quantity(option.orderQuantity)} ${option.packagingName ?? row.unit}`}</Text>
                          )}
                          {option.packagingName && (
                            <Text size="xs" c="dimmed" title={t("proposalRounded")}>
                              {`${quantity(Number(validQuantity(value) ? value : option.orderQuantity) * Number(option.packagingQuantity))} ${row.unit}`}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {option.price ? (
                            <Text size="sm" c={option.currencyDiffers ? "orange" : undefined}>
                              {unitPrice(option.price, option.currency)}
                            </Text>
                          ) : (
                            <Text size="sm" c="dimmed">
                              {t("proposalPriceNone")}
                            </Text>
                          )}
                          {option.currencyDiffers && (
                            <Text size="xs" c="orange">
                              {t("proposalCurrencyDiffers", { currency: option.currency })}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            <Text size="sm">{date(option.orderBy)}</Text>
                            {option.orderBy < now && (
                              <Badge size="xs" color="red" variant="light">
                                {t("proposalLate")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Stack>
        </PageSection>
      )}
      {unsupplied.length > 0 && (
        <PageSection title={t("proposalsNoSupplier")}>
          <Text size="sm" c="dimmed" mb="xs">
            {t("proposalsNoSupplierDescription")}
          </Text>
          <Table verticalSpacing="xs" data-testid="proposals-no-supplier">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("proposalItem")}</Table.Th>
                <Table.Th>{t("proposalSite")}</Table.Th>
                <Table.Th>{t("proposalNeeded")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {unsupplied.map((row) => (
                <Table.Tr key={keyOf(row)}>
                  <Table.Td>
                    <Text size="sm">{row.sku}</Text>
                    <Text size="xs" c="dimmed">
                      {row.productName}
                    </Text>
                  </Table.Td>
                  <Table.Td>{row.siteName}</Table.Td>
                  <Table.Td>{`${quantity(row.remaining)} ${row.unit}`}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </PageSection>
      )}
    </Stack>
  );
};
