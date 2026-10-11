import {
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  Pagination,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useDebouncedListSearch, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { staffMetaQueryOptions } from "../api/meta";
import { type PeopleStatusFilter, peopleQueryOptions } from "../api/people";
import { PersonStatusBadge } from "../components/person-status";
import "../i18n";
import { personLinkOptions } from "../lib/routes";
import type { PeopleSearch } from "../lib/search";
import { NewPersonModal } from "./-new-person-modal";

export type { PeopleSearch } from "../lib/search";

/**
 * People (design §7, reading 15): the directory with search and the
 * structure filters, every one in the URL. Who is listed is the server's
 * answer — a colleague holding `staff:view` sees the people active today,
 * HR (`staff:manage`) filters by where people stand and sees the status
 * column — and what this page offers follows `/meta`'s capabilities, never a
 * permission read here.
 */
export const PeoplePage = () => {
  const { t } = useI18n("staff");
  const search = useSearch({ strict: false }) as PeopleSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { page = 1, create, q, ...filters } = search;

  const { data: meta, isError: metaFailed, error: metaError } = useQuery(staffMetaQueryOptions());
  const canEdit = meta?.capabilities.canEdit ?? false;
  const structure = meta?.structure;

  // `?create=person` opens the form on arrival — the host's Spotlight action
  // lands here. Consumed once: closing the form takes it out of the URL.
  const [creating, setCreating] = useState(create === "person");
  const [askedFor, setAskedFor] = useState(create);
  if (askedFor !== create) {
    setAskedFor(create);
    if (create === "person") setCreating(true);
  }

  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: q ?? "",
    onNavigate: ({ page: nextPage, search: text }, options) =>
      navigate({ search: { ...search, q: text || undefined, page: nextPage }, replace: options?.replace }),
  });

  const filterBy = (next: Partial<PeopleSearch>) => navigate({ search: { ...search, ...next, page: 1 } });

  // The status filter is HR's alone; a status in a link a colleague pasted
  // never reaches the server from their page. Nothing is asked before /meta
  // has said which of the two this caller is.
  const { data, isPending, isError, error } = useQuery({
    ...peopleQueryOptions({ ...filters, q, status: canEdit ? filters.status : undefined, page }),
    enabled: meta !== undefined,
  });
  const people = data?.data ?? [];

  const unitOptions = (structure?.businessUnits ?? []).map((row) => ({
    value: String(row.id),
    label: row.archivedAt ? t("archivedName", { name: row.name }) : row.name,
  }));
  const departmentOptions = (structure?.departments ?? []).map((row) => ({
    value: String(row.id),
    label: row.archivedAt ? t("archivedName", { name: `${row.name} (${row.code})` }) : `${row.name} (${row.code})`,
  }));
  const locationOptions = (structure?.locations ?? []).map((row) => ({
    value: String(row.id),
    label: row.archivedAt ? t("archivedName", { name: row.name }) : row.name,
  }));
  const idOf = (value: string | null) => (value ? Number(value) : undefined);

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("people")}
        description={t("peopleDescription")}
        actions={
          canEdit && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
              {t("newPerson")}
            </Button>
          )
        }
      />
      {metaFailed && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPeople")}>
          {metaError.message}
        </Alert>
      )}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <SimpleGrid cols={{ base: 1, sm: 2, md: structure ? 4 : 1 }}>
            <TextInput
              label={t("search")}
              placeholder={t("searchPlaceholder")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
            />
            {structure && (
              <>
                <Select
                  label={t("businessUnit")}
                  clearable
                  searchable
                  data={unitOptions}
                  value={filters.unitId ? String(filters.unitId) : null}
                  onChange={(value) => filterBy({ unitId: idOf(value) })}
                />
                <Select
                  label={t("department")}
                  clearable
                  searchable
                  data={departmentOptions}
                  value={filters.departmentId ? String(filters.departmentId) : null}
                  onChange={(value) => filterBy({ departmentId: idOf(value) })}
                />
                <Select
                  label={t("location")}
                  clearable
                  searchable
                  data={locationOptions}
                  value={filters.locationId ? String(filters.locationId) : null}
                  onChange={(value) => filterBy({ locationId: idOf(value) })}
                />
              </>
            )}
          </SimpleGrid>
          {canEdit && (
            <SegmentedControl
              aria-label={t("status")}
              value={filters.status ?? "current"}
              onChange={(value) =>
                filterBy({ status: value === "current" ? undefined : (value as PeopleStatusFilter) })
              }
              data={[
                { value: "current", label: t("statusFilterCurrent") },
                { value: "ended", label: t("statusFilterEnded") },
                { value: "all", label: t("statusFilterAll") },
              ]}
            />
          )}

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPeople")}>
              {error.message}
            </Alert>
          )}
          {isPending && !metaFailed && <ContentSkeleton rows={5} rowHeight={44} />}
          {data && people.length === 0 && <EmptyState title={t("noPeople")} description={t("noPeopleDescription")} />}

          {people.length > 0 && (
            <Table.ScrollContainer minWidth={640}>
              <Table aria-label={t("people")} highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("name")}</Table.Th>
                    <Table.Th>{t("employeeNumber")}</Table.Th>
                    <Table.Th>{t("department")}</Table.Th>
                    <Table.Th>{t("manager")}</Table.Th>
                    <Table.Th>{t("workEmail")}</Table.Th>
                    {canEdit && <Table.Th>{t("status")}</Table.Th>}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {people.map((person) => (
                    <Table.Tr key={person.id} data-person={person.id}>
                      <Table.Td>
                        <Anchor size="sm" renderRoot={(props) => <Link {...personLinkOptions(person.id)} {...props} />}>
                          {person.displayName}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{person.employeeNumber}</Table.Td>
                      <Table.Td>{person.department?.name ?? t("notSet")}</Table.Td>
                      <Table.Td>{person.manager?.displayName ?? t("notSet")}</Table.Td>
                      <Table.Td>{person.workEmail ?? t("notSet")}</Table.Td>
                      {canEdit && (
                        <Table.Td>
                          <PersonStatusBadge status={person.status} />
                        </Table.Td>
                      )}
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}

          {data && data.pagination.totalPages > 1 && (
            <Group justify="center">
              <Pagination total={data.pagination.totalPages} value={page} onChange={onPageChange} />
            </Group>
          )}
        </Stack>
      </Card>

      <NewPersonModal
        opened={creating}
        onClose={() => {
          setCreating(false);
          if (create) navigate({ search: { ...search, create: undefined }, replace: true });
        }}
        onCreated={(personId) => {
          setCreating(false);
          navigate(personLinkOptions(personId));
        }}
      />
    </Stack>
  );
};
