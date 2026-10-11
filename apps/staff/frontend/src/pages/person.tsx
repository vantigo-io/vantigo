import { Alert, Group, Stack, Tabs, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { staffMetaQueryOptions } from "../api/meta";
import { personEventsQueryOptions, personQueryOptions } from "../api/people";
import { NotFoundError } from "../api/request";
import { PersonStatusBadge } from "../components/person-status";
import "../i18n";
import { PEOPLE_ROUTE_PATH } from "../lib/routes";
import type { PersonSearch, PersonTab } from "../lib/search";
import { PersonEmployments } from "./-person-employments";
import { PersonHistory } from "./-person-history";
import { PersonProfile } from "./-person-profile";

/**
 * The person page (design §7): Profile, Employments and History, the tab in
 * the URL. What it shows is what `GET /people/{id}` and its sub-resources
 * answer this reader (§5); what it offers follows the person's
 * capabilities. The Account, Absence and Documents tabs belong to later
 * deliveries and are not drawn at all until they exist.
 */
export const PersonPage = () => {
  const { t } = useI18n("staff");
  const { id } = useParams({ strict: false }) as { id: string };
  const personId = Number(id);
  const { tab = "profile" } = useSearch({ strict: false }) as PersonSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const meta = useQuery(staffMetaQueryOptions());
  const person = useQuery({ ...personQueryOptions(personId), retry: false });
  // The history is the person's own, a manager's of theirs and HR's (§5's
  // Events row); to anyone else it is the unknown id's 404 — so the tab is
  // left out on that 404 alone. Any other failure is shown in the tab.
  const history = useQuery({ ...personEventsQueryOptions(personId, 1), enabled: person.isSuccess, retry: false });
  // Decided once per person, on the first answer: a refetch puts a query
  // without data back to pending, which must not take the tab away from
  // under a reader looking at the error it shows.
  const [historyFor, setHistoryFor] = useState<{ id: number; readable: boolean } | null>(null);
  if (historyFor?.id !== personId && (history.isSuccess || history.isError))
    setHistoryFor({ id: personId, readable: !(history.error instanceof NotFoundError) });
  const historyReadable = historyFor?.id === personId && historyFor.readable;

  if (person.error instanceof NotFoundError)
    return <EmptyState title={t("personNotFound")} description={t("personNotFoundHint")} />;
  if (person.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPerson")}>
        {person.error.message}
      </Alert>
    );
  if (!person.data || !meta.data) return <ContentSkeleton rows={6} rowHeight={44} />;
  const data = person.data;

  const tabs = [
    { value: "profile" as const, label: t("tab_profile") },
    { value: "employments" as const, label: t("tab_employments") },
    ...(historyReadable ? [{ value: "history" as const, label: t("tab_history") }] : []),
  ];
  const current: PersonTab = tabs.some((one) => one.value === tab) ? tab : "profile";

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("people"), to: PEOPLE_ROUTE_PATH }, { label: data.displayName }]}
        title={data.displayName}
        description={
          <Group gap="xs" component="span">
            <Text component="span">{data.employeeNumber}</Text>
            <PersonStatusBadge status={data.status} />
          </Group>
        }
      />
      <PageTabs
        aria-label={t("personViews")}
        items={tabs}
        value={current}
        onChange={(next) => navigate({ search: { tab: next === "profile" ? undefined : next }, replace: true })}
      >
        <Tabs.Panel value="profile" pt="lg">
          <PersonProfile
            person={data}
            canOpenPeople={meta.data.capabilities.canViewDirectory || meta.data.capabilities.canEdit}
          />
        </Tabs.Panel>
        <Tabs.Panel value="employments" pt="lg">
          <PersonEmployments person={data} today={meta.data.today} structure={meta.data.structure} />
        </Tabs.Panel>
        {historyReadable && (
          <Tabs.Panel value="history" pt="lg">
            <PersonHistory
              page={(page) => personEventsQueryOptions(personId, page)}
              person={data}
              structure={meta.data.structure}
            />
          </Tabs.Panel>
        )}
      </PageTabs>
    </Stack>
  );
};
