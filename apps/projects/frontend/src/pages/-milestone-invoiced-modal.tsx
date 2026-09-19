import { Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type BillingMilestone, setMilestoneStatus } from "../api/milestones";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import "../i18n";
import { MILESTONE_INVOICE_REFERENCE_MAX } from "../lib/milestones";

interface InvoicedFormValues {
  invoiceReference: string;
  invoiceDate: string | null;
}

export interface MilestoneInvoicedModalProps {
  /** The milestone being marked invoiced; null while the dialog has never been opened on one. */
  milestone: BillingMilestone | null;
  onClose: () => void;
}

/**
 * Marking one milestone invoiced. The reference and the date are optional and
 * are the only two fields the status operation accepts, so they are asked for
 * here rather than on the milestone form, which cannot carry them at all.
 */
export const MilestoneInvoicedModal = ({ milestone, onClose }: MilestoneInvoicedModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={milestone !== null}
      onClose={onClose}
      title={milestone ? t("markInvoicedTitle", { name: milestone.name }) : t("markMilestoneInvoiced")}
      centered
    >
      {milestone && <InvoicedForm key={milestone.id} milestone={milestone} onClose={onClose} />}
    </Modal>
  );
};

const InvoicedForm = ({ milestone, onClose }: { milestone: BillingMilestone; onClose: () => void }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();

  const form = useForm<InvoicedFormValues>({
    initialValues: {
      invoiceReference: milestone.invoiceReference ?? "",
      invoiceDate: milestone.invoiceDate ?? null,
    },
    validate: {
      invoiceReference: (value) =>
        value.trim().length > MILESTONE_INVOICE_REFERENCE_MAX ? t("invoiceReferenceTooLong") : null,
    },
  });

  const mutation = useMutation({
    mutationFn: (values: InvoicedFormValues) =>
      setMilestoneStatus(milestone.id, {
        status: "invoiced",
        revision: milestone.revision,
        ...(values.invoiceReference.trim() ? { invoiceReference: values.invoiceReference.trim() } : {}),
        ...(values.invoiceDate ? { invoiceDate: values.invoiceDate } : {}),
      }),
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("milestoneUpdated"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // The move itself can be refused; that answer arrives on `status`,
        // which is no field of this form.
        const { status: statusError, ...fieldErrors } = error.fieldErrors;
        if (Object.keys(fieldErrors).length > 0) form.setErrors(fieldErrors);
        if (statusError) {
          notifications.show({ color: "red", title: t("couldNotChangeMilestone"), message: statusError });
        }
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotChangeMilestone"),
        message: conflict ? t("milestoneChangedElsewhere") : error.message,
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        {/* The length is validated rather than capped: a pasted reference
            that is too long should say so, not be silently clipped. */}
        <TextInput label={t("invoiceReference")} data-autofocus {...form.getInputProps("invoiceReference")} />
        <DateInput
          label={t("invoiceDate")}
          valueFormat={t("dateInputFormat")}
          clearable
          {...form.getInputProps("invoiceDate")}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("markMilestoneInvoiced")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
