import { Alert, Group, Stack, Tabs, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { personHolidayAccountQueryOptions } from "../api/absence";
import { staffMetaQueryOptions } from "../api/meta";
import { personEventsQueryOptions, personQueryOptions, type StaffPerson } from "../api/people";
import { NotFoundError } from "../api/request";
import { ErasedBadge, PersonStatusBadge } from "../components/person-status";
import "../i18n";
import { readsDocuments } from "../lib/documents";
import { PEOPLE_ROUTE_PATH } from "../lib/routes";
import type { PersonSearch, PersonTab } from "../lib/search";
import { ErasedBanner } from "./-erased-banner";
import { LegalHoldAlert, LegalHoldButton } from "./-legal-hold";
import { PersonAbsence } from "./-person-absence";
import { PersonAccount } from "./-person-account";
import { PersonCompetence } from "./-person-competence";
import { PersonDocuments } from "./-person-documents";
import { PersonEmployments } from "./-person-employments";
import { PersonHistory } from "./-person-history";
import { PersonProfile } from "./-person-profile";
import { PersonalDataDownload } from "./-personal-data-download";

/**
 * The person page (design §7): Profile, Employments, Absence, Competence,
 * History and Documents, the tab in the URL. What it shows is what `GET /people/{id}`
 * and its sub-resources answer this reader (§5); what it offers follows the
 * person's capabilities. The Account tab (delivery C) is HR's
 * (`canManageAccount`), and the person's own and `staff:personal-data`'s,
 * read-only for them; a manager,
 * an Approver and a colleague get the unknown id's 404 for it, so it is not
 * drawn for them.
 *
 * The hold group (design S15) sits above the tabs: the legal hold and an
 * erased record's banner for `staff:manage` and `staff:personal-data`, and
 * for `staff:personal-data` (`canHold`) the hold, the reassessment and the
 * complete personal-data file. An erased record is read-only but for those,
 * an unlink and a rehire, so the tabs are handed the person without the
 * capabilities to change anything else — the page offers nothing the
 * server would refuse with `person_erased`. The Account tab stays for HR on
 * an erased record with *Disable now* alone, which the server takes there:
 * the access must end too.
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
  // The absence is the person's, a manager's of theirs, an Approver's and
  // HR's; to anyone else the holiday account is the unknown id's 404 — so the
  // tab is left out on that 404 alone, decided once per person as the history.
  const year = meta.data ? Number(meta.data.today.slice(0, 4)) : 0;
  const absence = useQuery({
    ...personHolidayAccountQueryOptions(personId, year),
    enabled: person.isSuccess && meta.isSuccess,
  });
  const [absenceFor, setAbsenceFor] = useState<{ id: number; readable: boolean } | null>(null);
  if (absenceFor?.id !== personId && (absence.isSuccess || absence.isError))
    setAbsenceFor({ id: personId, readable: !(absence.error instanceof NotFoundError) });
  const absenceReadable = absenceFor?.id === personId && absenceFor.readable;

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
  const erased = Boolean(data.erasedAt);
  const shown: StaffPerson = erased
    ? {
        ...data,
        capabilities: {
          ...data.capabilities,
          canEdit: false,
          canRevealIdentifiers: false,
          canHold: false,
          canVerifyCompetence: false,
          // Read-only: the wishes stay readable (canReadWishes), never stamped.
          canStampWishesSeen: false,
        },
      }
    : data;
  const timeZone = meta.data.timeZone;
  // The links group's readers (design §5): HR, the person and staff:personal-data
  // (canRevealIdentifiers) — the last two read-only.
  const readsAccount =
    data.capabilities.canManageAccount || data.capabilities.self || data.capabilities.canRevealIdentifiers;

  const tabs = [
    { value: "profile" as const, label: t("tab_profile") },
    { value: "employments" as const, label: t("tab_employments") },
    ...(absenceReadable ? [{ value: "absence" as const, label: t("tab_absence") }] : []),
    // Every reader who sees the person reads their competences (plan reading 9).
    { value: "competence" as const, label: t("tab_competence") },
    ...(historyReadable ? [{ value: "history" as const, label: t("tab_history") }] : []),
    ...(readsDocuments(data) ? [{ value: "documents" as const, label: t("tab_documents") }] : []),
    ...(readsAccount ? [{ value: "account" as const, label: t("tab_account") }] : []),
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
            {erased && <ErasedBadge />}
          </Group>
        }
        actions={
          data.capabilities.canHold && (
            <Group gap="xs" wrap="wrap">
              <PersonalDataDownload person={data} />
              <LegalHoldButton person={data} />
            </Group>
          )
        }
      />
      <ErasedBanner person={data} timeZone={timeZone} />
      <LegalHoldAlert person={data} timeZone={timeZone} />
      <PageTabs
        aria-label={t("personViews")}
        items={tabs}
        value={current}
        onChange={(next) => navigate({ search: { tab: next === "profile" ? undefined : next }, replace: true })}
      >
        <Tabs.Panel value="profile" pt="lg">
          <PersonProfile
            person={shown}
            canOpenPeople={meta.data.capabilities.canViewDirectory || meta.data.capabilities.canEdit}
          />
        </Tabs.Panel>
        <Tabs.Panel value="employments" pt="lg">
          <PersonEmployments
            person={shown}
            today={meta.data.today}
            structure={meta.data.structure}
            rehire={erased && data.capabilities.canEdit}
          />
        </Tabs.Panel>
        {absenceReadable && (
          <Tabs.Panel value="absence" pt="lg">
            {current === "absence" && (
              <PersonAbsence
                person={shown}
                today={meta.data.today}
                types={meta.data.absenceTypes}
                capabilities={meta.data.capabilities}
              />
            )}
          </Tabs.Panel>
        )}
        <Tabs.Panel value="competence" pt="lg">
          {current === "competence" && (
            <PersonCompetence
              person={shown}
              self={data.capabilities.self}
              today={meta.data.today}
              timeZone={timeZone}
            />
          )}
        </Tabs.Panel>
        {historyReadable && (
          <Tabs.Panel value="history" pt="lg">
            <PersonHistory
              page={(page) => personEventsQueryOptions(personId, page)}
              person={shown}
              structure={meta.data.structure}
            />
          </Tabs.Panel>
        )}
        {readsDocuments(data) && (
          <Tabs.Panel value="documents" pt="lg">
            {current === "documents" && (
              <PersonDocuments
                person={shown}
                today={meta.data.today}
                canSeeHealth={meta.data.capabilities.canSeeHealth}
              />
            )}
          </Tabs.Panel>
        )}
        {readsAccount && (
          <Tabs.Panel value="account" pt="lg">
            {current === "account" && <PersonAccount person={data} timeZone={timeZone} erased={erased} />}
          </Tabs.Panel>
        )}
      </PageTabs>
    </Stack>
  );
};
