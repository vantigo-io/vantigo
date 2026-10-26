import {
  ActionIcon,
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Table,
  Text,
} from "@mantine/core";
import {
  IconAlertCircle,
  IconChevronDown,
  IconChevronLeft,
  IconChevronRight,
  IconLock,
  IconPlus,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { Fragment, useState } from "react";
import type { PlanningAllocation } from "../api/allocations";
import { boardQueryOptions, type PlanningBoardRow } from "../api/board";
import { planningMetaQueryOptions } from "../api/meta";
import { AllocationBars } from "../components/allocation-bars";
import { type AllocationPreset, AllocationSheet } from "../components/allocation-sheet";
import { LoadCell } from "../components/load-cell";
import { ProjectPicker } from "../components/project-picker";
import { WeekHeader } from "../components/week-header";
import { personLabel, usePlanningFormat } from "../lib/format";
import { personTimelineLinkOptions, projectPlanLinkOptions } from "../lib/routes";
import { BOARD_DEFAULT_WEEKS, BOARD_MAX_WEEKS, type BoardSearch, boardWeekCounts } from "../lib/search";
import { addDays, addWeeks, mondayOf, today, weeksBetween } from "../lib/weeks";

const NAME_WIDTH = 220;
const WEEK_WIDTH = 96;

/** The earliest Monday the server answers: thirteen weeks before this one (S8). */
const earliestMonday = () => addWeeks(mondayOf(today()), -13);

/**
 * The board (design S10, §7): people — or projects — against weeks, each
 * cell the week's load on the colour scale with tentative hours hatched
 * apart. A row opens to its allocations as bars; a planner's click on a cell
 * or on New allocation opens the allocation sheet. `planning:view`'s; a
 * planner Staff answers nobody to (no `staff:view`) is told so instead of
 * being shown an empty plan.
 */
export const BoardPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const search = useSearch({ strict: false }) as BoardSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(planningMetaQueryOptions());
  const canManage = meta.data?.canManage === true;

  const from = mondayOf(search.from ?? today());
  // A hand-edited `to` before this week's Monday (no `from` to check it against) still shows one week.
  const shown = search.to
    ? Math.max(1, Math.min(weeksBetween(from, search.to).length, BOARD_MAX_WEEKS))
    : BOARD_DEFAULT_WEEKS;
  const to = addDays(addWeeks(from, shown), -1);
  const groupBy = search.groupBy ?? "people";
  const showTentative = search.tentative !== false;
  const board = useQuery({
    ...boardQueryOptions({
      from,
      to,
      groupBy,
      departmentId: search.departmentId,
      competenceId: search.competenceId,
      projectId: search.projectId,
      includeTentative: showTentative,
      includePlanned: search.planned !== false,
    }),
    enabled: meta.data !== undefined && meta.data.workforceVisible !== false,
  });

  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  // Every department a load has shown, so picking one does not narrow the options to it.
  const [departments, setDepartments] = useState<ReadonlyMap<number, string>>(() => new Map());
  const [sheet, setSheet] = useState<{ allocation?: PlanningAllocation; preset?: AllocationPreset }>();
  const creating = search.create === "allocation" && canManage;
  const sheetOpen = sheet !== undefined || creating;

  const setSearch = (patch: Partial<BoardSearch>) =>
    navigate({ search: (previous: BoardSearch) => ({ ...previous, ...patch }), replace: true });
  const page = (weeks: number) => {
    const next = addWeeks(from, weeks);
    setSearch({ from: next, to: addDays(addWeeks(next, shown), -1) });
  };
  const closeSheet = () => {
    setSheet(undefined);
    if (search.create) setSearch({ create: undefined });
  };
  const toggle = (key: string) =>
    setExpanded((previous) => {
      const next = new Set(previous);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const weeks = board.data?.weeks.map((week) => week.monday) ?? weeksBetween(from, to);
  const thisWeek = mondayOf(today());
  const loaded = (board.data?.rows ?? []).flatMap((row) => (row.person?.department ? [row.person.department] : []));
  if (loaded.some((department) => departments.get(department.id) !== department.name))
    setDepartments((previous) => {
      const next = new Map(previous);
      for (const department of loaded) next.set(department.id, department.name);
      return next;
    });
  const departmentOptions = [...departments].map(([id, name]) => ({ value: String(id), label: name }));
  if (search.departmentId !== undefined && !departments.has(search.departmentId))
    departmentOptions.push({
      value: String(search.departmentId),
      label: t("departmentById", { id: search.departmentId }),
    });

  const rowKey = (row: PlanningBoardRow) => (row.person ? `person:${row.person.id}` : `project:${row.project?.id}`);
  const rowName = (row: PlanningBoardRow) =>
    row.person ? row.person.displayName : row.project ? `${row.project.code} ${row.project.name}` : "";
  const rowAllocations = (row: PlanningBoardRow) =>
    (board.data?.allocations ?? []).filter((allocation) =>
      row.person ? allocation.person.id === row.person.id : allocation.project.id === row.project?.id,
    );
  const presetFor = (row: PlanningBoardRow, monday: string): AllocationPreset => ({
    person: row.person ? { id: row.person.id, label: personLabel(row.person) } : undefined,
    project: row.project
      ? { id: row.project.id, label: `${row.project.code} ${row.project.name}`, status: row.project.status }
      : undefined,
    fromDate: monday,
    toDate: addDays(monday, 4),
  });

  const hidden = meta.data?.canView === true && meta.data.workforceVisible === false;

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("boardTitle")}
        description={t("boardDescription")}
        actions={
          canManage && !hidden ? (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setSheet({ preset: { fromDate: from } })}>
              {t("newAllocation")}
            </Button>
          ) : undefined
        }
      />

      {hidden ? (
        <Card withBorder padding="lg" radius="md">
          <EmptyState icon={IconLock} title={t("workforceHiddenTitle")} description={t("workforceHiddenDescription")} />
        </Card>
      ) : (
        <>
          <Group align="end" gap="sm" wrap="wrap">
            <Group gap={4} align="end">
              <ActionIcon
                variant="default"
                size="lg"
                aria-label={t("previousWeeks", { count: shown })}
                disabled={addWeeks(from, -shown) < earliestMonday()}
                onClick={() => page(-shown)}
              >
                <IconChevronLeft size={16} />
              </ActionIcon>
              <Button variant="default" onClick={() => setSearch({ from: undefined, to: undefined })}>
                {t("thisWeek")}
              </Button>
              <ActionIcon
                variant="default"
                size="lg"
                aria-label={t("nextWeeks", { count: shown })}
                onClick={() => page(shown)}
              >
                <IconChevronRight size={16} />
              </ActionIcon>
            </Group>
            <Select
              label={t("weeksShown")}
              w={130}
              allowDeselect={false}
              data={[...new Set([...boardWeekCounts, shown])]
                .sort((a, b) => a - b)
                .map((count) => ({ value: String(count), label: t("weeksOption", { count }) }))}
              value={String(shown)}
              onChange={(value) =>
                setSearch({
                  from: search.from,
                  to: value === String(BOARD_DEFAULT_WEEKS) ? undefined : addDays(addWeeks(from, Number(value)), -1),
                })
              }
            />
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                {t("groupBy")}
              </Text>
              <SegmentedControl
                aria-label={t("groupBy")}
                value={groupBy}
                onChange={(value) => setSearch({ groupBy: value === "project" ? "project" : undefined })}
                data={[
                  { value: "people", label: t("groupBy_people") },
                  { value: "project", label: t("groupBy_project") },
                ]}
              />
            </Stack>
            <Select
              label={t("department")}
              placeholder={t("allDepartments")}
              w={200}
              clearable
              data={departmentOptions}
              value={search.departmentId === undefined ? null : String(search.departmentId)}
              onChange={(value) => setSearch({ departmentId: value === null ? undefined : Number(value) })}
            />
            <div style={{ width: 240 }}>
              <ProjectPicker
                label={t("project")}
                placeholder={t("allProjects")}
                clearable
                value={search.projectId ?? null}
                onChange={(id) => setSearch({ projectId: id ?? undefined })}
              />
            </div>
            <Stack gap={6} pb={4}>
              <Switch
                label={t("showTentative")}
                checked={showTentative}
                onChange={(event) => setSearch({ tentative: event.currentTarget.checked ? undefined : false })}
              />
              <Switch
                label={t("includePipeline")}
                checked={search.planned !== false}
                onChange={(event) => setSearch({ planned: event.currentTarget.checked ? undefined : false })}
              />
            </Stack>
          </Group>

          <Card withBorder padding="sm" radius="md">
            {board.isError && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadBoard")}>
                {(board.error as { status?: number }).status === 400
                  ? t("refusal.window_out_of_bounds")
                  : board.error.message}
              </Alert>
            )}
            {(board.isPending || meta.isPending) && !board.isError && <ContentSkeleton rows={5} rowHeight={44} />}
            {board.data && board.data.rows.length === 0 && (
              <EmptyState
                title={groupBy === "project" ? t("noProjectRows") : t("noPeopleRows")}
                description={t("noRowsDescription")}
              />
            )}
            {board.data && board.data.rows.length > 0 && (
              <Table.ScrollContainer minWidth={NAME_WIDTH + weeks.length * WEEK_WIDTH}>
                <Table aria-label={t("boardTitle")} layout="fixed" verticalSpacing={4} horizontalSpacing={4}>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th
                        w={NAME_WIDTH}
                        style={{ position: "sticky", left: 0, zIndex: 1, background: "var(--mantine-color-body)" }}
                      >
                        {groupBy === "project" ? t("project") : t("person")}
                      </Table.Th>
                      {weeks.map((monday) => (
                        <Table.Th key={monday} w={WEEK_WIDTH} scope="col">
                          <WeekHeader monday={monday} current={monday === thisWeek} />
                        </Table.Th>
                      ))}
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {board.data.rows.map((row) => {
                      const key = rowKey(row);
                      const name = rowName(row);
                      const open = expanded.has(key);
                      return (
                        <Fragment key={key}>
                          <Table.Tr>
                            <Table.Th
                              scope="row"
                              style={{
                                position: "sticky",
                                left: 0,
                                zIndex: 1,
                                background: "var(--mantine-color-body)",
                              }}
                            >
                              <Group gap={4} wrap="nowrap">
                                <ActionIcon
                                  variant="subtle"
                                  size="sm"
                                  aria-label={open ? t("hideAllocations", { name }) : t("showAllocations", { name })}
                                  aria-expanded={open}
                                  onClick={() => toggle(key)}
                                >
                                  {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
                                </ActionIcon>
                                <Stack gap={0} style={{ minWidth: 0 }}>
                                  <Anchor
                                    size="sm"
                                    fw={500}
                                    truncate
                                    renderRoot={(props) =>
                                      row.person ? (
                                        <Link {...personTimelineLinkOptions(row.person.id)} {...props} />
                                      ) : (
                                        <Link {...projectPlanLinkOptions(row.project?.id ?? 0)} {...props} />
                                      )
                                    }
                                  >
                                    {name}
                                  </Anchor>
                                  <Text size="xs" c="dimmed" truncate>
                                    {row.person
                                      ? (row.person.department?.name ?? row.person.employeeNumber)
                                      : (row.project?.customer?.name ?? t(`projectStatus_${row.project?.status}`))}
                                  </Text>
                                </Stack>
                              </Group>
                            </Table.Th>
                            {row.cells.map((figures) => (
                              <Table.Td key={figures.monday}>
                                <LoadCell
                                  figures={figures}
                                  showTentative={showTentative}
                                  label={t("cellLabel", { name, week: format.week(figures.monday) })}
                                  onClick={
                                    canManage ? () => setSheet({ preset: presetFor(row, figures.monday) }) : undefined
                                  }
                                />
                              </Table.Td>
                            ))}
                          </Table.Tr>
                          {open && (
                            <Table.Tr>
                              <Table.Td />
                              <Table.Td colSpan={weeks.length}>
                                {rowAllocations(row).length === 0 ? (
                                  <Text size="xs" c="dimmed">
                                    {t("noAllocationsInWindow")}
                                  </Text>
                                ) : (
                                  <AllocationBars
                                    allocations={rowAllocations(row)}
                                    weeks={weeks}
                                    label={t("allocationsOf", { name })}
                                    name={(allocation) =>
                                      row.person
                                        ? format.projectName(allocation.project)
                                        : format.personName(allocation.person)
                                    }
                                    onOpen={canManage ? (allocation) => setSheet({ allocation }) : undefined}
                                  />
                                )}
                              </Table.Td>
                            </Table.Tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Card>
        </>
      )}

      {canManage && (
        <AllocationSheet
          opened={sheetOpen}
          onClose={closeSheet}
          allocation={sheet?.allocation}
          preset={sheet?.preset ?? { fromDate: from }}
        />
      )}
    </Stack>
  );
};
