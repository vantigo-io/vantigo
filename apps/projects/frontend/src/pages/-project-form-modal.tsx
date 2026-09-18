import {
  Button,
  Group,
  Input,
  Modal,
  NumberInput,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import {
  ApiValidationError,
  type BillingType,
  createProject,
  type Project,
  type ProjectInput,
  updateProject,
} from "../api/projects";
import type { ApiError } from "../api/request";
import { CustomerPicker, type CustomerPickerValue } from "../components/customer-picker";
import "../i18n";
import { billingTypeLabelKey, billingTypes } from "../lib/billing";
import { useCodeSuggestion } from "../lib/use-code-suggestion";

/** Creating, optionally for a customer already known, or editing the project the caller just read. */
export type ProjectModalState = { mode: "create"; customerId?: number } | { mode: "edit"; project: Project };

const CODE_PATTERN = /^[A-Z0-9]{2,20}$/;
const CURRENCY_PATTERN = /^[A-Z]{3}$/;
const DEFAULT_CURRENCY = "NOK";

interface ProjectFormValues {
  customer: CustomerPickerValue;
  name: string;
  code: string;
  description: string;
  startDate: string | null;
  endDate: string | null;
  billingType: BillingType;
  fixedPriceAmount: number | string;
  budgetHours: number | string;
  budgetAmount: number | string;
  currency: string;
}

const amount = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

const normalizeCode = (value: string) => value.trim().toUpperCase();

/**
 * Creates or edits a project (design §8.2). One component, two modes: the
 * financial fields and the code suggestion belong to creating, the revision
 * and the code-change confirmation to editing.
 *
 * The form's state lives in `ProjectForm`, which the modal mounts fresh every
 * time it opens, so no previous project's values survive a close.
 */
export const ProjectFormModal = ({ state, onClose }: { state: ProjectModalState | null; onClose: () => void }) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editProjectTitle") : t("createProjectTitle")}
      centered
      size="lg"
    >
      {state && <ProjectForm state={state} onClose={onClose} />}
    </Modal>
  );
};

const ProjectForm = ({ state, onClose }: { state: ProjectModalState; onClose: () => void }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const isEdit = state.mode === "edit";
  const project = state.mode === "edit" ? state.project : undefined;
  const initialCustomer: CustomerPickerValue =
    state.mode === "edit"
      ? state.project.internal
        ? "internal"
        : (state.project.customerId ?? null)
      : (state.customerId ?? null);

  const form = useForm<ProjectFormValues>({
    initialValues: {
      customer: initialCustomer,
      name: project?.name ?? "",
      code: project?.code ?? "",
      description: project?.description ?? "",
      startDate: project?.startDate ?? null,
      endDate: project?.endDate ?? null,
      billingType: project?.billingType ?? "time-and-materials",
      fixedPriceAmount: project?.financials?.fixedPriceAmount ?? "",
      budgetHours: project?.budgetHours ?? "",
      budgetAmount: project?.financials?.budgetAmount ?? "",
      currency: project?.financials?.currency ?? DEFAULT_CURRENCY,
    },
    validate: {
      name: (value) => (value.trim() ? null : t("nameRequired")),
      code: (value) => {
        const code = normalizeCode(value);
        if (!code) return t("codeRequired");
        return CODE_PATTERN.test(code) ? null : t("codeInvalid");
      },
      billingType: (value, values) =>
        values.customer === "internal" && value !== "non-billable" ? t("internalMustBeNonBillable") : null,
      fixedPriceAmount: (value, values) =>
        values.billingType === "fixed-price" && amount(value) === undefined ? t("fixedPriceRequired") : null,
      currency: (value, values) => {
        const currency = value.trim().toUpperCase();
        const hasAmount = amount(values.fixedPriceAmount) !== undefined || amount(values.budgetAmount) !== undefined;
        if (!currency) return hasAmount ? t("currencyRequired") : null;
        return CURRENCY_PATTERN.test(currency) ? null : t("currencyInvalid");
      },
      endDate: (value, values) => (value && values.startDate && value < values.startDate ? t("endBeforeStart") : null),
    },
  });

  const internal = form.values.customer === "internal";
  const customerId = typeof form.values.customer === "number" ? form.values.customer : undefined;
  // Amounts belong to whoever may see them; creating a project, the caller
  // is about to become its manager and so always may.
  const showFinancials = !project || project.capabilities.canSeeFinancials;

  const {
    suggestion,
    isFollowing,
    setManual,
    useSuggestion: followSuggestionAgain,
  } = useCodeSuggestion(customerId, internal, form.values.name, !isEdit);

  // The form object is rebuilt on every render, so it stays out of the
  // dependencies: the suggestion is applied when the suggestion moves, not
  // whenever anything at all in the form does.
  useEffect(() => {
    if (isFollowing && suggestion) form.setFieldValue("code", suggestion);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isFollowing, suggestion]);

  const mutation = useMutation({
    mutationFn: (values: ProjectFormValues) => {
      const selectedCustomer = typeof values.customer === "number" ? values.customer : undefined;
      const input: ProjectInput = {
        name: values.name.trim(),
        code: normalizeCode(values.code),
        billingType: values.billingType,
        ...(selectedCustomer === undefined ? {} : { customerId: selectedCustomer }),
        ...(values.description.trim() ? { description: values.description.trim() } : {}),
        ...(values.startDate ? { startDate: values.startDate } : {}),
        ...(values.endDate ? { endDate: values.endDate } : {}),
        ...(amount(values.budgetHours) === undefined ? {} : { budgetHours: amount(values.budgetHours) }),
        ...(showFinancials ? financialFields(values) : {}),
      };
      return project ? updateProject(project.id, { ...input, revision: project.revision }) : createProject(input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({
        color: "teal",
        title: project ? t("projectUpdated") : t("projectCreated"),
        message: t("projectSaved"),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // The contract's field is `customerId`; the picker's field is `customer`.
        const { customerId: customerError, ...fieldErrors } = error.fieldErrors;
        form.setErrors(customerError ? { ...fieldErrors, customer: customerError } : fieldErrors);
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveProject"),
        message: conflict ? t("projectChangedElsewhere") : error.message,
      });
    },
  });

  const submit = form.onSubmit((values) => {
    // Timesheets and billing lines quote the code, so changing it on a
    // project that has one is confirmed first, the way a customer's type is.
    if (project && normalizeCode(values.code) !== project.code) {
      modals.openConfirmModal({
        title: t("changeCodeTitle"),
        children: <Text size="sm">{t("changeCodeWarning", { code: project.code })}</Text>,
        labels: { confirm: t("confirmChangeCode"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => mutation.mutate(values),
      });
      return;
    }
    mutation.mutate(values);
  });

  return (
    <form onSubmit={submit}>
      <Stack>
        <CustomerPicker
          value={form.values.customer}
          onChange={(value) => {
            form.setFieldValue("customer", value);
            // An internal project is never billed (design §4.1).
            if (value === "internal") form.setFieldValue("billingType", "non-billable");
          }}
          withInternal
          selectedLabel={project?.customerName ?? undefined}
          withAsterisk
        />
        <TextInput label={t("projectName")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <Stack gap="xs">
          <TextInput
            label={t("projectCode")}
            description={t("projectCodeDescription")}
            withAsterisk
            {...form.getInputProps("code")}
            onChange={(event) => {
              const value = event.currentTarget.value.toUpperCase();
              form.setFieldValue("code", value);
              setManual(value);
            }}
          />
          {!isEdit && suggestion && !isFollowing && suggestion !== form.values.code && (
            <Group>
              <Button variant="subtle" size="compact-xs" onClick={followSuggestionAgain}>
                {t("useSuggestion")}: {suggestion}
              </Button>
            </Group>
          )}
        </Stack>
        <Textarea label={t("description")} rows={3} {...form.getInputProps("description")} />
        <Group grow align="start">
          <DateInput
            label={t("startDate")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("startDate")}
          />
          <DateInput
            label={t("endDate")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("endDate")}
          />
        </Group>
        <Input.Wrapper
          label={t("billingType")}
          labelElement="div"
          error={form.errors.billingType}
          description={internal ? t("internalProjectDescription") : undefined}
        >
          <SegmentedControl
            fullWidth
            mt={4}
            aria-label={t("billingType")}
            disabled={internal}
            data={billingTypes.map((value) => ({ value, label: t(billingTypeLabelKey(value)) }))}
            value={form.values.billingType}
            onChange={(value) => form.setFieldValue("billingType", value as BillingType)}
          />
        </Input.Wrapper>
        <NumberInput label={t("budgetHours")} min={0} decimalScale={2} {...form.getInputProps("budgetHours")} />
        {showFinancials ? (
          <Group grow align="start">
            {form.values.billingType === "fixed-price" && (
              <NumberInput
                label={t("fixedPriceAmount")}
                min={0}
                decimalScale={2}
                withAsterisk
                {...form.getInputProps("fixedPriceAmount")}
              />
            )}
            <NumberInput label={t("budgetAmount")} min={0} decimalScale={2} {...form.getInputProps("budgetAmount")} />
            <TextInput label={t("currency")} maxLength={3} {...form.getInputProps("currency")} />
          </Group>
        ) : (
          <Text size="sm" c="dimmed">
            {t("financialsHidden")}
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {isEdit ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** The amount fields, present only for a caller who may see them and only where they mean something. */
const financialFields = (values: ProjectFormValues) => {
  const currency = values.currency.trim().toUpperCase();
  const fixedPrice = values.billingType === "fixed-price" ? amount(values.fixedPriceAmount) : undefined;
  const budget = amount(values.budgetAmount);
  return {
    ...(fixedPrice === undefined ? {} : { fixedPriceAmount: fixedPrice }),
    ...(budget === undefined ? {} : { budgetAmount: budget }),
    ...(currency ? { currency } : {}),
  };
};
