import { ActionIcon, Alert, Button, Group, Loader, Modal, Select, Stack, Table, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  accessQueryOptions,
  articleQueryKey,
  assignableUsersQueryOptions,
  favouritesQueryKey,
  type KnowledgeAccessLevel,
  type KnowledgeAccessUpdate,
  type KnowledgeArticleAccess,
  treeQueryKey,
  updateAccess,
} from "../api/articles";
import { refusalCode } from "../api/request";
import { VisibilityConfirm } from "./visibility-confirm";

const levels: readonly KnowledgeAccessLevel[] = ["write", "read", "none"];
const INHERIT = "inherit";

interface OwnMember {
  userId: string;
  displayName: string | null;
  permission: KnowledgeAccessLevel;
}

const ownMembersOf = (access: KnowledgeArticleAccess): OwnMember[] =>
  access.members
    .filter((member) => !member.inherited)
    .map(({ userId, displayName, permission }) => ({ userId, displayName, permission }));

/**
 * Who may read and write an article (knowledge design K4): a default for
 * everyone in the company, inherited from the parent unless set here, and
 * named members, the parent's shown as inherited. A writer, or
 * knowledge:manage on an article that is not private, changes it; anyone
 * else only sees it.
 */
export const AccessDialog = ({
  articleId,
  isRoot,
  opened,
  onClose,
  onLostAccess,
}: {
  articleId: string;
  isRoot: boolean;
  opened: boolean;
  onClose: () => void;
  /** The change left the caller unable to read the page (the answer's effective access is none). */
  onLostAccess?: () => void;
}) => {
  const { t } = useI18n("knowledge");
  const access = useQuery({ ...accessQueryOptions(articleId), enabled: opened });
  return (
    <Modal opened={opened} onClose={onClose} title={t("accessTitle")} size="lg">
      {access.isPending ? (
        <Loader size="sm" />
      ) : access.isError ? (
        <Alert color="red">{t("accessLoadFailed")}</Alert>
      ) : (
        // A fresh answer starts the form again from what the server holds.
        <AccessForm
          key={access.dataUpdatedAt}
          data={access.data}
          articleId={articleId}
          isRoot={isRoot}
          onClose={onClose}
          onLostAccess={onLostAccess}
        />
      )}
    </Modal>
  );
};

const AccessForm = ({
  data,
  articleId,
  isRoot,
  onClose,
  onLostAccess,
}: {
  data: KnowledgeArticleAccess;
  articleId: string;
  isRoot: boolean;
  onClose: () => void;
  onLostAccess?: () => void;
}) => {
  const { t } = useI18n("knowledge");
  const queryClient = useQueryClient();
  const [internal, setInternal] = useState<KnowledgeAccessLevel | null>(data.internalPermission);
  const [members, setMembers] = useState<OwnMember[]>(() => ownMembersOf(data));
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  const save = useMutation({
    mutationFn: (input: KnowledgeAccessUpdate) => updateAccess(articleId, input),
    onSuccess: (answer) => {
      queryClient.setQueryData(accessQueryOptions(articleId).queryKey, answer);
      void queryClient.invalidateQueries({ queryKey: articleQueryKey(articleId) });
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
      void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
      setConfirming(false);
      notifications.show({ color: "green", message: t("accessSaved") });
      onClose();
      if (answer.effectivePermission === "none") onLostAccess?.();
    },
    onError: (failure, input) => {
      const code = refusalCode(failure);
      if (code === "visibility_change" && !input.confirmVisibility) {
        setConfirming(true);
        return;
      }
      setConfirming(false);
      setError(
        t(
          code === "last_writer"
            ? "accessLastWriter"
            : code === "root_permission_required"
              ? "accessRootRequired"
              : code === "article_read_only"
                ? "accessReadOnly"
                : "accessFailed",
        ),
      );
    },
  });

  const input = (confirmVisibility?: boolean): KnowledgeAccessUpdate => ({
    internalPermission: internal,
    members: members.map(({ userId, permission }) => ({ userId, permission })),
    ...(confirmVisibility ? { confirmVisibility: true } : {}),
  });

  const canManage = data.canManage;
  const levelOptions = levels.map((level) => ({ value: level, label: t(`level_${level}`) }));
  const inheritedFrom = data.inheritedFrom?.title;

  return (
    <>
      <Stack gap="md">
        {!canManage && (
          <Text size="sm" c="dimmed">
            {t("accessViewOnly")}
          </Text>
        )}
        <Select
          label={t("accessEveryone")}
          description={
            internal === null
              ? t("accessInherited", {
                  level: t(`level_${data.effectivePermission}`),
                  from: inheritedFrom ?? t("accessParent"),
                })
              : isRoot
                ? undefined
                : t("accessOwnDefault")
          }
          data={
            isRoot && data.internalPermission !== null
              ? levelOptions
              : [{ value: INHERIT, label: t("accessInherit") }, ...levelOptions]
          }
          value={internal ?? INHERIT}
          onChange={(value) => setInternal(!value || value === INHERIT ? null : (value as KnowledgeAccessLevel))}
          allowDeselect={false}
          disabled={!canManage}
        />
        <div>
          <Text size="sm" fw={500} mb={4}>
            {t("accessMembers")}
          </Text>
          <Table verticalSpacing={4} aria-label={t("accessMembers")}>
            <Table.Tbody>
              {/* A page with its own default does not take the parent pages' people (K4, as Odoo). */}
              {data.members
                .filter((member) => member.inherited && internal === null)
                .map((member) => (
                  <Table.Tr key={`inherited-${member.userId}`}>
                    <Table.Td>
                      <Text size="sm">{member.displayName ?? t("formerUser")}</Text>
                      <Text size="xs" c="dimmed">
                        {t("accessInheritedFrom", { from: member.inheritedFrom?.title ?? t("accessParent") })}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">{t(`level_${member.permission}`)}</Text>
                    </Table.Td>
                    <Table.Td />
                  </Table.Tr>
                ))}
              {members.map((member) => (
                <Table.Tr key={member.userId}>
                  <Table.Td>
                    <Text size="sm">{member.displayName ?? t("formerUser")}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Select
                      size="xs"
                      aria-label={t("accessMemberLevel", { name: member.displayName ?? t("formerUser") })}
                      data={levelOptions}
                      value={member.permission}
                      allowDeselect={false}
                      disabled={!canManage}
                      onChange={(value) =>
                        value &&
                        setMembers((rows) =>
                          rows.map((row) =>
                            row.userId === member.userId ? { ...row, permission: value as KnowledgeAccessLevel } : row,
                          ),
                        )
                      }
                    />
                  </Table.Td>
                  <Table.Td w={40}>
                    {canManage && (
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("accessRemoveMember", { name: member.displayName ?? t("formerUser") })}
                        onClick={() => setMembers((rows) => rows.filter((row) => row.userId !== member.userId))}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          {data.members.length === 0 && members.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("accessNoMembers")}
            </Text>
          )}
        </div>
        {canManage && (
          <AddMember
            exclude={members.map((member) => member.userId)}
            onAdd={(member) => setMembers((rows) => [...rows, member])}
          />
        )}
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {canManage ? t("cancel") : t("close")}
          </Button>
          {canManage && (
            <Button loading={save.isPending && !confirming} onClick={() => save.mutate(input())}>
              {t("save")}
            </Button>
          )}
        </Group>
      </Stack>
      <VisibilityConfirm
        opened={confirming}
        busy={save.isPending}
        onCancel={() => setConfirming(false)}
        onConfirm={() => save.mutate(input(true))}
      />
    </>
  );
};

/** A person to add, from the directory, and what they may do. */
const AddMember = ({ exclude, onAdd }: { exclude: readonly string[]; onAdd: (member: OwnMember) => void }) => {
  const { t } = useI18n("knowledge");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const [userId, setUserId] = useState<string | null>(null);
  const [level, setLevel] = useState<KnowledgeAccessLevel>("write");
  const users = useQuery(assignableUsersQueryOptions(debounced));
  const options = (users.data ?? [])
    .filter((user) => !exclude.includes(user.userId))
    .map((user) => ({ value: user.userId, label: user.displayName }));
  return (
    <Group align="flex-end" gap="xs">
      <Select
        label={t("accessAddPerson")}
        placeholder={t("accessSearchPeople")}
        searchable
        clearable
        style={{ flex: 1 }}
        data={options}
        value={userId}
        onChange={setUserId}
        onSearchChange={setSearch}
        filter={({ options: all }) => all}
        nothingFoundMessage={t("accessNobodyFound")}
      />
      <Select
        aria-label={t("accessNewMemberLevel")}
        w={140}
        data={levels.map((value) => ({ value, label: t(`level_${value}`) }))}
        value={level}
        allowDeselect={false}
        onChange={(value) => value && setLevel(value as KnowledgeAccessLevel)}
      />
      <Button
        variant="light"
        disabled={!userId}
        onClick={() => {
          const user = users.data?.find((candidate) => candidate.userId === userId);
          if (!userId) return;
          onAdd({ userId, displayName: user?.displayName ?? null, permission: level });
          setUserId(null);
          setSearch("");
        }}
      >
        {t("accessAdd")}
      </Button>
    </Group>
  );
};
