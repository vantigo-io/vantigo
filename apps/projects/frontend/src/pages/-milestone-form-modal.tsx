import { Button, Group, Input, Modal, NumberInput, SegmentedControl, Stack, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type BillingMilestone, type BillingMilestoneInput, createMilestone, updateMilestone } from "../api/milestones";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import "../i18n";
import { MILESTONE_AMOUNT_MAX, MILESTONE_DESCRIPTION_MAX, MILESTONE_NAME_MAX } from "../lib/milestones";

/** Adding a milestone, or editing the one the caller just read off the plan. */
export type MilestoneModalState = { mode: "create" } | { mode: "edit"; milestone: BillingMilestone };

/** Which of the two ways the contract prices a milestone the form is showing. */
type PricedBy = "amount" | "percent";

interface MilestoneFormValues {
  name: string;
  description: string;
  plannedDate: string | null;
  pricedBy: PricedBy;
  amount: number | string;
  percent: number | string;
}

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

export interface MilestoneFormModalProps {
  projectId: number;
  /** The project's fixed price, when it has one: a percent milestone is a share of it. */
  fixedPrice?: number;
  /** The project's currency, which every amount on the milestone is in. */
  currency?: string;
  state: MilestoneModalState | null;
  onClose: () => void;
}

/**
 * Adds or edits one billing milestone (design §7.4). The form is mounted fresh
 * by the modal every time it opens, so no previous milestone's values survive
 * a close.
 */
export const MilestoneFormModal = ({ state, ...rest }: MilestoneFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={rest.onClose}
      title={state?.mode === "edit" ? t("editMilestoneTitle") : t("createMilestoneTitle")}
      centered
    >
      {state && <MilestoneForm state={state} {...rest} />}
    </Modal>
  );
};

const MilestoneForm = ({
  projectId,
  fixedPrice,
  currency,
  state,
  onClose,
}: MilestoneFormModalProps & { state: MilestoneModalState }) => {
  const { t, formatters } = useI18n("projects");
  const queryClient = useQueryClient();
  const milestone = state.mode === "edit" ? state.milestone : undefined;
  const hasFixedPrice = fixedPrice !== undefined && fixedPrice !== null;

  const form = useForm<MilestoneFormValues>({
    initialValues: {
      name: milestone?.name ?? "",
      description: milestone?.description ?? "",
      plannedDate: milestone?.plannedDate ?? null,
      // A milestone already priced as a share keeps its toggle even if the
      // project's fixed price has since gone: the server decides, not the form.
      pricedBy: milestone?.percent === undefined || milestone?.percent === null ? "amount" : "percent",
      amount: milestone?.amount ?? "",
      percent: milestone?.percent ?? "",
    },
    validate: {
      name: (value) => {
        const name = value.trim();
        if (!name) return t("milestoneNameRequired");
        return name.length > MILESTONE_NAME_MAX ? t("milestoneNameTooLong") : null;
      },
      description: (value) =>
        value.trim().length > MILESTONE_DESCRIPTION_MAX ? t("milestoneDescriptionTooLong") : null,
      amount: (value, values) => {
        if (values.pricedBy !== "amount") return null;
        const entered = numeric(value);
        return entered === undefined || entered <= 0 || entered > MILESTONE_AMOUNT_MAX
          ? t("milestoneAmountRequired")
          : null;
      },
      percent: (value, values) => {
        if (values.pricedBy !== "percent") return null;
        if (!hasFixedPrice) return t("milestonePercentNeedsFixedPrice");
        const entered = numeric(value);
        return entered === undefined || entered <= 0 || entered > 100 ? t("milestonePercentRequired") : null;
      },
    },
  });

  const mutation = useMutation({
    mutationFn: (values: MilestoneFormValues) => {
      const input: BillingMilestoneInput = {
        name: values.name.trim(),
        ...(values.description.trim() ? { description: values.description.trim() } : {}),
        ...(values.plannedDate ? { plannedDate: values.plannedDate } : {}),
        ...(values.pricedBy === "amount" ? { amount: numeric(values.amount) } : { percent: numeric(values.percent) }),
      };
      // A PUT is a full replace and carries neither the position nor the
      // status: an edit saved from an open form cannot undo somebody's
      // reordering or unmark an invoicing.
      return milestone
        ? updateMilestone(milestone.id, { ...input, revision: milestone.revision })
        : createMilestone(projectId, input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("milestoneSaved"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // A refusal on `status` is about the milestone being read-only rather
        // than about anything on screen, so it is said out loud instead.
        const { status: statusError, ...fieldErrors } = error.fieldErrors;
        if (Object.keys(fieldErrors).length > 0) form.setErrors(fieldErrors);
        if (statusError) {
          notifications.show({ color: "red", title: t("couldNotSaveMilestone"), message: statusError });
        }
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveMilestone"),
        message: conflict ? t("milestoneChangedElsewhere") : error.message,
      });
    },
  });

  const share = numeric(form.values.percent);
  // What the server will work out, shown while the number is being typed. Its
  // arithmetic is exact decimal; this is a preview and says "about".
  const preview =
    form.values.pricedBy === "percent" && hasFixedPrice && share !== undefined && share > 0 && share <= 100
      ? t("milestoneApproximateAmount", {
          amount: currency
            ? formatters.formatCurrency((fixedPrice * share) / 100, currency)
            : formatters.formatNumber((fixedPrice * share) / 100),
        })
      : undefined;

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <TextInput label={t("name")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <Textarea label={t("description")} rows={3} {...form.getInputProps("description")} />
        <DateInput
          label={t("plannedDate")}
          valueFormat={t("dateInputFormat")}
          clearable
          {...form.getInputProps("plannedDate")}
        />
        <Input.Wrapper
          label={t("milestonePricedBy")}
          labelElement="div"
          description={hasFixedPrice ? undefined : t("milestonePercentNeedsFixedPrice")}
        >
          <SegmentedControl
            fullWidth
            mt={4}
            aria-label={t("milestonePricedBy")}
            data={[
              { value: "amount", label: t("milestonePricedByAmount") },
              { value: "percent", label: t("milestonePricedByPercent"), disabled: !hasFixedPrice },
            ]}
            value={form.values.pricedBy}
            onChange={(value) => form.setFieldValue("pricedBy", value as PricedBy)}
          />
        </Input.Wrapper>
        {form.values.pricedBy === "amount" ? (
          <NumberInput
            data-testid="milestone-amount"
            label={t("amount")}
            description={currency}
            min={0}
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("amount")}
          />
        ) : (
          <NumberInput
            data-testid="milestone-percent"
            label={t("percent")}
            description={preview}
            min={0}
            max={100}
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("percent")}
          />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {milestone ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
