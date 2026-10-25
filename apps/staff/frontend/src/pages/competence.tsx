import {
  Alert,
  Anchor,
  Badge,
  Card,
  Group,
  List,
  MultiSelect,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useMediaQuery } from "@mantine/hooks";
import { IconAlertCircle, IconCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import {
  competenceCategoriesQueryOptions,
  competenceInterestsQueryOptions,
  competenceMatrixQueryOptions,
  competencesQueryOptions,
  type StaffCompetenceMatrixCell,
  type StaffCompetenceSummary,
} from "../api/competences";
import { staffMetaQueryOptions } from "../api/meta";
import type { StaffPersonSummary } from "../api/people";
import "../i18n";
import { competenceName, levelLabel, levelOptions } from "../lib/competences";
import { useStaffFormat } from "../lib/format";
import { personLinkOptions } from "../lib/routes";
import type { CompetenceSearch, CompetenceTab } from "../lib/search";

type Navigate = (options: { search: (previous: CompetenceSearch) => CompetenceSearch; replace?: boolean }) => void;

/** A person's name opening their Competence tab, as My team's items do. */
const PersonLink = ({ id, name }: { id: number; name: string }) => (
  <Anchor
    size="sm"
    fw={500}
    renderRoot={(props) => <Link {...personLinkOptions(id)} search={{ tab: "competence" }} {...props} />}
  >
    {name}
  </Anchor>
);

/**
 * The Competence area (staff competence design S14, §7, delivery C): the
 * matrix — every person active today with the competences they hold valid on
 * a day, filtered by category, competence, minimum level and verified only,
 * every filter in the URL — and, for HR, who wants to learn what and who
 * wants to use more of what they hold. The matrix is the Colleague read:
 * names, levels, validity and the declared badge, nothing more; who is
 * listed and what each cell carries is the server's answer.
 */
export const CompetencePage = () => {
  const { t } = useI18n("staff");
  const search = useSearch({ strict: false }) as CompetenceSearch;
  const navigate = useNavigate() as unknown as Navigate;
  const meta = useQuery(staffMetaQueryOptions());

  const header = <PageHeader title={t("competenceArea")} description={t("competenceAreaDescription")} />;
  if (meta.isError)
    return (
      <Stack gap="lg">
        {header}
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMatrix")}>
          {meta.error.message}
        </Alert>
      </Stack>
    );
  if (!meta.data) return <ContentSkeleton rows={6} rowHeight={44} />;
  // The interests overview is staff:manage's alone (plan reading 30), never the Approver's.
  const hr = meta.data.capabilities.canEdit;
  const current: CompetenceTab = hr ? (search.tab ?? "matrix") : "matrix";

  return (
    <Stack gap="lg">
      {header}
      <PageTabs
        aria-label={t("competenceViews")}
        items={[
          { value: "matrix", label: t("tab_matrix") },
          ...(hr ? [{ value: "interests" as const, label: t("tab_interests") }] : []),
        ]}
        value={current}
        onChange={(next) => navigate({ search: (previous) => ({ ...previous, tab: next }), replace: true })}
      >
        <Tabs.Panel value="matrix" pt="lg">
          <Matrix
            search={search}
            today={meta.data.today}
            onChange={(next) => navigate({ search: (previous) => ({ ...previous, ...next }) })}
          />
        </Tabs.Panel>
        {hr && (
          <Tabs.Panel value="interests" pt="lg">
            <InterestsOverview />
          </Tabs.Panel>
        )}
      </PageTabs>
    </Stack>
  );
};

type MatrixFilters = Omit<CompetenceSearch, "tab">;

const Matrix = ({
  search,
  today,
  onChange,
}: {
  search: CompetenceSearch;
  today: string;
  onChange: (next: MatrixFilters) => void;
}) => {
  const { t, locale } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  const { categoryId, competenceId, minLevel, validOn, verifiedOnly } = search;
  const matrix = useQuery(competenceMatrixQueryOptions({ categoryId, competenceId, minLevel, validOn, verifiedOnly }));
  const categories = useQuery(competenceCategoriesQueryOptions());
  // HR is answered the hidden and the archived too; anyone else what is offered.
  const catalogue = useQuery(competencesQueryOptions({ includeHidden: true, includeArchived: true }));

  const categoryLabel = (row: { names: { en: string; nb: string }; archivedAt?: string }) => {
    const name = competenceName(row, locale);
    return row.archivedAt ? t("archivedName", { name }) : name;
  };
  const order = new Map((categories.data ?? []).map((category, index) => [category.id, index]));
  const competenceOptions = [...new Set((catalogue.data ?? []).map((one) => one.categoryId))]
    .sort((a, b) => (order.get(a) ?? Number.MAX_SAFE_INTEGER) - (order.get(b) ?? Number.MAX_SAFE_INTEGER))
    .map((id) => {
      const category = categories.data?.find((one) => one.id === id);
      return {
        group: category ? competenceName(category, locale) : t("competenceCategoryUnknown"),
        items: (catalogue.data ?? [])
          .filter((one) => one.categoryId === id)
          .map((one) => ({ value: String(one.id), label: categoryLabel(one) })),
      };
    });
  const columnName = (competence: StaffCompetenceSummary) => {
    const name = competenceName(competence, locale);
    return competence.archived ? t("competenceArchivedName", { name }) : name;
  };
  const columns = matrix.data?.competences ?? [];
  const cellOf = (cells: StaffCompetenceMatrixCell[], id: number) => cells.find((one) => one.competenceId === id);

  return (
    <Stack gap="md">
      <Card withBorder padding="lg" radius="md">
        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
          <Select
            label={t("competenceCategory")}
            clearable
            data={(categories.data ?? []).map((one) => ({ value: String(one.id), label: categoryLabel(one) }))}
            value={categoryId ? String(categoryId) : null}
            onChange={(value) => onChange({ categoryId: value ? Number(value) : undefined })}
          />
          <MultiSelect
            label={t("matrixCompetences")}
            placeholder={t("competencePickPlaceholder")}
            nothingFoundMessage={t("competenceNothingFound")}
            searchable
            clearable
            data={competenceOptions}
            value={(competenceId ?? []).map(String)}
            onChange={(values) => onChange({ competenceId: values.length > 0 ? values.map(Number) : undefined })}
          />
          <Select
            label={t("minLevel")}
            clearable
            data={levelOptions(t)}
            value={minLevel === undefined ? null : String(minLevel)}
            onChange={(value) => onChange({ minLevel: value === null ? undefined : Number(value) })}
          />
          <DateInput
            label={t("matrixValidOn")}
            valueFormat={t("dateInputFormat")}
            clearable
            value={validOn ?? today}
            // Cleared, the matrix is today's again.
            onChange={(value) => onChange({ validOn: value && value !== today ? value : undefined })}
          />
        </SimpleGrid>
        <Switch
          mt="md"
          label={t("matrixVerifiedOnly")}
          description={t("matrixVerifiedOnlyDescription")}
          checked={verifiedOnly ?? false}
          onChange={(event) => onChange({ verifiedOnly: event.currentTarget.checked || undefined })}
        />
      </Card>

      {matrix.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMatrix")}>
          {matrix.error.message}
        </Alert>
      )}
      {matrix.isPending && <ContentSkeleton rows={5} rowHeight={44} />}
      {matrix.data?.people.length === 0 && (
        <EmptyState title={t("matrixNobody")} description={t("matrixNobodyDescription")} />
      )}
      {matrix.data && matrix.data.people.length > 0 && phone && (
        <Stack gap="sm">
          {matrix.data.people.map((person) => (
            <Card
              key={person.personId}
              withBorder
              padding="md"
              radius="md"
              component="section"
              aria-label={person.name}
            >
              <Stack gap="xs">
                <PersonLink id={person.personId} name={person.name} />
                {person.competences.length === 0 && (
                  <Text size="sm" c="dimmed">
                    {t("matrixHoldsNone")}
                  </Text>
                )}
                {columns.map((competence) => {
                  const cell = cellOf(person.competences, competence.id);
                  return (
                    cell && (
                      <Group key={competence.id} gap="xs" wrap="wrap">
                        <Text size="sm" fw={500}>
                          {columnName(competence)}
                        </Text>
                        <MatrixCell cell={cell} />
                      </Group>
                    )
                  );
                })}
              </Stack>
            </Card>
          ))}
        </Stack>
      )}
      {matrix.data && matrix.data.people.length > 0 && !phone && (
        <Table.ScrollContainer minWidth={240 + columns.length * 160}>
          <Table aria-label={t("tab_matrix")} highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("name")}</Table.Th>
                {columns.map((competence) => (
                  <Table.Th key={competence.id}>{columnName(competence)}</Table.Th>
                ))}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {matrix.data.people.map((person) => (
                <Table.Tr key={person.personId} data-person={person.personId}>
                  <Table.Td>
                    <PersonLink id={person.personId} name={person.name} />
                  </Table.Td>
                  {columns.map((competence) => {
                    const cell = cellOf(person.competences, competence.id);
                    return <Table.Td key={competence.id}>{cell && <MatrixCell cell={cell} />}</Table.Td>;
                  })}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </Stack>
  );
};

/** One held competence: the level or a check, the declared badge when nobody verified it, and the expiry. */
const MatrixCell = ({ cell }: { cell: StaffCompetenceMatrixCell }) => {
  const { t, date } = useStaffFormat();
  return (
    <Group gap={4} wrap="wrap">
      {cell.level !== undefined ? (
        <Text size="sm">{levelLabel(t, cell.level)}</Text>
      ) : (
        <IconCheck size={16} color="var(--mantine-color-teal-6)" role="img" aria-label={t("matrixHeld")} />
      )}
      {!cell.verified && (
        <Badge size="sm" variant="outline" color="yellow">
          {t("competenceDeclared")}
        </Badge>
      )}
      {cell.validTo && (
        <Text size="xs" c="dimmed">
          {t("competenceValidUntil", { date: date(cell.validTo) })}
        </Text>
      )}
    </Group>
  );
};

/**
 * Who wants what (plan reading 30), HR's gap-to-train view: one card per
 * competence somebody wants, those who want to learn it and those who hold
 * it and want to use it more, each name opening the person's tab.
 */
const InterestsOverview = () => {
  const { t, locale } = useI18n("staff");
  const interests = useQuery(competenceInterestsQueryOptions());
  if (interests.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInterestsOverview")}>
        {interests.error.message}
      </Alert>
    );
  if (!interests.data) return <ContentSkeleton rows={4} rowHeight={44} />;
  if (interests.data.length === 0)
    return <EmptyState title={t("interestsOverviewEmpty")} description={t("interestsOverviewEmptyDescription")} />;
  const people = (title: string, list: StaffPersonSummary[]) => (
    <Stack gap={4}>
      <Text size="sm" c="dimmed">
        {title}
      </Text>
      {list.length === 0 ? (
        <Text size="sm">—</Text>
      ) : (
        <List spacing={2} listStyleType="none" aria-label={title}>
          {list.map((person) => (
            <List.Item key={person.id}>
              <PersonLink id={person.id} name={person.displayName} />
            </List.Item>
          ))}
        </List>
      )}
    </Stack>
  );
  return (
    <SimpleGrid cols={{ base: 1, md: 2 }}>
      {interests.data.map((entry) => {
        const named = competenceName(entry.competence, locale);
        const name = entry.competence.archived ? t("competenceArchivedName", { name: named }) : named;
        return (
          <Card key={entry.competence.id} withBorder padding="lg" radius="md" component="section" aria-label={name}>
            <Stack gap="sm">
              <Title order={4} size="h5">
                {name}
              </Title>
              <SimpleGrid cols={2}>
                {people(t("wantsToLearn"), entry.wantsToLearn)}
                {people(t("wantsMore"), entry.wantsMore)}
              </SimpleGrid>
            </Stack>
          </Card>
        );
      })}
    </SimpleGrid>
  );
};
