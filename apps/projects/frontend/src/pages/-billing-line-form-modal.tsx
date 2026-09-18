import {
  Alert,
  Button,
  Group,
  Input,
  Modal,
  NumberInput,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { type BillingLine, type BillingLineInput, createBillingLine, updateBillingLine } from "../api/lines";
import { serviceVariantSearchQueryOptions } from "../api/products";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import "../i18n";
import { type PricingMode, pricingModeLabelKey, pricingModes } from "../lib/billing";

/** Adding a line, or editing the one the caller just read off the table. */
export type BillingLineModalState = { mode: "create" } | { mode: "edit"; line: BillingLine };

const LINE_CODE_PATTERN = /^[A-Z0-9]{1,10}$/;
const SEARCH_DEBOUNCE_MS = 300;

interface BillingLineFormValues {
  code: string;
  variantId: string | null;
  pricingMode: PricingMode;
  fixedAmount: number | string;
  discountPercent: number | string;
  active: boolean;
}

const amount = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

export interface BillingLineFormModalProps {
  projectId: number;
  /** The project's currency, shown beside a fixed amount; absent until one is set. */
  currency?: string;
  state: BillingLineModalState | null;
  onClose: () => void;
}

/**
 * Adds or edits one billing line (design §8.2). The form is mounted fresh by
 * the modal every time it opens, so no previous line's values survive a close.
 */
export const BillingLineFormModal = ({ projectId, currency, state, onClose }: BillingLineFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editBillingLine") : t("addBillingLine")}
      centered
    >
      {state && <BillingLineForm projectId={projectId} currency={currency} state={state} onClose={onClose} />}
    </Modal>
  );
};

const BillingLineForm = ({
  projectId,
  currency,
  state,
  onClose,
}: BillingLineFormModalProps & { state: BillingLineModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const line = state.mode === "edit" ? state.line : undefined;
  // The picker's own query, read from the cache rather than fetched again, so
  // the form knows when there is no picker to pick a variant with (D15) and
  // does not offer a Create the API would only refuse.
  const { error: variantError } = useQuery(serviceVariantSearchQueryOptions(""));
  const productsForbidden = (variantError as ApiError | null)?.status === 403;

  const form = useForm<BillingLineFormValues>({
    initialValues: {
      code: line?.code ?? "",
      variantId: line ? String(line.variantId) : null,
      pricingMode: line?.pricing?.mode ?? "list",
      fixedAmount: line?.pricing?.fixedAmount ?? "",
      discountPercent: line?.pricing?.discountPercent ?? "",
      active: line?.active ?? true,
    },
    validate: {
      code: (value) => {
        const code = value.trim().toUpperCase();
        if (!code) return t("lineCodeRequired");
        return LINE_CODE_PATTERN.test(code) ? null : t("lineCodeInvalid");
      },
      variantId: (value) => (value ? null : t("variantRequired")),
      fixedAmount: (value, values) =>
        values.pricingMode === "fixed" && amount(value) === undefined ? t("fixedAmountRequired") : null,
      discountPercent: (value, values) => {
        if (values.pricingMode !== "discount") return null;
        const percent = amount(value);
        return percent === undefined || percent <= 0 || percent > 100 ? t("discountPercentRequired") : null;
      },
    },
  });

  const mutation = useMutation({
    mutationFn: (values: BillingLineFormValues) => {
      const input: BillingLineInput = {
        code: values.code.trim().toUpperCase(),
        variantId: Number(values.variantId),
        pricingMode: values.pricingMode,
        ...(values.pricingMode === "fixed" ? { fixedAmount: amount(values.fixedAmount) } : {}),
        ...(values.pricingMode === "discount" ? { discountPercent: amount(values.discountPercent) } : {}),
        // `active` is the one field a PUT may leave out; creating never sends it.
        ...(line ? { active: values.active } : {}),
      };
      return line ? updateBillingLine(projectId, line.id, input) : createBillingLine(projectId, input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("lineSaved"), message: saved.trackableCode });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveLine"),
        message: conflict ? t("projectChangedElsewhere") : error.message,
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <VariantPicker
          value={form.values.variantId}
          onChange={(value) => form.setFieldValue("variantId", value)}
          error={form.errors.variantId}
          selectedLabel={line?.productName ?? undefined}
        />
        <TextInput
          label={t("lineCode")}
          description={t("lineCodeDescription")}
          withAsterisk
          data-autofocus
          {...form.getInputProps("code")}
          onChange={(event) => form.setFieldValue("code", event.currentTarget.value.toUpperCase())}
        />
        <Input.Wrapper label={t("pricingMode")} labelElement="div">
          <SegmentedControl
            fullWidth
            mt={4}
            aria-label={t("pricingMode")}
            data={pricingModes.map((mode) => ({ value: mode, label: t(pricingModeLabelKey(mode)) }))}
            value={form.values.pricingMode}
            onChange={(value) => form.setFieldValue("pricingMode", value as PricingMode)}
          />
        </Input.Wrapper>
        {form.values.pricingMode === "fixed" && (
          <NumberInput
            data-testid="fixed-amount"
            label={t("fixedAmount")}
            description={currency}
            min={0}
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("fixedAmount")}
          />
        )}
        {form.values.pricingMode === "discount" && (
          <NumberInput
            data-testid="discount-percent"
            label={t("discountPercent")}
            min={0}
            max={100}
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("discountPercent")}
          />
        )}
        {line && (
          <Switch
            label={t("active")}
            checked={form.values.active}
            onChange={(event) => form.setFieldValue("active", event.currentTarget.checked)}
          />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending} disabled={productsForbidden && !form.values.variantId}>
            {line ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * The variant the line is pinned to, searched straight from the products API
 * (D15). A caller without a products view permission is told why there is no
 * picker rather than being shown an empty one.
 */
const VariantPicker = ({
  value,
  onChange,
  error,
  selectedLabel,
}: {
  value: string | null;
  onChange: (value: string | null) => void;
  error?: ReactNode;
  selectedLabel?: string;
}) => {
  const { t } = useI18n("projects");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data, error: searchError } = useQuery(serviceVariantSearchQueryOptions(debouncedSearch));

  if ((searchError as ApiError | null)?.status === 403) {
    return (
      <Alert color="yellow" icon={<IconInfoCircle size={16} />} title={t("productVariant")}>
        {t("noProductsPermission")}
      </Alert>
    );
  }

  const variants = data ?? [];
  const options = variants.map((variant) => ({
    value: String(variant.variantId),
    label: `${variant.productName} · ${variant.sku}`,
  }));
  // The line's own variant keeps its name even when the current search does
  // not contain it, the way the customer picker keeps a chosen customer.
  if (value !== null && selectedLabel && !variants.some((variant) => String(variant.variantId) === value)) {
    options.push({ value, label: selectedLabel });
  }

  return (
    <Select
      label={t("productVariant")}
      placeholder={t("searchServices")}
      withAsterisk
      searchable
      error={error}
      // The products API has already filtered; filtering again would hide
      // matches whose name does not contain the term literally.
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noProductsFound")}
      data={options}
      value={value}
      onChange={onChange}
    />
  );
};
