import { Alert, Badge, Button, Group, Modal, SimpleGrid, Stack, Tabs, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload, IconPencil, IconUserQuestion } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { downloadMyData, myDocumentsQueryOptions, saveFile } from "../api/documents";
import { myGroupsQueryOptions } from "../api/groups";
import { meQueryOptions, myEventsQueryOptions, updateMe } from "../api/me";
import { staffMetaQueryOptions } from "../api/meta";
import { nextOfKinQueryOptions, type StaffNextOfKin, type StaffPerson } from "../api/people";
import { NotFoundError, STAFF_QUERY_KEY } from "../api/request";
import { DocumentList } from "../components/document-list";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { isRevisionConflict, listPath, mappedRefusals } from "../lib/errors";
import {
  type MyDetailsFormValues,
  meInputOf,
  myDetailsFields,
  myDetailsFormValuesOf,
  myDetailsValidation,
} from "../lib/person-form";
import type { MyPageSearch, MyPageTab } from "../lib/search";
import { MyAbsence } from "./-my-absence";
import { MyTeam } from "./-my-team";
import { NextOfKinFields } from "./-next-of-kin-fields";
import { PersonAccount } from "./-person-account";
import { PersonEmployments } from "./-person-employments";
import { PersonHistory } from "./-person-history";
import { PersonProfile } from "./-person-profile";

/**
 * My page (design §7, S17, S28): the caller's own record through their
 * self-service link, read as themselves — whatever else they hold — with the
 * private group theirs to edit (`PUT /me`), their employments and terms with
 * the pay (it is their contract), their history, and their documents with
 * the copy of their data they may take (design S16), their absence with the
 * request sheet, their account read-only (delivery C), and — for a caller
 * who manages anyone — My team. A caller HR has not
 * linked yet gets a plain explanation, not an error: `no_person` is the
 * expected state of somebody new. Laid out for a phone first.
 */
export const MyPage = () => {
  const { t } = useI18n("staff");
  const { tab = "profile", create } = useSearch({ strict: false }) as MyPageSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(staffMetaQueryOptions());
  const me = useQuery(meQueryOptions());
  const [editing, setEditing] = useState(false);

  const header = <PageHeader title={t("navMyPage")} description={t("myPageDescription")} />;
  // `/me` has no id to be unknown: its one 404 is `no_person`.
  if (me.error instanceof NotFoundError)
    return (
      <Stack gap="lg">
        {header}
        <EmptyState icon={IconUserQuestion} title={t("noPersonTitle")} description={t("noPersonExplained")} />
      </Stack>
    );
  if (me.isError)
    return (
      <Stack gap="lg">
        {header}
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMyPage")}>
          {me.error.message}
        </Alert>
      </Stack>
    );
  if (!me.data || !meta.data) return <ContentSkeleton rows={6} rowHeight={44} />;
  const person = me.data;
  const managesAnyone = meta.data.capabilities.managesAnyone;
  // My team is there only for a caller who manages anyone (plan reading 6).
  const current: MyPageTab = tab === "team" && !managesAnyone ? "profile" : tab;

  return (
    <Stack gap="lg">
      {header}
      <PageTabs
        aria-label={t("personViews")}
        items={[
          { value: "profile", label: t("tab_profile") },
          { value: "employments", label: t("tab_employments") },
          { value: "history", label: t("tab_history") },
          { value: "documents", label: t("tab_documents") },
          { value: "absence", label: t("tab_absence") },
          { value: "account", label: t("tab_account") },
          ...(managesAnyone ? [{ value: "team", label: t("tab_team") }] : []),
        ]}
        value={current}
        onChange={(next) => navigate({ search: { tab: next === "profile" ? undefined : next }, replace: true })}
      >
        <Tabs.Panel value="profile" pt="lg">
          <Stack gap="lg">
            <PersonProfile
              person={person}
              canOpenPeople={meta.data.capabilities.canViewDirectory || meta.data.capabilities.canEdit}
              privateActions={
                <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
                  {t("editMyDetails")}
                </Button>
              }
            />
            {current === "profile" && <MyGroups />}
          </Stack>
        </Tabs.Panel>
        <Tabs.Panel value="employments" pt="lg">
          <PersonEmployments person={person} today={meta.data.today} structure={meta.data.structure} asSelf />
        </Tabs.Panel>
        <Tabs.Panel value="history" pt="lg">
          <PersonHistory page={myEventsQueryOptions} person={person} structure={meta.data.structure} />
        </Tabs.Panel>
        <Tabs.Panel value="documents" pt="lg">
          {current === "documents" && <MyDocuments today={meta.data.today} />}
        </Tabs.Panel>
        <Tabs.Panel value="absence" pt="lg">
          {current === "absence" && (
            <MyAbsence
              // The spotlight's Request absence may arrive while the tab is
              // open: a new `create` mounts it afresh, the sheet seeded open.
              key={create === "absence" ? "requesting" : "plain"}
              today={meta.data.today}
              types={meta.data.absenceTypes}
              requestOnArrival={create === "absence"}
              onArrivalDone={() => navigate({ search: { tab: "absence" }, replace: true })}
            />
          )}
        </Tabs.Panel>
        <Tabs.Panel value="account" pt="lg">
          {/* My own account, read as myself: read-only, whatever else I hold. */}
          {current === "account" && <PersonAccount person={person} timeZone={meta.data.timeZone} />}
        </Tabs.Panel>
        {managesAnyone && (
          <Tabs.Panel value="team" pt="lg">
            {current === "team" && <MyTeam person={person} today={meta.data.today} types={meta.data.absenceTypes} />}
          </Tabs.Panel>
        )}
      </PageTabs>
      <MyDetailsModal person={person} opened={editing} onClose={() => setEditing(false)} />
    </Stack>
  );
};

/** The groups I am in, by name (`GET /me/groups`, Entra design D9). */
const MyGroups = () => {
  const { t } = useI18n("staff");
  const groups = useQuery(myGroupsQueryOptions());
  return (
    <FieldGroup title={t("myGroups")} description={t("myGroupsDescription")}>
      {groups.isError && (
        <Text size="sm" c="red">
          {t("failedToLoadMyGroups")}
        </Text>
      )}
      {groups.isPending && <ContentSkeleton rows={1} rowHeight={28} />}
      {groups.data?.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("myNoGroups")}
        </Text>
      )}
      {groups.data && groups.data.length > 0 && (
        <Group gap="xs" component="ul" aria-label={t("myGroups")} style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {groups.data.map((group) => (
            <Badge key={group.id} component="li" variant="light" size="lg" tt="none">
              {group.name}
            </Badge>
          ))}
        </Group>
      )}
    </FieldGroup>
  );
};

/**
 * My own documents (`GET /me/documents`), health documents included, to
 * download — HR files them — and my own copy of my data (`GET /me/export`),
 * saved under the name the server gives it.
 */
const MyDocuments = ({ today }: { today: string }) => {
  const { t } = useI18n("staff");
  const documents = useQuery(myDocumentsQueryOptions());
  const myData = useMutation({
    mutationFn: downloadMyData,
    onSuccess: saveFile,
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDownloadMyData"), message: error.message }),
  });
  return (
    <Stack gap="md">
      {documents.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadDocuments")}>
          {documents.error.message}
        </Alert>
      )}
      {documents.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
      {documents.data?.length === 0 && <EmptyState title={t("noDocuments")} />}
      {documents.data && documents.data.length > 0 && <DocumentList documents={documents.data} today={today} />}
      <FieldGroup
        title={t("myData")}
        description={t("myDataExplained")}
        actions={
          <Button
            variant="light"
            leftSection={<IconDownload size={16} />}
            loading={myData.isPending}
            onClick={() => myData.mutate()}
          >
            {t("downloadMyData")}
          </Button>
        }
      >
        {null}
      </FieldGroup>
    </Stack>
  );
};

/** On a phone the form is a sheet that takes the whole screen. */
const MyDetailsModal = ({ person, opened, onClose }: { person: StaffPerson; opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={opened} onClose={onClose} title={t("editMyDetails")} size="lg" fullScreen={phone}>
      {opened && <FreshMyDetails personId={person.id} onDone={onClose} />}
    </Modal>
  );
};

/**
 * The form opens on the record as it is now: `/me` and the next of kin are
 * read again when it opens, and the form waits for both, so the revision it
 * sends is the one the fields it shows were read at — a change HR made since
 * the page loaded is in the form, and one HR makes while it is open is a
 * 409, never overwritten.
 */
const FreshMyDetails = ({ personId, onDone }: { personId: number; onDone: () => void }) => {
  const me = useQuery({ ...meQueryOptions(), refetchOnMount: "always" });
  const nextOfKin = useQuery({ ...nextOfKinQueryOptions(personId), refetchOnMount: "always" });
  if (!me.isFetchedAfterMount || !nextOfKin.isFetchedAfterMount || !me.data || !nextOfKin.data)
    return <ContentSkeleton rows={4} rowHeight={44} />;
  return (
    <MyDetailsForm person={me.data} nextOfKin={nextOfKin.data.nextOfKin} revision={me.data.revision} onDone={onDone} />
  );
};

const MyDetailsForm = ({
  person,
  nextOfKin,
  revision,
  onDone,
}: {
  person: StaffPerson;
  nextOfKin: StaffNextOfKin[];
  /** The person's revision, read with the fields the form shows — the one `PUT /me` sends. */
  revision: number;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const form = useForm<MyDetailsFormValues>({
    initialValues: myDetailsFormValuesOf(person, nextOfKin),
    validate: myDetailsValidation(t),
  });

  const save = useMutation({
    mutationFn: (values: MyDetailsFormValues) => updateMe(meInputOf(values), revision),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("myDetailsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        // HR saved the record meanwhile: close on what is there now.
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      const { fields, rest } = mappedRefusals(error, (field) =>
        field.startsWith("nextOfKin") || (myDetailsFields as readonly string[]).includes(field)
          ? listPath(field)
          : undefined,
      );
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("askHrForTheRest")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("phoneMobile")} type="tel" {...form.getInputProps("phoneMobile")} />
          <TextInput label={t("phonePrivate")} type="tel" {...form.getInputProps("phonePrivate")} />
          <TextInput label={t("privateEmail")} type="email" {...form.getInputProps("privateEmail")} />
          <TextInput label={t("street")} {...form.getInputProps("street")} />
          <TextInput label={t("postalCode")} {...form.getInputProps("postalCode")} />
          <TextInput label={t("city")} {...form.getInputProps("city")} />
          <TextInput
            label={t("country")}
            description={t("homeCountryDescription")}
            {...form.getInputProps("country")}
          />
        </SimpleGrid>
        <Text size="sm" fw={500}>
          {t("nextOfKin")}
        </Text>
        <NextOfKinFields form={form} />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
