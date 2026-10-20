import { Anchor, Button, Group, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type StaffGroup, setGroupMembers, setGroupOwners } from "../api/groups";
import { STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup } from "../components/field-group";
import { PeoplePicker, type PickedPerson } from "../components/people-picker";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { personLinkOptions } from "../lib/routes";

const pickedOf = (rows: { personId: number; displayName: string; employeeNumber: string }[] = []): PickedPerson[] =>
  rows.map((row) => ({ id: row.personId, displayName: row.displayName, employeeNumber: row.employeeNumber }));

const sameIds = (a: PickedPerson[], b: PickedPerson[]) =>
  a.length === b.length && a.every((person, index) => person.id === b[index]?.id);

/**
 * The picker and *Save* every list here shares: the people as the group
 * holds them, a replace of exactly the ones picked, the group's answer put
 * in the cache, and the server's sentence when it refuses (a person erased
 * meanwhile, `group_derived`, `group_archived`).
 */
const usePeopleWrite = (write: (personIds: number[]) => Promise<StaffGroup>) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (people: PickedPerson[]) => write(people.map((person) => person.id)),
    onSuccess: async (saved) => {
      notifications.show({ color: "teal", title: t("rowSaved", { name: saved.name }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error) }),
  });
};

/**
 * The members (Entra design D9): a listed group's are HR's — a multi-person
 * picker and *Save*, each who joins or leaves an event on the person; a
 * derived group's are its rule's, listed read-only and materialised by the
 * accounts worker on the business day.
 */
export const GroupMembers = ({ group }: { group: StaffGroup }) => {
  const { t, date } = useStaffFormat();
  const members = group.members ?? [];
  const editable = group.kind === "listed" && !group.archivedAt;
  const [picked, setPicked] = useState(() => pickedOf(members));
  // The group read again (a save, another's change) is the picker's new start.
  const [seen, setSeen] = useState(group.members);
  if (seen !== group.members) {
    setSeen(group.members);
    setPicked(pickedOf(group.members));
  }
  const save = usePeopleWrite((ids) => setGroupMembers(group.id, ids));

  return (
    <FieldGroup
      title={t("groupMembers")}
      description={group.kind === "derived" ? t("groupMembersDerived") : t("groupMembersListed")}
    >
      {editable && (
        <Stack gap="xs">
          <PeoplePicker label={t("groupMembersPick")} value={picked} onChange={setPicked} />
          <Group justify="flex-end">
            <Button
              loading={save.isPending}
              disabled={sameIds(picked, pickedOf(members))}
              onClick={() => save.mutate(picked)}
            >
              {t("saveMembers")}
            </Button>
          </Group>
        </Stack>
      )}
      {members.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("groupNoMembers")}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={480}>
          <Table aria-label={t("groupMembers")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("name")}</Table.Th>
                <Table.Th>{t("employeeNumber")}</Table.Th>
                <Table.Th>{t("groupSource")}</Table.Th>
                <Table.Th>{t("groupSince")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {members.map((member) => (
                <Table.Tr key={member.personId}>
                  <Table.Td>
                    <Anchor
                      size="sm"
                      renderRoot={(props) => <Link {...personLinkOptions(member.personId)} {...props} />}
                    >
                      {member.displayName}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>{member.employeeNumber}</Table.Td>
                  <Table.Td>{t(`groupSource_${member.source}`)}</Table.Td>
                  <Table.Td>{date(member.since)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </FieldGroup>
  );
};

/** The owners: the people a provider names as the group's owners in its directory, beside itself. */
export const GroupOwners = ({ group }: { group: StaffGroup }) => {
  const { t } = useI18n("staff");
  const owners = group.owners ?? [];
  const [picked, setPicked] = useState(() => pickedOf(owners));
  const [seen, setSeen] = useState(group.owners);
  if (seen !== group.owners) {
    setSeen(group.owners);
    setPicked(pickedOf(group.owners));
  }
  const save = usePeopleWrite((ids) => setGroupOwners(group.id, ids));

  return (
    <FieldGroup title={t("groupOwners")} description={t("groupOwnersExplained")}>
      {group.archivedAt ? (
        <Text size="sm">
          {owners.length === 0
            ? t("groupNoOwners")
            : owners.map((owner) => `${owner.displayName} (${owner.employeeNumber})`).join(", ")}
        </Text>
      ) : (
        <Stack gap="xs">
          <PeoplePicker label={t("groupOwnersPick")} value={picked} onChange={setPicked} />
          <Group justify="flex-end">
            <Button
              loading={save.isPending}
              disabled={sameIds(picked, pickedOf(owners))}
              onClick={() => save.mutate(picked)}
            >
              {t("saveOwners")}
            </Button>
          </Group>
        </Stack>
      )}
    </FieldGroup>
  );
};
