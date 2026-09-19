import { ActionIcon, Alert, Badge, Box, Button, Card, Group, Modal, Select, Stack, Table, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useId, useState } from "react";
import {
  assignableUsersQueryOptions,
  type ProjectRole,
  type ProjectRoleAssignment,
  projectRolesQueryOptions,
  removeProjectRole,
  setProjectRole,
} from "../api/people";
import { projectQueryOptions } from "../api/projects";
import "../i18n";
import { isProjectRole, projectRoleLabelKey, projectRoles } from "../lib/roles";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** The role options every picker on this tab offers, in the order roles widen. */
const useRoleOptions = () => {
  const { t } = useI18n("projects");
  return projectRoles.map((role) => ({ value: role, label: t(projectRoleLabelKey(role)) }));
};

/**
 * Who is on the project and in what role (design §8.2). Everyone who can see
 * the project sees the table; only a manager gets the three actions, which is
 * what `capabilities.canManage` already answered for us.
 */
export const ProjectPeople = ({ projectId }: { projectId: number }) => {
  const { t } = useI18n("projects");
  const project = useQuery(projectQueryOptions(projectId));
  const { data: assignments, isPending, isError, error } = useQuery(projectRolesQueryOptions(projectId));
  const [addOpen, setAddOpen] = useState(false);
  const headingId = useId();

  // The project answers who may act here, so the tab waits for it and says so
  // when it fails, rather than quietly rendering a read-only table.
  if (project.isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")} mt="md">
        {project.error.message}
      </Alert>
    );
  }
  if (project.isPending)
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );

  const canManage = project.data.capabilities.canManage;
  const people = assignments ?? [];

  return (
    <Card withBorder padding="lg" radius="md" mt="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Text fw={600} component="h3" id={headingId}>
            {t("projectPeople")}
          </Text>
          {canManage && (
            <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setAddOpen(true)}>
              {t("addPerson")}
            </Button>
          )}
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPeople")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={48} />}

        {assignments && people.length === 0 && <EmptyState title={t("noPeopleAssigned")} size="sm" />}

        {people.length > 0 && (
          <Table.ScrollContainer minWidth={520}>
            <Table striped highlightOnHover aria-labelledby={headingId}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("role")}</Table.Th>
                  {canManage && <Table.Th>{t("actions")}</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {people.map((person) => (
                  <PersonRow key={person.userId} projectId={projectId} person={person} canManage={canManage} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>

      <AddPersonModal projectId={projectId} opened={addOpen} onClose={() => setAddOpen(false)} />
    </Card>
  );
};

/**
 * Both writes on a row go through one mutation: changing a role and adding
 * somebody are the same call, so the page tells them apart only by the
 * notification it shows afterwards.
 */
const useRoleMutation = (projectId: number, title: string) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: ProjectRole }) => setProjectRole(projectId, userId, role),
    onSuccess: (assignment) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({ color: "teal", title, message: assignment.displayName });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotUpdatePerson"), message: error.message });
    },
  });
};

const PersonRow = ({
  projectId,
  person,
  canManage,
}: {
  projectId: number;
  person: ProjectRoleAssignment;
  canManage: boolean;
}) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const roleOptions = useRoleOptions();
  const changeRole = useRoleMutation(projectId, t("roleChanged"));

  const remove = useMutation({
    mutationFn: () => removeProjectRole(projectId, person.userId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({ color: "teal", title: t("personRemoved"), message: person.displayName });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotUpdatePerson"), message: error.message });
    },
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("removePersonTitle", { name: person.displayName }),
      children: <Text size="sm">{t("removePersonWarning")}</Text>,
      labels: { confirm: t("removePerson"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  return (
    <Table.Tr>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          <Text size="sm">{person.displayName}</Text>
          {!person.active && (
            <Badge size="xs" variant="light" color="gray">
              {t("inactiveUser")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        {canManage ? (
          <Select
            aria-label={t("changeRoleFor", { name: person.displayName })}
            w={160}
            size="xs"
            allowDeselect={false}
            data={roleOptions}
            value={person.role}
            disabled={changeRole.isPending}
            onChange={(value) =>
              value && isProjectRole(value) && changeRole.mutate({ userId: person.userId, role: value })
            }
          />
        ) : (
          <Badge variant="light">{t(projectRoleLabelKey(person.role))}</Badge>
        )}
      </Table.Td>
      {canManage && (
        <Table.Td>
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={t("removePersonFor", { name: person.displayName })}
            loading={remove.isPending}
            onClick={confirmRemove}
          >
            <IconTrash size={16} />
          </ActionIcon>
        </Table.Td>
      )}
    </Table.Tr>
  );
};

/** Adds somebody the API says may be added: active users not already on the project. */
const AddPersonModal = ({
  projectId,
  opened,
  onClose,
}: {
  projectId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("projects");
  const roleOptions = useRoleOptions();
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const [userId, setUserId] = useState<string | null>(null);
  const [role, setRole] = useState<ProjectRole>("member");

  const { data: candidates } = useQuery({
    ...assignableUsersQueryOptions(projectId, debouncedSearch),
    enabled: opened,
  });
  const add = useRoleMutation(projectId, t("personAdded"));

  const close = () => {
    setUserId(null);
    setRole("member");
    setSearch("");
    onClose();
  };

  const submit = () => {
    if (userId) add.mutate({ userId, role }, { onSuccess: close });
  };

  return (
    <Modal opened={opened} onClose={close} title={t("addPerson")} centered>
      <Stack>
        <Select
          label={t("person")}
          placeholder={t("searchUsers")}
          searchable
          withAsterisk
          // The API has already filtered; filtering again would hide matches
          // whose display name does not contain the term literally.
          filter={({ options }) => options}
          onSearchChange={setSearch}
          nothingFoundMessage={t("noAssignableUsers")}
          data={(candidates ?? []).map((person) => ({ value: person.userId, label: person.displayName }))}
          value={userId}
          onChange={setUserId}
        />
        <Select
          label={t("role")}
          allowDeselect={false}
          data={roleOptions}
          value={role}
          onChange={(value) => value && isProjectRole(value) && setRole(value)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={close}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} loading={add.isPending} disabled={!userId}>
            {t("addPerson")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
