import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Divider,
  Group,
  Loader,
  Modal,
  MultiSelect,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconLock, IconPlus, IconShield, IconTrash, IconUsers } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";
import {
  type AuthorizationRole,
  assignUserRoles,
  createAuthorizationRole,
  createDelegation,
  type DelegationInput,
  deleteAuthorizationRole,
  getAuthorizationMe,
  getUserAccess,
  listAuthorizationRoles,
  listAuthorizationUsers,
  listDelegations,
  listPermissionCatalog,
  type RoleInput,
  revokeDelegation,
  updateAuthorizationRole,
} from "../../api/authorization";

const errorText = (error: unknown) => (error instanceof Error ? error.message : "The request could not be completed.");

const RolesPage = () => {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe });
  const manageable = me.data?.canManageAuthorization === true;
  const scope = me.data?.administrationScope;
  const isOwner = scope?.isOwner === true;
  const scopes = scope?.delegationScopes ?? [];
  const catalog = useQuery({
    queryKey: ["authorization", "catalog"],
    queryFn: listPermissionCatalog,
    enabled: manageable,
  });
  const roles = useQuery({
    queryKey: ["authorization", "roles"],
    queryFn: listAuthorizationRoles,
    enabled: manageable,
  });
  const users = useQuery({
    queryKey: ["authorization", "users"],
    queryFn: listAuthorizationUsers,
    enabled: manageable,
  });
  const delegations = useQuery({
    queryKey: ["authorization", "delegations"],
    queryFn: listDelegations,
    enabled: manageable && isOwner,
  });
  const [selectedRole, setSelectedRole] = useState<AuthorizationRole | null>(null);
  const [selectedUser, setSelectedUser] = useState<string | null>(null);
  const [selectedScopeId, setSelectedScopeId] = useState<string | null>(null);
  const [assignmentScopeId, setAssignmentScopeId] = useState<string | null>(null);
  const [roleModal, { open: openRole, close: closeRole }] = useDisclosure(false);
  const [delegateModal, { open: openDelegate, close: closeDelegate }] = useDisclosure(false);
  const roleForm = useForm<RoleInput>({
    initialValues: { name: "", displayName: "", description: "", permissionKeys: [] },
    validate: {
      name: (value) => (/^[a-z][a-z0-9-]+$/.test(value) ? null : "Use lowercase letters, numbers, and hyphens"),
      displayName: (value) => (value.trim() ? null : "Enter a display name"),
      description: (value) => (value.trim() ? null : "Enter a description"),
      permissionKeys: (value) => (value.length ? null : "Select at least one permission"),
    },
  });
  const delegateForm = useForm<DelegationInput>({
    initialValues: {
      granteeUserId: "",
      expiresAt: null,
      permissionKeys: [],
      stewardedRoleIds: [],
      canCreateRoles: false,
    },
  });
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["authorization"] });
  const mutationError = (title: string) => (error: unknown) =>
    notifications.show({ color: "red", title, message: errorText(error) });
  const scopeForCreation = scopes.find((item) => item.id === selectedScopeId && item.canCreateRoles);
  const stewardedRoleIds = new Set(scopes.flatMap((item) => item.stewardedRoleIds));
  const allRoles = roles.data ?? [];
  const customRoles = allRoles.filter((role) => !role.isSystem && !role.isBuiltIn);
  const assignmentScope = scopes.find((item) => item.id === assignmentScopeId && item.assignableRoleIds.length > 0);
  const assignableRoleIds = new Set(
    isOwner ? customRoles.map((role) => role.id) : (assignmentScope?.assignableRoleIds ?? []),
  );
  const displayRoles = allRoles.filter(
    (role) => role.isSystem || role.isBuiltIn || isOwner || stewardedRoleIds.has(role.id),
  );
  const editableRole = (role: AuthorizationRole) => isOwner || stewardedRoleIds.has(role.id);
  const assignableRoles = isOwner ? customRoles : customRoles.filter((role) => assignableRoleIds.has(role.id));
  const qualifyingEditScopes = selectedRole
    ? scopes.filter((item) => item.stewardedRoleIds.includes(selectedRole.id))
    : [];
  const coveringEditScopes = selectedRole
    ? qualifyingEditScopes.filter((item) =>
        selectedRole.permissions.every((key) => item.grantablePermissionKeys.includes(key)),
      )
    : [];
  const selectedEditScope = selectedRole ? coveringEditScopes.find((item) => item.id === selectedScopeId) : undefined;
  const allowedPermissionKeys = new Set(
    isOwner
      ? (catalog.data ?? []).map((item) => item.key)
      : selectedRole
        ? (selectedEditScope?.grantablePermissionKeys ?? [])
        : (scopeForCreation?.grantablePermissionKeys ?? []),
  );
  const groupedPermissions = new Map<string, typeof catalog.data>();
  for (const permission of (catalog.data ?? []).filter((item) => allowedPermissionKeys.has(item.key))) {
    const key = `${permission.module} · ${permission.category}`;
    groupedPermissions.set(key, [...(groupedPermissions.get(key) ?? []), permission]);
  }
  const saveRole = useMutation({
    mutationFn: (input: RoleInput) =>
      selectedRole
        ? updateAuthorizationRole(selectedRole.id, { ...input, concurrencyStamp: selectedRole.version })
        : createAuthorizationRole(input),
    onSuccess: () => {
      closeRole();
      invalidate();
      notifications.show({
        color: "teal",
        title: selectedRole ? "Role updated" : "Role created",
        message: "The role was saved successfully.",
      });
    },
    onError: mutationError("Role could not be saved"),
  });
  const removeRole = useMutation({
    mutationFn: (role: AuthorizationRole) => deleteAuthorizationRole(role.id, role.version),
    onSuccess: invalidate,
    onError: mutationError("Role could not be deleted"),
  });
  const assignment = useMutation({
    mutationFn: ({ id, roleIds, version }: { id: string; roleIds: string[]; version: string }) =>
      assignUserRoles(id, {
        roleIds,
        concurrencyStamp: version,
        ...(!isOwner && assignmentScope ? { delegationId: assignmentScope.id } : {}),
      }),
    onSuccess: () => {
      invalidate();
      notifications.show({
        color: "teal",
        title: "Roles assigned",
        message: "Roles outside your delegated scope are kept.",
      });
    },
    onError: mutationError("Roles could not be assigned"),
  });
  const saveDelegation = useMutation({
    mutationFn: createDelegation,
    onSuccess: () => {
      closeDelegate();
      invalidate();
      notifications.show({
        color: "teal",
        title: "Delegation granted",
        message: "This grants administration only, not data access.",
      });
    },
    onError: mutationError("Delegation could not be granted"),
  });
  const revoke = useMutation({
    mutationFn: (item: { id: string; version: string }) => revokeDelegation(item.id, item.version),
    onSuccess: invalidate,
    onError: mutationError("Delegation could not be revoked"),
  });
  const access = useQuery({
    queryKey: ["authorization", "user", selectedUser],
    queryFn: () => {
      if (!selectedUser) throw new Error("Select a user before loading access.");
      return getUserAccess(selectedUser);
    },
    enabled: !!selectedUser && manageable,
  });
  const openRoleEditor = (role?: AuthorizationRole) => {
    setSelectedRole(role ?? null);
    const qualifyingScopes = role ? scopes.filter((item) => item.stewardedRoleIds.includes(role.id)) : [];
    const defaultScope = role
      ? qualifyingScopes.find((item) => role.permissions.every((key) => item.grantablePermissionKeys.includes(key)))
      : undefined;
    setSelectedScopeId(role && !isOwner ? (defaultScope?.id ?? null) : null);
    roleForm.setValues(
      role
        ? {
            name: role.name,
            displayName: role.displayName,
            description: role.description,
            permissionKeys: role.permissions,
          }
        : { name: "", displayName: "", description: "", permissionKeys: [] },
    );
    openRole();
  };
  if (me.isPending) return <Loader />;
  if (me.isError)
    return (
      <Alert color="red" title="Authorization administration unavailable">
        {errorText(me.error)}
      </Alert>
    );
  if (!manageable)
    return (
      <Alert color="yellow" title="Access not available">
        Your account cannot manage roles and access.
      </Alert>
    );
  const canCreateRole = isOwner || scopes.some((item) => item.canCreateRoles);
  const canEditRole = (role: AuthorizationRole) => isOwner || coveringScopesFor(role).length > 0;
  const coveringScopesFor = (role: AuthorizationRole) =>
    scopes.filter(
      (item) =>
        item.stewardedRoleIds.includes(role.id) &&
        role.permissions.every((key) => item.grantablePermissionKeys.includes(key)),
    );
  return (
    <Stack maw={1180} mx="auto" gap="xl">
      <Group justify="space-between" align="flex-end">
        <div>
          <Group gap="sm">
            <IconShield size={30} color="var(--mantine-color-vantigo-6)" />
            <Title order={2}>Roles & access</Title>
          </Group>
          <Text c="dimmed" mt={5}>
            Build additive permission sets and assign them safely to your team.
          </Text>
        </div>
        <Group>
          {isOwner && (
            <Button variant="light" leftSection={<IconUsers size={16} />} onClick={openDelegate}>
              Delegate administration
            </Button>
          )}
          {canCreateRole && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => openRoleEditor()}>
              Create custom role
            </Button>
          )}
        </Group>
      </Group>
      <SimpleGrid cols={{ base: 1, md: 2 }}>
        <Card withBorder>
          <Stack>
            <Group justify="space-between">
              <Title order={3}>Role composer</Title>
              <Badge variant="light">
                {displayRoles.filter((role) => !role.isSystem && !role.isBuiltIn).length} managed custom
              </Badge>
            </Group>
            <Text size="sm" c="dimmed">
              Permissions are additive. Protected roles are read-only; delegated administrators see only their stewarded
              roles.
            </Text>
            {roles.isPending || catalog.isPending ? (
              <Loader />
            ) : (
              <Stack>
                {displayRoles.map((role) => {
                  const editable = editableRole(role) && canEditRole(role);
                  return (
                    <Card key={role.id} withBorder padding="sm">
                      <Group justify="space-between">
                        <div>
                          <Group gap="xs">
                            {(role.isSystem || role.isBuiltIn || !editable) && <IconLock size={15} />}
                            <Text fw={600}>{role.displayName}</Text>
                          </Group>
                          <Text size="xs" c="dimmed">
                            {role.description}
                          </Text>
                        </div>
                        {role.isSystem || role.isBuiltIn ? (
                          <Badge color="gray">Protected</Badge>
                        ) : editable ? (
                          <Group>
                            <Button size="compact-sm" variant="subtle" onClick={() => openRoleEditor(role)}>
                              Edit
                            </Button>
                            <Button
                              size="compact-sm"
                              color="red"
                              variant="subtle"
                              onClick={() =>
                                modals.openConfirmModal({
                                  title: "Delete custom role?",
                                  children: <Text size="sm">Assignments using this role may change.</Text>,
                                  labels: { confirm: "Delete role", cancel: "Cancel" },
                                  confirmProps: { color: "red" },
                                  onConfirm: () => removeRole.mutate(role),
                                })
                              }
                            >
                              <IconTrash size={15} />
                            </Button>
                          </Group>
                        ) : (
                          <Badge color="gray">Outside current delegation boundary</Badge>
                        )}
                      </Group>
                    </Card>
                  );
                })}
              </Stack>
            )}
          </Stack>
        </Card>
        <Card withBorder>
          <Stack>
            <Title order={3}>Assign roles</Title>
            <Text size="sm" c="dimmed">
              Roles outside your delegated scope are kept. Backend authority remains final.
            </Text>
            {!isOwner && (
              <Select
                label="Assignment scope"
                placeholder="Choose one scope"
                data={scopes
                  .filter((item) => item.assignableRoleIds.length > 0)
                  .map((item) => ({ value: item.id, label: `Delegation ${item.id.slice(0, 8)}` }))}
                value={assignmentScopeId}
                onChange={setAssignmentScopeId}
                required
              />
            )}
            <Select
              label="User"
              placeholder="Select a user"
              searchable
              data={(users.data ?? [])
                .filter((user) => user.id !== me.data?.id)
                .map((user) => ({ value: user.id, label: user.displayName || user.email || user.id }))}
              value={selectedUser}
              onChange={setSelectedUser}
            />
            <Divider />
            {selectedUser && access.data && (isOwner || assignmentScope) ? (
              <>
                <Group>
                  <Text fw={600}>Current roles</Text>
                  {access.data.roles.map((role) => (
                    <Badge key={role} color={role === "Owner" ? "violet" : undefined}>
                      {role}
                    </Badge>
                  ))}
                </Group>
                <Text size="sm" c="dimmed">
                  Effective permissions:{" "}
                  {access.data.permissions.includes("*")
                    ? "All permissions"
                    : access.data.permissions.join(", ") || "None"}
                </Text>
                <MultiSelect
                  label="Assignable custom roles"
                  data={assignableRoles.map((role) => ({ value: role.id, label: role.displayName }))}
                  value={assignableRoles
                    .filter((role) => access.data?.roleIds?.includes(role.id))
                    .map((role) => role.id)}
                  onChange={(roleIds) =>
                    selectedUser && access.data
                      ? assignment.mutate({ id: selectedUser, roleIds, version: access.data.version })
                      : undefined
                  }
                  disabled={assignment.isPending}
                />
              </>
            ) : selectedUser ? (
              <Loader />
            ) : (
              <Text size="sm" c="dimmed">
                Select a user to review effective access.
              </Text>
            )}
          </Stack>
        </Card>
      </SimpleGrid>
      {isOwner && (
        <Card withBorder>
          <Stack>
            <Title order={3}>Delegated administrators</Title>
            <Text size="sm" c="dimmed">
              Delegation grants role administration, not business data access.
            </Text>
            {(delegations.data ?? [])
              .filter((item) => !item.revokedAt)
              .map((item) => (
                <Group key={item.id} justify="space-between">
                  <Text>
                    {users.data?.find((user) => user.id === item.granteeUserId)?.displayName || item.granteeUserId}
                  </Text>
                  <Button
                    size="compact-sm"
                    color="red"
                    variant="subtle"
                    onClick={() =>
                      modals.openConfirmModal({
                        title: "Revoke delegation?",
                        children: <Text size="sm">This removes the delegated administration boundary.</Text>,
                        labels: { confirm: "Revoke", cancel: "Cancel" },
                        confirmProps: { color: "red" },
                        onConfirm: () => revoke.mutate({ id: item.id, version: item.version }),
                      })
                    }
                  >
                    Revoke
                  </Button>
                </Group>
              ))}
          </Stack>
        </Card>
      )}
      <Modal
        opened={roleModal}
        onClose={closeRole}
        title={selectedRole ? "Edit custom role" : "Create custom role"}
        size="lg"
      >
        <form
          onSubmit={roleForm.onSubmit((values) => {
            const scopeForSubmit = selectedRole ? selectedEditScope : scopeForCreation;
            if (
              !isOwner &&
              (!scopeForSubmit ||
                (selectedRole &&
                  !selectedRole.permissions.every((key) => scopeForSubmit.grantablePermissionKeys.includes(key))))
            )
              return;
            saveRole.mutate({
              ...values,
              permissionKeys: selectedRole
                ? values.permissionKeys
                : values.permissionKeys.filter((key) => allowedPermissionKeys.has(key)),
              ...(!isOwner && !selectedRole && scopeForSubmit ? { delegationId: scopeForSubmit.id } : {}),
            });
          })}
        >
          <Stack>
            {!isOwner && (
              <Select
                label="Delegation scope"
                placeholder="Choose a scope"
                data={(selectedRole ? coveringEditScopes : scopes.filter((item) => item.canCreateRoles)).map(
                  (item) => ({ value: item.id, label: `Delegation ${item.id.slice(0, 8)}` }),
                )}
                value={selectedScopeId}
                onChange={(value) => {
                  setSelectedScopeId(value);
                  if (!selectedRole) roleForm.setFieldValue("permissionKeys", []);
                }}
                required
              />
            )}
            {!isOwner && !selectedScopeId && (
              <Text size="sm" c="dimmed">
                Choose a qualifying delegation scope before selecting permissions.
              </Text>
            )}
            <TextInput label="Internal name" disabled={!!selectedRole} {...roleForm.getInputProps("name")} />
            <TextInput label="Display name" {...roleForm.getInputProps("displayName")} />
            <TextInput label="Description" {...roleForm.getInputProps("description")} />
            {(isOwner || (selectedRole ? selectedEditScope : scopeForCreation)) &&
              [...groupedPermissions.entries()].map(([group, permissions]) => (
                <Stack key={group} gap="xs">
                  <Text fw={600}>{group}</Text>
                  {permissions?.map((permission) => (
                    <Checkbox
                      key={permission.key}
                      label={
                        <Group gap="xs">
                          <span>{permission.displayName}</span>
                          {permission.sensitive && <Badge color="orange">Sensitive</Badge>}
                        </Group>
                      }
                      description={permission.description}
                      checked={roleForm.values.permissionKeys.includes(permission.key)}
                      onChange={(event) =>
                        roleForm.setFieldValue(
                          "permissionKeys",
                          event.currentTarget.checked
                            ? [...roleForm.values.permissionKeys, permission.key]
                            : roleForm.values.permissionKeys.filter((key) => key !== permission.key),
                        )
                      }
                    />
                  ))}
                </Stack>
              ))}
            <Button
              type="submit"
              loading={saveRole.isPending}
              disabled={!isOwner && !(selectedRole ? selectedEditScope : scopeForCreation)}
            >
              Save role
            </Button>
          </Stack>
        </form>
      </Modal>
      {isOwner && (
        <Modal opened={delegateModal} onClose={closeDelegate} title="Delegate role administration">
          <form onSubmit={delegateForm.onSubmit((values) => saveDelegation.mutate(values))}>
            <Stack>
              <Text size="sm" c="dimmed">
                This grants administration only, not business data access. You cannot delegate to yourself.
              </Text>
              <Select
                label="Administrator"
                data={(users.data ?? [])
                  .filter((user) => user.id !== me.data?.id)
                  .map((user) => ({ value: user.id, label: user.displayName || user.email || user.id }))}
                {...delegateForm.getInputProps("granteeUserId")}
              />
              <MultiSelect
                label="Delegable permissions"
                data={(catalog.data ?? [])
                  .filter((item) => item.delegable)
                  .map((item) => ({ value: item.key, label: item.displayName }))}
                {...delegateForm.getInputProps("permissionKeys")}
              />
              <MultiSelect
                label="Stewarded custom roles"
                data={customRoles.map((role) => ({ value: role.id, label: role.displayName }))}
                {...delegateForm.getInputProps("stewardedRoleIds")}
              />
              <TextInput
                label="Expiry (optional)"
                placeholder="2027-01-31T00:00:00Z"
                {...delegateForm.getInputProps("expiresAt")}
              />
              <Checkbox
                label="May create custom roles"
                {...delegateForm.getInputProps("canCreateRoles", { type: "checkbox" })}
              />
              <Button type="submit" loading={saveDelegation.isPending}>
                Grant delegation
              </Button>
            </Stack>
          </form>
        </Modal>
      )}
    </Stack>
  );
};

export const Route = createFileRoute("/admin/roles")({
  beforeLoad: async ({ context }) => {
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!access.canManageAuthorization) throw redirect({ to: "/" });
  },
  component: RolesPage,
});
