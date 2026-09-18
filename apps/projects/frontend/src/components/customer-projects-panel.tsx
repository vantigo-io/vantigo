import { Alert, Anchor, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type ProjectSummary, projectsQueryOptions } from "../api/projects";
import { useProjectDates } from "../lib/dates";
import "../i18n";
import { ProjectFormModal, type ProjectModalState } from "../pages/-project-form-modal";
import { ProjectStatusBadge } from "./project-status-badge";

export interface CustomerProjectsPanelProps {
  customerId: number;
  /** Whether to offer the create button — the host reads `projects:create`. */
  canCreate?: boolean;
}

/**
 * The customer page's Projects tab (design §8.2): that customer's projects,
 * visibility-filtered by the API exactly as the list page is, and a create
 * action that starts with the customer already chosen.
 */
export const CustomerProjectsPanel = ({ customerId, canCreate = false }: CustomerProjectsPanelProps) => {
  const { t } = useI18n("projects");
  const [modalState, setModalState] = useState<ProjectModalState | null>(null);
  const { data, isPending, isError, error } = useQuery(
    projectsQueryOptions({ page: 1, search: "", status: "", customerId, mine: false }),
  );
  const projects = data?.data ?? [];

  return (
    <Card withBorder padding="lg" radius="md" mt="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Text fw={600} component="h3">
            {t("projects")}
          </Text>
          {canCreate && (
            <Button
              size="xs"
              leftSection={<IconPlus size={14} />}
              onClick={() => setModalState({ mode: "create", customerId })}
            >
              {t("newProject")}
            </Button>
          )}
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProjects")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={48} />}

        {data && projects.length === 0 && (
          <EmptyState title={t("noProjectsYet")} description={t("createFirstProject")} size="sm" />
        )}

        {projects.length > 0 && (
          <Table.ScrollContainer minWidth={520}>
            <Table striped highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("code")}</Table.Th>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("dates")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {projects.map((project) => (
                  <PanelRow key={project.id} project={project} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>

      <ProjectFormModal state={modalState} onClose={() => setModalState(null)} />
    </Card>
  );
};

const PanelRow = ({ project }: { project: ProjectSummary }) => {
  const dates = useProjectDates();
  const Link = useShellLink();
  const to = `/projects/${project.id}`;
  return (
    <Table.Tr>
      <Table.Td>
        {Link ? (
          <Anchor ff="monospace" fw={600} size="sm" renderRoot={(props) => <Link to={to} {...props} />}>
            {project.code}
          </Anchor>
        ) : (
          <Anchor href={to} ff="monospace" fw={600} size="sm">
            {project.code}
          </Anchor>
        )}
      </Table.Td>
      <Table.Td>{project.name}</Table.Td>
      <Table.Td>
        <ProjectStatusBadge status={project.status} />
      </Table.Td>
      <Table.Td>
        <Text size="sm">{dates.range(project.startDate, project.endDate)}</Text>
      </Table.Td>
    </Table.Tr>
  );
};
