import { Anchor, Button, Group, Modal, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconCheck } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ApiError, ApiValidationError } from "../api/request";
import { saveProjectAsTemplate } from "../api/templates";
import "../i18n";
import { TEMPLATE_DESCRIPTION_MAX, TEMPLATE_NAME_MAX } from "../lib/templates";
import { nameLength } from "../lib/work-types";
import { PROJECT_TEMPLATES_PATH } from "./-apply-template-modal";

export interface SaveAsTemplateModalProps {
  projectId: number;
  /** What the name starts as: the project's own name. */
  projectName: string;
  opened: boolean;
  onClose: () => void;
}

interface SaveAsTemplateValues {
  name: string;
  description: string;
}

/**
 * Saves the project's plan as a new template: the caller names it, and the
 * server snapshots the milestones, tasks, subtasks, checklists and links. The
 * form is mounted fresh each time the modal opens, so the name starts again
 * from the project's.
 */
export const SaveAsTemplateModal = ({ projectId, projectName, opened, onClose }: SaveAsTemplateModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal opened={opened} onClose={onClose} title={t("saveAsTemplateTitle")} centered size="md">
      {opened && <SaveAsTemplateForm projectId={projectId} projectName={projectName} onClose={onClose} />}
    </Modal>
  );
};

const SaveAsTemplateForm = ({ projectId, projectName, onClose }: Omit<SaveAsTemplateModalProps, "opened">) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const form = useForm<SaveAsTemplateValues>({
    initialValues: { name: projectName, description: "" },
    validate: {
      name: (value) => {
        const name = value.trim();
        if (!name) return t("templateNameRequired");
        return nameLength(name) > TEMPLATE_NAME_MAX ? t("templateNameTooLong") : null;
      },
      description: (value) =>
        nameLength(value.trim()) > TEMPLATE_DESCRIPTION_MAX ? t("deliveryMilestoneDescriptionTooLong") : null,
    },
  });

  const mutation = useMutation({
    mutationFn: (values: SaveAsTemplateValues) =>
      saveProjectAsTemplate(projectId, {
        name: values.name.trim(),
        description: values.description.trim() || undefined,
      }),
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects", "templates"] });
      onClose();
      // The notification renders outside the router, so the way to the
      // catalog is a plain link rather than the host's client-side one.
      notifications.show({
        color: "teal",
        icon: <IconCheck size={16} />,
        title: t("templateCreatedFromProject", { name: saved.name }),
        message: (
          <Anchor size="sm" href={PROJECT_TEMPLATES_PATH}>
            {t("openProjectTemplates")}
          </Anchor>
        ),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        const own = Object.fromEntries(
          Object.entries(fields).filter(([field]) => field === "name" || field === "description"),
        );
        if (Object.keys(own).length > 0) {
          form.setErrors(own);
          return;
        }
        // Anything else is the project's plan breaking a template rule — a
        // checklist longer than a template holds, say — which is fixed on the
        // project, not in this form.
        const [first] = Object.values(fields);
        notifications.show({
          color: "red",
          title: t("couldNotSaveTemplate"),
          message: first ? t("saveAsTemplatePlanBreaksRules", { reason: first }) : error.message,
        });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveTemplate"), message: (error as ApiError).message });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          {t("saveAsTemplateDescription")}
        </Text>
        <TextInput data-autofocus label={t("name")} withAsterisk {...form.getInputProps("name")} />
        <Textarea label={t("description")} rows={2} {...form.getInputProps("description")} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("saveTemplate")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
