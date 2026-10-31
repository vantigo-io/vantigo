import { Button, Group, Modal, Stack, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import {
  createDeliveryMilestone,
  type DeliveryMilestone,
  type DeliveryMilestoneCreateInput,
  updateDeliveryMilestone,
} from "../api/delivery-milestones";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import "../i18n";
import { DELIVERY_MILESTONE_DESCRIPTION_MAX, DELIVERY_MILESTONE_NAME_MAX } from "../lib/delivery-milestones";

export type DeliveryMilestoneModalState = { mode: "create" } | { mode: "edit"; milestone: DeliveryMilestone };

interface DeliveryMilestoneFormValues {
  name: string;
  description: string;
  targetDate: string | null;
}

export interface DeliveryMilestoneFormModalProps {
  projectId: number;
  state: DeliveryMilestoneModalState | null;
  onClose: () => void;
}

/** Adds or edits one delivery milestone. */
export const DeliveryMilestoneFormModal = ({ state, ...rest }: DeliveryMilestoneFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={rest.onClose}
      title={state?.mode === "edit" ? t("editDeliveryMilestoneTitle") : t("createDeliveryMilestoneTitle")}
      centered
    >
      {state && (
        <DeliveryMilestoneForm key={state.mode === "edit" ? state.milestone.id : "create"} state={state} {...rest} />
      )}
    </Modal>
  );
};

const DeliveryMilestoneForm = ({
  projectId,
  state,
  onClose,
}: DeliveryMilestoneFormModalProps & { state: DeliveryMilestoneModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const milestone = state.mode === "edit" ? state.milestone : undefined;

  const form = useForm<DeliveryMilestoneFormValues>({
    initialValues: {
      name: milestone?.name ?? "",
      description: milestone?.description ?? "",
      targetDate: milestone?.targetDate ?? null,
    },
    validate: {
      name: (value) => {
        const name = value.trim();
        if (!name) return t("deliveryMilestoneNameRequired");
        return name.length > DELIVERY_MILESTONE_NAME_MAX ? t("deliveryMilestoneNameTooLong") : null;
      },
      description: (value) =>
        value.trim().length > DELIVERY_MILESTONE_DESCRIPTION_MAX ? t("deliveryMilestoneDescriptionTooLong") : null,
    },
  });

  const mutation = useMutation({
    mutationFn: (values: DeliveryMilestoneFormValues) => {
      const input: DeliveryMilestoneCreateInput = {
        name: values.name.trim(),
        ...(values.description.trim() ? { description: values.description.trim() } : {}),
        ...(values.targetDate ? { targetDate: values.targetDate } : {}),
      };
      return milestone
        ? updateDeliveryMilestone(milestone.id, {
            name: input.name,
            description: input.description ?? null,
            targetDate: input.targetDate ?? null,
            revision: milestone.revision,
          })
        : createDeliveryMilestone(projectId, input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("deliveryMilestoneSaved"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveDeliveryMilestone"),
        message: conflict ? t("taskChangedElsewhere") : error.message,
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack gap="md">
        <TextInput
          data-autofocus
          label={t("name")}
          withAsterisk
          placeholder={t("createDeliveryMilestoneTitle")}
          {...form.getInputProps("name")}
        />
        <DateInput
          label={t("targetDate")}
          valueFormat={t("dateInputFormat")}
          clearable
          {...form.getInputProps("targetDate")}
        />
        <Textarea label={t("description")} rows={3} {...form.getInputProps("description")} />
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
