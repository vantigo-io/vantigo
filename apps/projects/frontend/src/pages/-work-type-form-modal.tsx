import { Button, Group, Modal, NumberInput, Stack, Switch, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { ApiConflictError, ApiValidationError } from "../api/request";
import { createWorkType, updateWorkType, type WorkType, type WorkTypeInput } from "../api/work-types";
import "../i18n";
import { MAX_WORK_TYPE_NAME, multiplierProblem, nameLength } from "../lib/work-types";

/** Adding a type, or editing the one the caller just read off the card. */
export type WorkTypeModalState = { mode: "create" } | { mode: "edit"; workType: WorkType };

interface WorkTypeFormValues {
  name: string;
  billMultiplierPercent: number | string;
  costMultiplierPercent: number | string;
  active: boolean;
}

const percent = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

export interface WorkTypeFormModalProps {
  projectId: number;
  state: WorkTypeModalState | null;
  onClose: () => void;
}

/**
 * Adds or edits one work type (work types design D5). The form is mounted
 * fresh every time the modal opens, so no previous type's values survive a
 * close. The multipliers are entered as percentages — 150, not 1.5 — the way
 * the helper line says and the API stores them.
 */
export const WorkTypeFormModal = ({ projectId, state, onClose }: WorkTypeFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editWorkType") : t("addWorkType")}
      centered
    >
      {state && <WorkTypeForm projectId={projectId} state={state} onClose={onClose} />}
    </Modal>
  );
};

const WorkTypeForm = ({ projectId, state, onClose }: WorkTypeFormModalProps & { state: WorkTypeModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const workType = state.mode === "edit" ? state.workType : undefined;
  // The server's rules and words, so a typo is caught before a round trip.
  const multiplier = (value: number | string) => {
    const problem = multiplierProblem(value);
    return problem ? t(problem) : null;
  };

  const form = useForm<WorkTypeFormValues>({
    initialValues: {
      name: workType?.name ?? "",
      // 100 % is "as the rate says": a new type multiplies nothing until the
      // manager says by how much.
      billMultiplierPercent: workType?.billMultiplierPercent ?? 100,
      costMultiplierPercent: workType?.costMultiplierPercent ?? 100,
      active: workType?.active ?? true,
    },
    validate: {
      name: (value) => {
        const name = value.trim();
        if (!name) return t("workTypeNameRequired");
        return nameLength(name) > MAX_WORK_TYPE_NAME ? t("workTypeNameTooLong") : null;
      },
      billMultiplierPercent: multiplier,
      costMultiplierPercent: multiplier,
    },
  });

  const mutation = useMutation({
    mutationFn: (values: WorkTypeFormValues) => {
      const input: WorkTypeInput = {
        name: values.name.trim(),
        billMultiplierPercent: percent(values.billMultiplierPercent) ?? 0,
        costMultiplierPercent: percent(values.costMultiplierPercent) ?? 0,
        // `active` is the one field a PUT may leave out; creating never sends it.
        ...(workType ? { active: values.active } : {}),
      };
      return workType ? updateWorkType(projectId, workType.id, input) : createWorkType(projectId, input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("workTypeSaved"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      // The two writes answer 409 for one reason only: the project already
      // has a type of that name (the server's "Work type exists", no code).
      if (error instanceof ApiConflictError) {
        form.setFieldError("name", t("workTypeNameTaken"));
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveWorkType"), message: error.message });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <TextInput label={t("workTypeName")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <Group grow align="start">
          <NumberInput
            label={t("billMultiplier")}
            suffix=" %"
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("billMultiplierPercent")}
          />
          <NumberInput
            label={t("costMultiplier")}
            suffix=" %"
            decimalScale={2}
            withAsterisk
            {...form.getInputProps("costMultiplierPercent")}
          />
        </Group>
        <Text size="xs" c="dimmed">
          {t("multiplierHelp")}
        </Text>
        {workType && (
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
          <Button type="submit" loading={mutation.isPending}>
            {workType ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
