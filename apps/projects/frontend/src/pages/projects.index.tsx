import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import {
  ContentSkeleton,
  EmptyState,
  KpiCard,
  PageHeader,
  useDebouncedListSearch,
  useI18n,
  useShellLink,
} from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { accessQueryOptions } from "../api/access";
import {
  type ProjectListParams,
  type ProjectSummary,
  projectStatsQueryOptions,
  projectsQueryOptions,
} from "../api/projects";
import { CustomerPicker } from "../components/customer-picker";
import { ProjectStatusBadge } from "../components/project-status-badge";
import "../i18n";
import { holdsPermission } from "../lib/permissions";
import { isProjectStatus, projectStatuses, projectStatusLabelKey } from "../lib/status";
import { ProjectFormModal, type ProjectModalState } from "./-project-form-modal";

/**
 * The list's URL search params, which the host route validates (Task 14).
 * `create` only ever arrives as `true`, from Spotlight's quick action.
 */
export interface ProjectsSearch extends ProjectListParams {
  create?: boolean;
}

/** The toolbar's project-type filter, and the `internal` param each choice asks for. */
const projectTypes = ["customer", "internal"] as const;
type ProjectTypeFilter = (typeof projectTypes)[number];
const isProjectTypeFilter = (value: string): value is ProjectTypeFilter =>
  (projectTypes as readonly string[]).includes(value);

/**
 * The project list (design §8.2): counts, a toolbar whose every choice lands
 * in the URL, and the table. Search, filters and page live in validated URL
 * search params the host route declares; the page reads and navigates them
 * the way every other module's list page does.
 */
export const ProjectsPage = () => {
  const { t, formatters } = useI18n("projects");
  const { create, ...params } = useSearch({ strict: false }) as ProjectsSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: params.search,
    onNavigate: (next, options) => navigate({ search: { ...params, ...next }, ...options }),
  });
  const filterBy = (next: Partial<ProjectListParams>) => navigate({ search: { ...params, ...next, page: 1 } });
  // The intent is consumed: it must not reopen the form on refresh or back.
  const dropCreateIntent = () => navigate({ search: params, replace: true });

  const access = useQuery(accessQueryOptions());
  const canCreate = holdsPermission(access.data?.permissions, "projects:create");

  const [modalState, setModalState] = useState<ProjectModalState | null>(null);
  // Open the create form when `create` arrives in the URL, once per arrival:
  // state adjusted during render from the previous render's value, the way
  // React documents, rather than an effect that would flash the closed form.
  // It waits for the permissions, so a caller who may not create never sees it.
  const [createSeen, setCreateSeen] = useState(false);
  if (create && !access.isPending && !createSeen) {
    setCreateSeen(true);
    if (canCreate) setModalState({ mode: "create" });
  }
  if (!create && createSeen) setCreateSeen(false);
  useEffect(() => {
    if (create && !access.isPending && !canCreate) dropCreateIntent();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [create, access.isPending, canCreate]);
  const closeModal = () => {
    setModalState(null);
    if (create) dropCreateIntent();
  };

  const { data, isPending, isError, error } = useQuery(projectsQueryOptions(params));
  const { data: stats } = useQuery(projectStatsQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("projects")}
        description={t("projectsDescription")}
        actions={
          canCreate && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
              {t("newProject")}
            </Button>
          )
        }
      />

      <ProjectFormModal state={modalState} onClose={closeModal} />

      {stats && (
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" data-testid="projects-kpis">
          <KpiCard label={t("activeProjects")} value={formatters.formatNumber(stats.active)} />
          <KpiCard label={t("plannedProjects")} value={formatters.formatNumber(stats.planned)} />
          <KpiCard label={t("onHoldProjects")} value={formatters.formatNumber(stats.onHold)} />
        </SimpleGrid>
      )}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end" wrap="wrap">
            <TextInput
              placeholder={t("searchProjects")}
              aria-label={t("searchProjects")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={320}
            />
            <Select
              label={t("status")}
              placeholder={t("allStatuses")}
              clearable
              w={170}
              data={projectStatuses.map((status) => ({ value: status, label: t(projectStatusLabelKey(status)) }))}
              value={params.status || null}
              onChange={(value) => filterBy({ status: value && isProjectStatus(value) ? value : "" })}
            />
            <Select
              label={t("projectType")}
              placeholder={t("allProjectTypes")}
              clearable
              w={180}
              data={[
                { value: "customer", label: t("customerProjects") },
                { value: "internal", label: t("internalProjects") },
              ]}
              value={params.internal === undefined ? null : params.internal ? "internal" : "customer"}
              onChange={(value) =>
                filterBy({ internal: value && isProjectTypeFilter(value) ? value === "internal" : undefined })
              }
            />
            <CustomerPicker
              label={t("customer")}
              placeholder={t("allCustomers")}
              w={220}
              value={params.customerId ?? null}
              onChange={(value) => filterBy({ customerId: typeof value === "number" ? value : undefined })}
            />
            <Switch label={t("myProjects")} checked={params.mine} onChange={() => filterBy({ mine: !params.mine })} />
          </Group>

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProjects")}>
              {error.message}
            </Alert>
          )}

          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}

          {data && (
            <>
              <Table.ScrollContainer minWidth={860}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("code")}</Table.Th>
                      <Table.Th>{t("name")}</Table.Th>
                      <Table.Th>{t("customer")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("managers")}</Table.Th>
                      <Table.Th>{t("dates")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((project) => (
                      <ProjectRow key={project.id} project={project} />
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>

              {data.data.length === 0 && <EmptyState title={t("noProjectsFound")} />}

              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination total={data.pagination.totalPages} value={params.page} onChange={onPageChange} />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};

const ProjectRow = ({ project }: { project: ProjectSummary }) => {
  const { t, formatters } = useI18n("projects");
  const Link = useShellLink();
  const to = `/projects/${project.id}`;
  const [first, ...rest] = project.managers;
  // Plain calendar dates: formatted in UTC so a local evening does not move them a day.
  const day = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeZone: "UTC" });
  const start = project.startDate ? day(project.startDate) : null;
  const end = project.endDate ? day(project.endDate) : null;

  return (
    <Table.Tr>
      <Table.Td>
        {Link ? (
          <Anchor ff="monospace" fw={600} renderRoot={(props) => <Link to={to} {...props} />}>
            {project.code}
          </Anchor>
        ) : (
          <Anchor href={to} ff="monospace" fw={600}>
            {project.code}
          </Anchor>
        )}
      </Table.Td>
      <Table.Td>{project.name}</Table.Td>
      <Table.Td>
        {project.internal ? (
          <Badge variant="light" color="grape">
            {t("internal")}
          </Badge>
        ) : (
          (project.customerName ?? t("notAvailable"))
        )}
      </Table.Td>
      <Table.Td>
        <ProjectStatusBadge status={project.status} />
      </Table.Td>
      <Table.Td>
        {first ? (
          <Group gap="xs" wrap="nowrap">
            <Text size="sm">{first.displayName}</Text>
            {rest.length > 0 && (
              <Text size="sm" c="dimmed">
                {t("moreManagers", { more: rest.length })}
              </Text>
            )}
          </Group>
        ) : (
          <Text size="sm" c="dimmed">
            {t("noManager")}
          </Text>
        )}
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          {start || end ? `${start ?? t("notAvailable")} – ${end ?? t("notAvailable")}` : t("notAvailable")}
        </Text>
      </Table.Td>
    </Table.Tr>
  );
};
