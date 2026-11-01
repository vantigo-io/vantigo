import { Anchor, Badge, Button, Divider, Group, Modal, Paper, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconDiamond, IconListCheck, IconTemplate } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { appUrl, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import { applyProjectTemplate, type ProjectTemplate, projectTemplatesQueryOptions } from "../api/templates";
import "../i18n";
import { localIsoDate } from "../lib/dates";

export interface ApplyTemplateModalProps {
  projectId: number;
  projectStartDate?: string | null;
  opened: boolean;
  onClose: () => void;
  /**
   * Whether the caller may create templates (`projects:create`, which the
   * host reads). With none to pick from, they are pointed at the catalog
   * rather than left at an empty picker.
   */
  canManageTemplates?: boolean;
}

/** Where the template catalog lives in the host. */
export const PROJECT_TEMPLATES_PATH = "/projects/templates";

export const ApplyTemplateModal = ({
  projectId,
  projectStartDate,
  opened,
  onClose,
  canManageTemplates = false,
}: ApplyTemplateModalProps) => {
  const { t } = useI18n("projects");
  const Link = useShellLink();
  const queryClient = useQueryClient();
  const { data: templatesData, isPending } = useQuery(projectTemplatesQueryOptions());

  const templates = templatesData?.templates ?? [];

  const defaultKickoff =
    projectStartDate && /^\d{4}-\d{2}-\d{2}$/.test(projectStartDate) ? projectStartDate : localIsoDate();

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
      handleClose();
    },
    onError: (error) => {
      // A refusal on anything but the request's own two fields is the stored
      // blueprint breaking a rule (one saved before the rules were tightened):
      // say so, and where it is fixed, rather than the bare problem title.
      let message = (error as ApiError).message;
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        const blueprint = Object.keys(fields).find((field) => field !== "kickoffDate" && field !== "templateId");
        const first = blueprint ?? Object.keys(fields)[0];
        message = blueprint
          ? t("templateBreaksRules", { reason: fields[blueprint] })
          : first
            ? fields[first]
            : error.message;
      }
      notifications.show({ color: "red", title: t("couldNotApplyTemplate"), message });
    },
  });

  const selectData = templates.map((tmpl) => ({
    value: String(tmpl.id),
    label: tmpl.name,
  }));

  // The modal stays mounted, so each opening starts again from the project's
  // start date rather than the date left from the last one.
  const handleClose = () => {
    setSelectedTemplateId(null);
    setKickoffDate(defaultKickoff);
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

        {!isPending && templates.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noTemplatesAvailable")}{" "}
            {canManageTemplates &&
              (Link ? (
                <Anchor
                  size="sm"
                  renderRoot={(anchorProps) => <Link to={PROJECT_TEMPLATES_PATH} {...anchorProps} />}
                  onClick={handleClose}
                >
                  {t("noTemplatesManageLink")}
                </Anchor>
              ) : (
                <Anchor size="sm" href={appUrl(PROJECT_TEMPLATES_PATH)}>
                  {t("noTemplatesManageLink")}
                </Anchor>
              ))}
          </Text>
        )}

        <DateInput
          label={t("kickoffDate")}
          valueFormat={t("dateInputFormat")}
          value={kickoffDate}
          onChange={(val) => setKickoffDate(typeof val === "string" ? val : val ? localIsoDate(val as Date) : null)}
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
