import { Badge, Button, Divider, Group, Modal, Paper, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconDiamond, IconListCheck, IconTemplate } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import { applyProjectTemplate, type ProjectTemplate, projectTemplatesQueryOptions } from "../api/templates";
import "../i18n";

export interface ApplyTemplateModalProps {
  projectId: number;
  projectStartDate?: string | null;
  opened: boolean;
  onClose: () => void;
}

export const ApplyTemplateModal = ({ projectId, projectStartDate, opened, onClose }: ApplyTemplateModalProps) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const { data: templatesData, isPending } = useQuery(projectTemplatesQueryOptions());

  const templates = templatesData?.templates ?? [];

  const defaultKickoff =
    projectStartDate && /^\d{4}-\d{2}-\d{2}$/.test(projectStartDate)
      ? projectStartDate
      : new Date().toISOString().slice(0, 10);

  const [selectedTemplateId, setSelectedTemplateId] = useState<string | null>(null);
  const [kickoffDate, setKickoffDate] = useState<string | null>(defaultKickoff);

  const selectedTemplate: ProjectTemplate | undefined = templates.find(
    (tmpl) => String(tmpl.id) === selectedTemplateId,
  );

  const mutation = useMutation({
    mutationFn: () => {
      if (!selectedTemplate || !kickoffDate) {
        throw new Error("Missing template or kickoff date");
      }
      return applyProjectTemplate(projectId, {
        templateId: selectedTemplate.id,
        kickoffDate,
      });
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        color: "teal",
        icon: <IconCheck size={16} />,
        title: t("templateApplied"),
        message: t("templateAppliedMessage", {
          milestones: result.milestonesCreated,
          tasks: result.tasksCreated,
          dependencies: result.dependenciesCreated,
        }),
      });
      onClose();
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("couldNotApplyTemplate"),
        message: error instanceof ApiValidationError ? error.message : (error as ApiError).message,
      });
    },
  });

  const selectData = templates.map((tmpl) => ({
    value: String(tmpl.id),
    label: tmpl.name,
  }));

  const handleClose = () => {
    setSelectedTemplateId(null);
    onClose();
  };

  return (
    <Modal
      opened={opened}
      onClose={handleClose}
      title={
        <Group gap="xs">
          <IconTemplate size={18} />
          <Text fw={600} size="sm">
            {t("applyTemplateTitle")}
          </Text>
        </Group>
      }
      centered
      size="md"
    >
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          {t("applyTemplateDescription")}
        </Text>

        <Select
          label={t("template")}
          placeholder={isPending ? "…" : t("selectTemplate")}
          data={selectData}
          value={selectedTemplateId}
          onChange={setSelectedTemplateId}
          searchable
          nothingFoundMessage={t("noTemplatesAvailable")}
          withAsterisk
        />

        <DateInput
          label={t("kickoffDate")}
          valueFormat={t("dateInputFormat")}
          value={kickoffDate}
          onChange={(val) =>
            setKickoffDate(typeof val === "string" ? val : val ? (val as Date).toISOString().slice(0, 10) : null)
          }
          withAsterisk
        />

        {selectedTemplate && (
          <Paper withBorder p="sm" radius="sm">
            <Stack gap="xs">
              {selectedTemplate.description && (
                <Text size="xs" c="dimmed">
                  {selectedTemplate.description}
                </Text>
              )}
              <Group gap="xs">
                <Badge size="sm" variant="light" color="blue" leftSection={<IconDiamond size={12} />}>
                  {t("templateMilestonesPreview", { count: selectedTemplate.milestones.length })}
                </Badge>
                <Badge size="sm" variant="light" color="gray" leftSection={<IconListCheck size={12} />}>
                  {t("templateTasksPreview", { count: selectedTemplate.tasks.length })}
                </Badge>
                {selectedTemplate.dependencies.length > 0 && (
                  <Badge size="sm" variant="outline" color="gray">
                    {t("templateDependenciesPreview", { count: selectedTemplate.dependencies.length })}
                  </Badge>
                )}
              </Group>

              {selectedTemplate.milestones.length > 0 && (
                <>
                  <Divider my={4} />
                  <Stack gap={4}>
                    {selectedTemplate.milestones.map((m, idx) => (
                      <Group key={`${m.name}-${idx}`} justify="space-between" align="center">
                        <Group gap="xs">
                          <IconDiamond size={13} />
                          <Text size="xs" fw={500}>
                            {m.name}
                          </Text>
                        </Group>
                        <Badge size="xs" variant="subtle" color="blue">
                          {t("dayOffset", { days: m.targetDayOffset })}
                        </Badge>
                      </Group>
                    ))}
                  </Stack>
                </>
              )}
            </Stack>
          </Paper>
        )}

        <Group justify="flex-end" mt="xs">
          <Button variant="default" onClick={handleClose}>
            {t("cancel")}
          </Button>
          <Button
            onClick={() => mutation.mutate()}
            loading={mutation.isPending}
            disabled={!selectedTemplateId || !kickoffDate}
          >
            {t("applyTemplateButton")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
