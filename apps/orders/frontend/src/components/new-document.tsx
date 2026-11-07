import { Alert, Box, Button, Group, Select, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowRight, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { variantGridQueryOptions } from "../api/grid";
import { createOrder } from "../api/orders";
import { createQuote } from "../api/quotes";
import { ORDERS_QUERY_KEY } from "../api/request";
import { templateListQueryOptions } from "../api/templates";
import type { OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { newRequestId, useOrdersFormat } from "../lib/format";
import { QUOTES_ROUTE_PATH, quoteLinkOptions, SALES_ORDERS_ROUTE_PATH, salesOrderLinkOptions } from "../lib/routes";
import { CustomerPicker } from "./customer-picker";

export interface NewDocumentProps {
  kind: "quote" | "order";
  /** The customer, when the editor was opened from the customer page (O31a). */
  customerId?: number;
  /**
   * The product the page was opened for: its one variant is the order's first
   * line, and when it has several the person picks which.
   */
  productId?: number;
  access: OrdersAccess;
}

/** The currency the product's variants are listed in when the grid is read for the picker: the price is not shown. */
const GRID_CURRENCY = "NOK";

/**
 * A new quote or order starts from its customer: the server fills the rest of
 * the header from the customer's billing profile (O13) — currency, inkl. or
 * eks. mva, payment terms, Deres referanse — and, for a quote, from the
 * template chosen. The create carries one idempotency key for the page's
 * life, so a double click answers the same document (O27).
 */
export const NewDocument = ({ kind, customerId: preset, productId, access }: NewDocumentProps) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [customerId, setCustomerId] = useState<number | null>(preset ?? null);
  const [templateId, setTemplateId] = useState<string | null>(null);
  const [requestId] = useState(newRequestId);
  const [variantId, setVariantId] = useState<string | null>(null);
  // The product's variants are listed, and the only one is taken.
  const grid = useQuery({
    ...variantGridQueryOptions(productId ?? 0, GRID_CURRENCY),
    enabled: productId !== undefined && kind === "order" && access.canManageOrders,
  });
  // Creating waits for the variants, so the order never starts without the product's line by a click that
  // came too early; when they cannot be read the person is told, and the order starts empty.
  const gridPending = productId !== undefined && kind === "order" && access.canManageOrders && grid.isPending;
  const gridFailed = productId !== undefined && kind === "order" && grid.isError;
  const variants = [...(grid.data?.cells ?? []), ...(grid.data?.variants ?? [])].filter(
    (v, i, all) => all.findIndex((other) => other.variantId === v.variantId) === i,
  );
  // The product's only variant is the first line unless the person has chosen otherwise.
  const onlyVariant = variants.length === 1 ? String(variants[0]?.variantId) : null;
  const startVariant = variantId ?? onlyVariant;
  const allowed = kind === "quote" ? access.canManageQuotes : access.canManageOrders;
  const templates = useQuery({ ...templateListQueryOptions(), enabled: kind === "quote" && allowed });
  const create = useMutation({
    mutationFn: async () => {
      const header = {
        clientRequestId: requestId,
        customerId: customerId as number,
        ...(access.userId ? { salespersonId: access.userId } : {}),
      };
      if (kind === "quote")
        return (await createQuote({ ...header, ...(templateId ? { templateId: Number(templateId) } : {}) })).id;
      // An order opened from a message starts with the variant shown: one line, the quantity 1 (N17).
      const lines =
        startVariant === null ? [] : [{ kind: "product" as const, variantId: Number(startVariant), quantity: 1 }];
      return (await createOrder({ ...header, ...(lines.length > 0 ? { lines } : {}) })).id;
    },
    onSuccess: async (id) => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      navigate(kind === "quote" ? quoteLinkOptions(id) : salesOrderLinkOptions(id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreate"), message: refusalMessage(error, t) }),
  });
  const title = kind === "quote" ? t("newQuoteTitle") : t("newSalesOrderTitle");
  // Each kind's controls carry their own tour marker, so they are written out twice.
  const picker = <CustomerPicker value={customerId} onChange={setCustomerId} required />;
  const createButton = {
    rightSection: <IconArrowRight size={16} />,
    disabled: customerId === null || gridPending,
    loading: create.isPending || gridPending,
    onClick: () => create.mutate(),
  };
  return (
    <Stack gap="lg">
      <PageHeader
        title={title}
        breadcrumbs={[
          kind === "quote"
            ? { label: t("quotesTitle"), to: QUOTES_ROUTE_PATH }
            : { label: t("salesOrdersTitle"), to: SALES_ORDERS_ROUTE_PATH },
          { label: title },
        ]}
      />
      {!allowed ? (
        <Alert color="gray" icon={<IconLock size={16} />}>
          {kind === "quote" ? t("mayNotCreateQuote") : t("mayNotCreateOrder")}
        </Alert>
      ) : (
        <PageSection maw={560}>
          <Stack>
            <Text size="sm" c="dimmed">
              {kind === "quote" ? t("newQuoteHint") : t("newOrderHint")}
            </Text>
            {access.canViewCustomers ? (
              kind === "quote" ? (
                <Box data-assist="orders.new-quote.customer">{picker}</Box>
              ) : (
                <Box data-assist="orders.new-order.customer">{picker}</Box>
              )
            ) : preset !== undefined ? (
              <Text size="sm">{t("newForCustomer")}</Text>
            ) : (
              <Alert color="yellow">{t("needsCustomersView")}</Alert>
            )}
            {gridFailed && <Alert color="yellow">{t("productNotRead")}</Alert>}
            {kind === "order" && productId !== undefined && variants.length > 1 && (
              <Select
                label={t("startWithVariant", { product: grid.data?.productName ?? "" })}
                placeholder={t("noVariant")}
                clearable
                searchable
                data={variants.map((v) => ({
                  value: String(v.variantId),
                  label: [v.sku, Object.values(v.values).join(" / ")].filter(Boolean).join(" · "),
                }))}
                value={variantId}
                onChange={setVariantId}
              />
            )}
            {kind === "order" && startVariant !== null && variants.length <= 1 && (
              <Text size="sm">{t("startsWithProduct", { product: grid.data?.productName ?? "" })}</Text>
            )}
            {kind === "quote" && (templates.data ?? []).length > 0 && (
              <Select
                label={t("startFromTemplate")}
                placeholder={t("noTemplate")}
                clearable
                data={(templates.data ?? []).map((tpl) => ({ value: String(tpl.id), label: tpl.name }))}
                value={templateId}
                onChange={setTemplateId}
              />
            )}
            <Group justify="flex-end">
              {kind === "quote" ? (
                <Button {...createButton} data-assist="orders.new-quote.create">
                  {t("createQuote")}
                </Button>
              ) : (
                <Button {...createButton} data-assist="orders.new-order.create">
                  {t("createOrder")}
                </Button>
              )}
            </Group>
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
