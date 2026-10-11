import { ActionIcon, Anchor, Button, Card, Group, List, Modal, Select, SimpleGrid, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  nextOfKinQueryOptions,
  orgQueryOptions,
  rolesQueryOptions,
  type StaffNextOfKin,
  type StaffPerson,
  type StaffPersonRole,
  type StaffPersonRoleKind,
  type StaffPersonSummary,
  updateNextOfKin,
  updateRoles,
} from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup, Fields } from "../components/field-group";
import { SealedField } from "../components/sealed-field";
import "../i18n";
import { isRevisionConflict, listPath, mappedRefusals } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import {
  type NextOfKinFormValues,
  nextOfKinFormValuesOf,
  nextOfKinInputOf,
  nextOfKinValidation,
} from "../lib/person-form";
import { personLinkOptions } from "../lib/routes";
import { MAX_PERSON_ROLES, personRoleKinds } from "../lib/terms";
import { NextOfKinFields } from "./-next-of-kin-fields";
import { PersonEditModal } from "./-person-edit-modal";
import { PersonLinkGroup } from "./-person-link";

export interface PersonProfileProps {
  person: StaffPerson;
  /** Whether the caller reads the directory, so a manager up the chain can be opened. */
  canOpenPeople: boolean;
  /**
   * What the private group offers on My page — the person's own edit — in
   * place of HR's; the person page leaves it out.
   */
  privateActions?: ReactNode;
}

/**
 * The Profile tab (design §7): the person's field groups as the response
 * carries them (§5) — a group the server left out is not drawn, not even as
 * a heading — then the next of kin, the roles, the self-service link and the
 * place in the organisation. What may be changed follows the person's
 * `capabilities`.
 */
export const PersonProfile = ({ person, canOpenPeople, privateActions }: PersonProfileProps) => {
  const format = useStaffFormat();
  const t = format.t;
  const [editing, setEditing] = useState(false);
  // The national id is answered — on file or not — to exactly the private
  // group's readers (the person, staff:manage, staff:personal-data, §5), so
  // its presence says the private group is this reader's even when every
  // private field happens to be empty.
  const readsPrivate = person.nationalId !== undefined;
  const canEdit = person.capabilities.canEdit;
  const date = (value?: string) => (value ? format.date(value) : undefined);

  return (
    <Stack gap="lg">
      <FieldGroup
        title={t("groupWork")}
        actions={
          canEdit && (
            <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
              {t("editDetails")}
            </Button>
          )
        }
      >
        <Fields
          items={[
            [t("givenName"), person.givenName],
            [t("familyName"), person.familyName],
            [t("employeeNumber"), person.employeeNumber],
            [t("workEmail"), person.workEmail],
            [t("phoneWork"), person.phoneWork],
            [t("jobTitle"), person.title],
            [t("department"), person.department?.name],
            [t("businessUnit"), person.businessUnit?.name],
            [t("location"), person.location?.name],
            [t("manager"), person.manager?.displayName],
            [t("preferredLanguage"), t(person.preferredLanguage === "en" ? "languageEn" : "languageNb")],
            [t("workCountry"), person.workCountry],
          ]}
        />
      </FieldGroup>

      {readsPrivate && (
        <FieldGroup
          title={t("groupPrivate")}
          description={privateActions && t("askHrForTheRest")}
          actions={privateActions}
        >
          <Fields
            items={[
              [t("birthDate"), date(person.birthDate)],
              [t("gender"), person.gender && t(`gender_${person.gender}`)],
              [t("phoneMobile"), person.phoneMobile],
              [t("phonePrivate"), person.phonePrivate],
              [t("privateEmail"), person.privateEmail],
              [t("street"), person.street],
              [t("postalCode"), person.postalCode],
              [t("city"), person.city],
              [t("country"), person.country],
            ]}
          />
        </FieldGroup>
      )}

      {person.notes && (
        <FieldGroup title={t("hrNotes")}>
          <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
            {person.notes}
          </Text>
        </FieldGroup>
      )}

      {person.nationalId && (
        <FieldGroup title={t("groupIdentifiers")}>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <SealedField person={person} kind="nationalId" />
            <SealedField person={person} kind="bankAccount" />
          </SimpleGrid>
        </FieldGroup>
      )}

      {readsPrivate && <NextOfKinGroup person={person} />}
      <RolesGroup person={person} />
      {person.capabilities.canManageAccount ? (
        <PersonLinkGroup person={person} />
      ) : (
        person.user && (
          <FieldGroup title={t("selfService")} description={t("linkExplained")}>
            <Text size="sm">{t("linkedNotManaged", { name: person.user.displayName })}</Text>
          </FieldGroup>
        )
      )}
      <OrgGroup person={person} canOpenPeople={canOpenPeople} />

      {canEdit && <PersonEditModal person={person} opened={editing} onClose={() => setEditing(false)} />}
    </Stack>
  );
};

/** A person named as the work group names them: a link to their page for a reader who can open it. */
const PersonName = ({ person, linked }: { person: StaffPersonSummary; linked: boolean }) =>
  linked ? (
    <Anchor size="sm" renderRoot={(props) => <Link {...personLinkOptions(person.id)} {...props} />}>
      {person.displayName}
    </Anchor>
  ) : (
    <Text size="sm" component="span">
      {person.displayName}
    </Text>
  );

/**
 * The org view (`GET /people/{id}/org`): the managers up the chain, today,
 * and the direct reports. A report is always the reader's to open — they read
 * this person, and the person is that report's manager — but a manager up
 * the chain only for a reader of the directory.
 */
const OrgGroup = ({ person, canOpenPeople }: { person: StaffPerson; canOpenPeople: boolean }) => {
  const { t } = useI18n("staff");
  const { data } = useQuery(orgQueryOptions(person.id));
  if (!data) return null;
  const names = (people: StaffPersonSummary[], linked: boolean) =>
    people.length === 0 ? (
      t("nobody")
    ) : (
      <List size="sm" listStyleType="none" spacing={2}>
        {people.map((one) => (
          <List.Item key={one.id}>
            <PersonName person={one} linked={linked} />
          </List.Item>
        ))}
      </List>
    );
  return (
    <FieldGroup title={t("organisation")}>
      <Fields
        items={[
          [t("managers"), names(data.chain, canOpenPeople || person.capabilities.canEdit)],
          [t("directReports"), names(data.reports, true)],
        ]}
      />
    </FieldGroup>
  );
};

const NextOfKinGroup = ({ person }: { person: StaffPerson }) => {
  const { t } = useI18n("staff");
  const { data } = useQuery(nextOfKinQueryOptions(person.id));
  const [editing, setEditing] = useState(false);
  if (!data) return null;
  return (
    <FieldGroup
      title={t("nextOfKin")}
      actions={
        person.capabilities.canEdit && (
          <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
            {t("editNextOfKin")}
          </Button>
        )
      }
    >
      <NextOfKinList nextOfKin={data.nextOfKin} />
      {person.capabilities.canEdit && (
        <NextOfKinModal
          personId={person.id}
          nextOfKin={data.nextOfKin}
          revision={data.revision}
          opened={editing}
          onClose={() => setEditing(false)}
        />
      )}
    </FieldGroup>
  );
};

export const NextOfKinList = ({ nextOfKin }: { nextOfKin: StaffNextOfKin[] }) => {
  const { t } = useI18n("staff");
  if (nextOfKin.length === 0)
    return (
      <Text size="sm" c="dimmed">
        {t("noNextOfKin")}
      </Text>
    );
  return (
    <SimpleGrid cols={{ base: 1, sm: 2 }}>
      {nextOfKin.map((kin, index) => (
        <Stack key={index} gap={0}>
          <Text size="sm" fw={500}>
            {kin.name}
          </Text>
          <Text size="xs" c="dimmed">
            {t(`relationship_${kin.relationship}`)}
          </Text>
          {kin.phone && <Text size="sm">{kin.phone}</Text>}
          {kin.note && (
            <Text size="sm" c="dimmed">
              {kin.note}
            </Text>
          )}
        </Stack>
      ))}
    </SimpleGrid>
  );
};

/**
 * Says a replace lost to somebody else's save in the reader's words, and
 * reloads: the server's sentence names revision numbers, which mean nothing
 * to the person.
 */
const useRevisionConflict = () => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  return async (error: unknown): Promise<boolean> => {
    if (!isRevisionConflict(error)) return false;
    notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
    await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    return true;
  };
};

const NextOfKinModal = ({
  personId,
  nextOfKin,
  revision,
  opened,
  onClose,
}: {
  personId: number;
  nextOfKin: StaffNextOfKin[];
  revision: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={opened} onClose={onClose} title={t("editNextOfKin")} size="lg" fullScreen={phone}>
      {opened && <NextOfKinForm personId={personId} nextOfKin={nextOfKin} revision={revision} onDone={onClose} />}
    </Modal>
  );
};

const NextOfKinForm = ({
  personId,
  nextOfKin,
  revision,
  onDone,
}: {
  personId: number;
  nextOfKin: StaffNextOfKin[];
  revision: number;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const conflict = useRevisionConflict();
  const form = useForm<{ nextOfKin: NextOfKinFormValues[] }>({
    initialValues: { nextOfKin: nextOfKin.map(nextOfKinFormValuesOf) },
    validate: { nextOfKin: nextOfKinValidation(t) },
  });
  const save = useMutation({
    // The revision the list was read at, so somebody else's save in between is a conflict, not lost.
    mutationFn: (values: { nextOfKin: NextOfKinFormValues[] }) =>
      updateNextOfKin(personId, nextOfKinInputOf(values.nextOfKin), revision),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("nextOfKinSaved"), message: "" });
      // The person's revision moved: every read of the person follows it.
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people", "detail", personId] });
      onDone();
    },
    onError: async (error) => {
      if (await conflict(error)) return onDone();
      const { fields, rest } = mappedRefusals(error, (field) =>
        field.startsWith("nextOfKin") ? listPath(field) : undefined,
      );
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
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

const RolesGroup = ({ person }: { person: StaffPerson }) => {
  const format = useStaffFormat();
  const t = format.t;
  const { data } = useQuery(rolesQueryOptions(person.id));
  const [editing, setEditing] = useState(false);
  if (!data) return null;
  const period = (role: StaffPersonRole) =>
    role.fromDate && role.toDate
      ? t("rolePeriod", { from: format.date(role.fromDate), to: format.date(role.toDate) })
      : role.fromDate
        ? t("roleFrom", { date: format.date(role.fromDate) })
        : role.toDate
          ? t("roleUntil", { date: format.date(role.toDate) })
          : undefined;
  return (
    <FieldGroup
      title={t("roles")}
      actions={
        person.capabilities.canEdit && (
          <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
            {t("editRoles")}
          </Button>
        )
      }
    >
      {data.roles.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("noRoles")}
        </Text>
      ) : (
        <List size="sm" listStyleType="none" spacing={4}>
          {data.roles.map((role, index) => (
            <List.Item key={index}>
              <Text size="sm" component="span">
                {t(`role_${role.role}`)}
              </Text>
              {period(role) && (
                <Text size="xs" c="dimmed" component="span" ml="xs">
                  {period(role)}
                </Text>
              )}
            </List.Item>
          ))}
        </List>
      )}
      {person.capabilities.canEdit && (
        <RolesModal
          personId={person.id}
          roles={data.roles}
          revision={data.revision}
          opened={editing}
          onClose={() => setEditing(false)}
        />
      )}
    </FieldGroup>
  );
};

interface RoleFormValues {
  role: StaffPersonRoleKind | null;
  fromDate: string | null;
  toDate: string | null;
}

const RolesModal = ({
  personId,
  roles,
  revision,
  opened,
  onClose,
}: {
  personId: number;
  roles: StaffPersonRole[];
  revision: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={opened} onClose={onClose} title={t("editRoles")} size="lg" fullScreen={phone}>
      {opened && <RolesForm personId={personId} roles={roles} revision={revision} onDone={onClose} />}
    </Modal>
  );
};

const RolesForm = ({
  personId,
  roles,
  revision,
  onDone,
}: {
  personId: number;
  roles: StaffPersonRole[];
  revision: number;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const conflict = useRevisionConflict();
  const form = useForm<{ roles: RoleFormValues[] }>({
    initialValues: {
      roles: roles.map((role) => ({ role: role.role, fromDate: role.fromDate ?? null, toDate: role.toDate ?? null })),
    },
    validate: {
      roles: {
        role: (value) => (value ? null : t("roleRequired")),
        toDate: (value, values, path) => {
          const from = values.roles[Number(path.split(".")[1])]?.fromDate;
          return value && from && value < from ? t("toBeforeFrom") : null;
        },
      },
    },
  });
  const save = useMutation({
    mutationFn: (values: { roles: RoleFormValues[] }) =>
      updateRoles(
        personId,
        values.roles.map((row) => ({
          role: row.role as StaffPersonRoleKind,
          ...(row.fromDate ? { fromDate: row.fromDate } : {}),
          ...(row.toDate ? { toDate: row.toDate } : {}),
        })),
        revision,
      ),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("rolesSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people", "detail", personId] });
      onDone();
    },
    onError: async (error) => {
      if (await conflict(error)) return onDone();
      const { fields, rest } = mappedRefusals(error, (field) =>
        field.startsWith("roles") ? listPath(field) : undefined,
      );
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });
  const rows = form.values.roles;
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {rows.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noRoles")}
          </Text>
        )}
        {rows.map((_, index) => (
          <Card key={index} withBorder padding="sm" radius="sm">
            <Group align="flex-start" wrap="nowrap" gap="xs">
              <SimpleGrid cols={{ base: 1, sm: 3 }} style={{ flex: 1 }}>
                <Select
                  label={t("role")}
                  withAsterisk
                  allowDeselect={false}
                  data={personRoleKinds.map((value) => ({ value, label: t(`role_${value}`) }))}
                  {...form.getInputProps(`roles.${index}.role`)}
                />
                <DateInput
                  label={t("fromDate")}
                  valueFormat={t("dateInputFormat")}
                  clearable
                  {...form.getInputProps(`roles.${index}.fromDate`)}
                />
                <DateInput
                  label={t("toDate")}
                  valueFormat={t("dateInputFormat")}
                  clearable
                  {...form.getInputProps(`roles.${index}.toDate`)}
                />
              </SimpleGrid>
              <ActionIcon
                variant="subtle"
                color="red"
                mt={28}
                aria-label={t("removeRole", { n: index + 1 })}
                onClick={() => form.removeListItem("roles", index)}
              >
                <IconTrash size={16} />
              </ActionIcon>
            </Group>
          </Card>
        ))}
        {rows.length < MAX_PERSON_ROLES && (
          <Button
            variant="light"
            leftSection={<IconPlus size={16} />}
            style={{ alignSelf: "flex-start" }}
            onClick={() => form.insertListItem("roles", { role: null, fromDate: null, toDate: null })}
          >
            {t("addRole")}
          </Button>
        )}
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
