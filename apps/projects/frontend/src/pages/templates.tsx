import { ActionIcon, Alert, Button, Menu, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDots, IconPencil, IconPlus, IconTemplate, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { deleteProjectTemplate, type ProjectTemplate, projectTemplatesQueryOptions } from "../api/templates";
import "../i18n";
import { templateChecklistCount, templateSubtaskCount } from "../lib/templates";
import { TemplateFormModal, type TemplateModalState } from "./-template-form-modal";

/**
 * The workspace's project templates: the blueprints "Use template" on a
 * project's Tasks tab applies. Everyone in the app may read the catalog where
 * it is used, but this page is the people's who own it: the host offers and
 * guards the route on projects:create, the permission the API checks every
 * template write against, so whoever sees the page may create, edit and
 * delete.
 */
export const ProjectTemplatesPage = () => {
  const { t } = useI18n("projects");
  const { data, isPending, isError, error } = useQuery(projectTemplatesQueryOptions());
  const [modalState, setModalState] = useState<TemplateModalState | null>(null);
  const templates = data?.templates ?? [];

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("projectTemplates")}
        description={t("projectTemplatesDescription")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            {t("newTemplate")}
          </Button>
        }
      />

      <TemplateFormModal state={modalState} onClose={() => setModalState(null)} />

      <PageSection>
        <Stack gap="md">
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTemplates")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={4} rowHeight={48} />}

          {data && templates.length === 0 && (
            <EmptyState
              icon={IconTemplate}
              title={t("noProjectTemplates")}
              description={t("noProjectTemplatesCanCreate")}
            />
          )}

          {templates.length > 0 && (
            <Table.ScrollContainer minWidth={640}>
              <Table striped highlightOnHover aria-label={t("projectTemplates")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("name")}</Table.Th>
                    <Table.Th ta="right">{t("deliveryMilestones")}</Table.Th>
                    <Table.Th ta="right">{t("tasks")}</Table.Th>
                    <Table.Th ta="right">{t("subtasks")}</Table.Th>
                    <Table.Th ta="right">{t("templateChecklistItems")}</Table.Th>
                    <Table.Th ta="right">{t("dependencies")}</Table.Th>
                    <Table.Th w={48} aria-label={t("actions")} />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {templates.map((template) => (
                    <TemplateRow
                      key={template.id}
                      template={template}
                      onEdit={() => setModalState({ mode: "edit", template })}
                    />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};

const TemplateRow = ({ template, onEdit }: { template: ProjectTemplate; onEdit: () => void }) => {
  const { t, formatters } = useI18n("projects");
  const queryClient = useQueryClient();

  const remove = useMutation({
    mutationFn: () => deleteProjectTemplate(template.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects", "templates"] });
      notifications.show({ color: "teal", title: t("templateDeleted"), message: template.name });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotDeleteTemplate"), message: error.message });
    },
  });

  const confirmDelete = () =>
    modals.openConfirmModal({
      title: t("deleteTemplateTitle", { name: template.name }),
      children: <Text size="sm">{t("deleteTemplateWarning")}</Text>,
      labels: { confirm: t("deleteTemplate"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  const count = (n: number) => formatters.formatNumber(n);

  return (
    <Table.Tr>
      <Table.Td>
        <Text size="sm" fw={500}>
          {template.name}
        </Text>
        {template.description && (
          <Text size="xs" c="dimmed" lineClamp={2}>
            {template.description}
          </Text>
        )}
      </Table.Td>
      <Table.Td ta="right">{count(template.milestones.length)}</Table.Td>
      <Table.Td ta="right">{count(template.tasks.length)}</Table.Td>
      <Table.Td ta="right">{count(templateSubtaskCount(template))}</Table.Td>
      <Table.Td ta="right">{count(templateChecklistCount(template))}</Table.Td>
      <Table.Td ta="right">{count(template.dependencies.length)}</Table.Td>
      <Table.Td>
        <Menu position="bottom-end" shadow="md">
          <Menu.Target>
            <ActionIcon
              variant="subtle"
              aria-label={t("templateActionsFor", { name: template.name })}
              loading={remove.isPending}
            >
              <IconDots size={16} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconPencil size={14} />} onClick={onEdit}>
              {t("edit")}
            </Menu.Item>
            <Menu.Divider />
            <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={confirmDelete}>
              {t("deleteTemplate")}
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </Table.Td>
    </Table.Tr>
  );
};
