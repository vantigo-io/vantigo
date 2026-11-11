import { Alert, Button, Group, Select, SimpleGrid, Stack, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { bomQueryOptions, bomsQueryOptions } from "../api/boms";
import { createOrder, type ManufacturingOrder, priorities, updateOrder } from "../api/orders";
import { isRevisionConflict, refusalCode, refusedFields } from "../api/request";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import {
  type OrderDefaults,
  type OrderValues,
  orderInputOf,
  orderProblems,
  orderUpdateOf,
  orderValuesOf,
} from "../lib/order-body";
import { orderLinkOptions } from "../lib/routes";
import { LocationSelect } from "./location-select";
import { PersonPicker } from "./person-picker";
import { VariantPicker } from "./variant-picker";

/** The fields a refusal can name that the form shows beside their input. */
const formFields = new Set([
  "variantId",
  "bomId",
  "quantity",
  "siteId",
  "componentLocationId",
  "outputLocationId",
  "dueDate",
  "plannedStart",
  "plannedFinish",
  "requestedStart",
]);

/** Which field a refusal code belongs to, when it is one field's. */
const codeField: Record<string, string> = {
  bom_required: "variantId",
  variant_not_goods: "variantId",
  variant_not_found: "variantId",
  bom_not_active: "bomId",
  quantity_invalid: "quantity",
  site_unknown: "siteId",
  work_center_other_site: "siteId",
};

export interface OrderFormProps {
  /** The order edited; a new draft when absent. */
  order?: ManufacturingOrder;
  /** After a save of an order edited. */
  onSaved?: () => void;
  onCancel?: () => void;
}

/**
 * A manufacturing order's fields (M17): the variant — only those with an
 * active BoM — and its BoM, the quantity, the site, the locations, the dates,
 * the priority, the responsible and a note. A draft may change all of them;
 * a confirmed or in-progress order only its dates — the requested start among
 * them (M35a) — priority, responsible, note and locations (M19). A new order opens its page once it is created.
 */
export const OrderForm = ({ order, onSaved, onCancel }: OrderFormProps) => {
  const { t, day } = useManufacturingFormat();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const invalidate = useInvalidateManufacturing();
  const [values, setValues] = useState<OrderValues>(() => orderValuesOf(order));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [problem, setProblem] = useState<string>();
  const draft = !order || order.status === "draft";
  const set = <Key extends keyof OrderValues>(key: Key, value: OrderValues[Key]) =>
    setValues((current) => ({ ...current, [key]: value }));

  const sites = useQuery(manufacturingSitesQueryOptions());
  const boms = useQuery({
    ...bomsQueryOptions({ variantId: values.variant?.id }),
    enabled: values.variant !== undefined,
  });
  const bomChoices = values.variant ? (boms.data?.items ?? []) : [];
  const defaultBomId = (bomChoices.find((bom) => bom.isDefault) ?? bomChoices[0])?.id ?? null;
  const bomId = values.bomId ?? defaultBomId;
  const bom = useQuery({ ...bomQueryOptions(bomId ?? 0), enabled: bomId !== null && draft });
  const siteId = values.siteId ?? bom.data?.siteId ?? sites.data?.[0]?.id ?? null;
  const site = sites.data?.find((one) => one.id === siteId);
  const outputLocationId = values.outputLocationId ?? site?.rootLocationId ?? null;
  const defaults: OrderDefaults = { bomId: defaultBomId, siteId, outputLocationId };

  const save = useMutation({
    mutationFn: () =>
      order
        ? updateOrder(order.id, orderUpdateOf(values, order, defaults))
        : createOrder(orderInputOf(values, defaults)),
    onSuccess: async (saved) => {
      notifications.show({ color: "teal", title: t(order ? "orderSaved" : "orderCreated"), message: saved.number });
      await invalidate();
      if (order) onSaved?.();
      else void navigate({ ...orderLinkOptions(saved.id), replace: true });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        onSaved?.();
        return;
      }
      const fields = refusedFields(error);
      const shown = Object.fromEntries(Object.entries(fields).filter(([field]) => formFields.has(field)));
      const code = refusalCode(error);
      if (code && codeField[code]) shown[codeField[code]] = t(`refusal.${code}`);
      if (code === "location_unknown") shown.outputLocationId = t("refusal.location_unknown");
      setErrors(shown);
      setProblem(refusalMessage(t, error));
    },
  });

  const submit = () => {
    const found = Object.fromEntries(
      Object.entries(orderProblems(values)).map(([field, key]) => [
        field === "variant" ? "variantId" : field,
        t(`problem_${key}`),
      ]),
    );
    setErrors(found);
    setProblem(undefined);
    if (Object.keys(found).length === 0) save.mutate();
  };

  const bomOptions = bomChoices.map((one) => ({
    value: String(one.id),
    label: `${one.code ?? t("bomWithoutCode")}${one.isDefault ? ` · ${t("default")}` : ""}`,
  }));
  if (order && !bomOptions.some((option) => option.value === String(order.bomId)))
    bomOptions.unshift({ value: String(order.bomId), label: order.bomCode ?? t("bomWithoutCode") });
  const siteOptions = (sites.data ?? []).map((one) => ({ value: String(one.id), label: `${one.code} · ${one.name}` }));

  return (
    <Stack gap="md" data-assist="manufacturing.order.form">
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <VariantPicker
          label={t("productVariant")}
          withAsterisk
          onlyWithBom
          disabled={Boolean(order)}
          value={values.variant}
          onChange={(next) => setValues((current) => ({ ...current, variant: next, bomId: null, siteId: null }))}
          error={errors.variantId}
        />
        <Select
          label={t("bom")}
          allowDeselect={false}
          disabled={!draft || !values.variant}
          placeholder={values.variant && boms.isSuccess && bomOptions.length === 0 ? t("noActiveBom") : undefined}
          data={bomOptions}
          value={bomId === null ? null : String(bomId)}
          onChange={(next) => setValues((current) => ({ ...current, bomId: next ? Number(next) : null, siteId: null }))}
          error={errors.bomId}
        />
        <TextInput
          label={t("quantity")}
          withAsterisk
          inputMode="decimal"
          disabled={!draft}
          value={values.quantity}
          onChange={(event) => set("quantity", event.currentTarget.value)}
          error={errors.quantity}
        />
        <Select
          label={t("site")}
          description={draft ? t("orderSiteHint") : undefined}
          allowDeselect={false}
          disabled={!draft}
          data={siteOptions}
          value={siteId === null ? null : String(siteId)}
          onChange={(next) =>
            setValues((current) => ({
              ...current,
              siteId: next ? Number(next) : null,
              componentLocationId: null,
              outputLocationId: null,
            }))
          }
          error={errors.siteId}
        />
        <LocationSelect
          label={t("componentLocation")}
          description={t("componentLocationHint")}
          placeholder={t("componentLocationDefault")}
          clearable
          siteId={siteId ?? undefined}
          value={values.componentLocationId}
          onChange={(next) => set("componentLocationId", next)}
          error={errors.componentLocationId}
        />
        <LocationSelect
          label={t("outputLocation")}
          description={t("outputLocationHint")}
          siteId={siteId ?? undefined}
          value={outputLocationId}
          onChange={(next) => set("outputLocationId", next)}
          error={errors.outputLocationId}
        />
        <TextInput
          type="date"
          label={t("dueDate")}
          value={values.dueDate}
          onChange={(event) => set("dueDate", event.currentTarget.value)}
          error={errors.dueDate}
        />
        <Select
          label={t("priority")}
          allowDeselect={false}
          data={priorities.map((value) => ({ value, label: t(`priority_${value}`) }))}
          value={values.priority}
          onChange={(next) => next && set("priority", next as OrderValues["priority"])}
        />
        <TextInput
          type="datetime-local"
          label={t("plannedStart")}
          value={values.plannedStart}
          onChange={(event) => set("plannedStart", event.currentTarget.value)}
          error={errors.plannedStart}
        />
        <TextInput
          type="datetime-local"
          label={t("plannedFinish")}
          value={values.plannedFinish}
          onChange={(event) => set("plannedFinish", event.currentTarget.value)}
          error={errors.plannedFinish}
        />
        <TextInput
          type="date"
          label={t("requestedStart")}
          description={
            order?.requestedStartDefault
              ? t("requestedStartHint", { date: day(order.requestedStartDefault) })
              : t("requestedStartHintNone")
          }
          value={values.requestedStart}
          onChange={(event) => set("requestedStart", event.currentTarget.value)}
          error={errors.requestedStart}
        />
        <PersonPicker
          label={t("responsible")}
          value={values.responsible}
          onChange={(next) => set("responsible", next)}
        />
      </SimpleGrid>
      <Textarea
        label={t("note")}
        autosize
        minRows={2}
        value={values.note}
        onChange={(event) => set("note", event.currentTarget.value)}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        {onCancel && (
          <Button variant="default" onClick={onCancel}>
            {t("cancel")}
          </Button>
        )}
        <Button loading={save.isPending} onClick={submit}>
          {order ? t("save") : t("createOrder")}
        </Button>
      </Group>
    </Stack>
  );
};
