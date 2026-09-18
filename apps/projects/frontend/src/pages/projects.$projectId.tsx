import { Alert, Anchor, Badge, Button, Card, Group, Menu, SimpleGrid, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChevronDown, IconPencil } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { type Project, type ProjectStatus, projectQueryOptions, setProjectStatus } from "../api/projects";
import { Field } from "../components/field";
import { ProjectStatusBadge } from "../components/project-status-badge";
import "../i18n";
import { billingTypeLabelKey } from "../lib/billing";
import { useProjectDates } from "../lib/dates";
import { projectStatuses, projectStatusLabelKey } from "../lib/status";
import { ProjectFormModal, type ProjectModalState } from "./-project-form-modal";
import { ProjectTimeline } from "./-project-timeline";

/**
 * The project page's header (design §8.2). The host owns the tab list, so the
 * header is mounted above it and every tab shares it: the code and name, the
 * status, who the project bills to, and — for a manager — the two things that
 * change the project itself.
 */
export const ProjectDetailHeader = ({ projectId, actions }: { projectId: number; actions?: ReactNode }) => {
  const { t } = useI18n("projects");
  const { data: project, isPending, isError, error } = useQuery(projectQueryOptions(projectId));
  const [modalState, setModalState] = useState<ProjectModalState | null>(null);

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")}>
        {error.message}
      </Alert>
    );
  }
  if (isPending) return <ContentSkeleton rows={2} rowHeight={40} />;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("projects"), to: "/projects" }, { label: project.name }]}
        title={
          <Group gap="sm" align="baseline">
            <Text ff="monospace" fz="inherit" fw={700} component="span">
              {project.code}
            </Text>
            <Text fz="inherit" component="span">
              {project.name}
            </Text>
          </Group>
        }
        description={
          <Group gap="xs" mt={4}>
            <ProjectStatusBadge status={project.status} size="lg" />
            <CustomerLink project={project} />
          </Group>
        }
        actions={
          <Group gap="sm">
            {project.capabilities.canManage && (
              <>
                <Button
                  variant="default"
                  leftSection={<IconPencil size={16} />}
                  onClick={() => setModalState({ mode: "edit", project })}
                >
                  {t("edit")}
                </Button>
                <StatusMenu project={project} />
              </>
            )}
            {actions}
          </Group>
        }
      />
      <ProjectFormModal state={modalState} onClose={() => setModalState(null)} />
    </Stack>
  );
};

/** Who the project bills to, or the badge that says it bills nobody. */
const CustomerLink = ({ project }: { project: Project }) => {
  const { t } = useI18n("projects");
  const Link = useShellLink();
  if (project.internal || project.customerId === undefined || project.customerId === null) {
    return (
      <Badge variant="light" color="grape">
        {t("internal")}
      </Badge>
    );
  }
  const to = `/customers/${project.customerId}`;
  const label = project.customerName ?? t("customer");
  return Link ? (
    <Anchor size="sm" renderRoot={(props) => <Link to={to} {...props} />}>
      {label}
    </Anchor>
  ) : (
    <Anchor size="sm" href={to}>
      {label}
    </Anchor>
  );
};

/**
 * Every status a manager may move the project to (D14: any transition is
 * allowed, including reopening). The one the project already has stays in the
 * list, disabled, so the menu reads as a state rather than a list of moves.
 */
const StatusMenu = ({ project }: { project: Project }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (status: ProjectStatus) => setProjectStatus(project.id, status),
    onSuccess: (updated) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        color: "teal",
        title: t("statusChanged"),
        message: t("statusChangedTo", { status: t(projectStatusLabelKey(updated.status)).toLocaleLowerCase() }),
      });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotChangeStatus"), message: error.message });
    },
  });

  return (
    <Menu position="bottom-end" withinPortal>
      <Menu.Target>
        <Button variant="default" rightSection={<IconChevronDown size={16} />} loading={mutation.isPending}>
          {t("changeStatus")}
        </Button>
      </Menu.Target>
      <Menu.Dropdown>
        {projectStatuses.map((status) => (
          <Menu.Item key={status} disabled={status === project.status} onClick={() => mutation.mutate(status)}>
            {t(projectStatusLabelKey(status))}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  );
};

/** The Overview tab: what the project is, and what has happened to it. */
export const ProjectOverview = ({ projectId }: { projectId: number }) => (
  <Stack gap="lg" mt="md">
    <ProjectDetailsCard projectId={projectId} />
    <ProjectTimeline projectId={projectId} />
  </Stack>
);

const ProjectDetailsCard = ({ projectId }: { projectId: number }) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  const { data: project, isPending, isError, error } = useQuery(projectQueryOptions(projectId));

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")}>
        {error.message}
      </Alert>
    );
  }
  if (isPending) return <ContentSkeleton rows={4} rowHeight={40} />;

  return (
    <Card withBorder padding="lg" radius="md" data-testid="project-details">
      <Stack gap="md">
        <Text fw={600} component="h3">
          {t("projectDetails")}
        </Text>
        <Stack gap={4}>
          <Text size="sm" c="dimmed">
            {t("description")}
          </Text>
          <Text size="sm">{project.description || t("noDescription")}</Text>
        </Stack>
        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md">
          <Field label={t("dates")}>{dates.range(project.startDate, project.endDate)}</Field>
          <Field label={t("billingType")}>{t(billingTypeLabelKey(project.billingType))}</Field>
          <Field label={t("budgetHours")}>
            {project.budgetHours === undefined || project.budgetHours === null
              ? t("notAvailable")
              : formatters.formatNumber(project.budgetHours)}
          </Field>
          <Field label={t("managers")}>
            {project.managers.length > 0
              ? project.managers.map((manager) => manager.displayName).join(", ")
              : t("noManager")}
          </Field>
        </SimpleGrid>
      </Stack>
    </Card>
  );
};
