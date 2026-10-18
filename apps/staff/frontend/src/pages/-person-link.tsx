import { Badge, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconLink, IconLinkOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { personLinkQueryOptions, type StaffAssignableUser, type StaffPerson, updatePersonLink } from "../api/people";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup } from "../components/field-group";
import { UserPicker } from "../components/user-picker";
import "../i18n";
import { refusalText } from "../lib/absence";
import { fieldRefusals } from "../lib/errors";

/**
 * The self-service link (design S24 as amended): which user is this person
 * to Staff, for My page and nothing else. HR (`canManageAccount`) links any
 * active user, Owners included, and unlinks. The wording says what it is
 * and is not — linked, not managed by Staff — since delivery C's managed
 * accounts are another thing.
 *
 * An erased record takes the unlink — so the user can be linked to someone
 * else — and no link (design S15): the picker is not offered on it.
 *
 * The accounts worker's link (delivery C) is the same row: a person whose
 * link the worker holds is named as managed by Staff, with neither a picker
 * nor an unlink — the server refuses both (`link_managed`), and the Account
 * tab is where it is handled. A refusal is said in words: `link_managed` for
 * a user Staff manages, `accounts_unavailable` when identity could not say.
 */
export const PersonLinkGroup = ({ person }: { person: StaffPerson }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const { data: link } = useQuery(personLinkQueryOptions(person.id));
  const [picked, setPicked] = useState<StaffAssignableUser | null>(null);
  const [error, setError] = useState<string | undefined>();

  const save = useMutation({
    mutationFn: (userId: string | null) => updatePersonLink(person.id, userId),
    onSuccess: async (answer) => {
      queryClient.setQueryData(personLinkQueryOptions(person.id).queryKey, answer);
      setPicked(null);
      setError(undefined);
      notifications.show({ color: "teal", title: t("linkSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people", "detail", person.id] });
    },
    onError: (failure) => {
      if (!(failure instanceof ApiValidationError)) {
        notifications.show({ color: "red", title: t("couldNotLink"), message: refusalText(failure, t) });
        return;
      }
      const { fields, rest } = fieldRefusals(failure, ["userId"]);
      setError(fields.userId);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotLink"), message: rest.join(" ") });
    },
  });

  const confirmUnlink = (name: string) =>
    modals.openConfirmModal({
      title: t("unlinkTitle", { name }),
      children: <Text size="sm">{t("unlinkExplained")}</Text>,
      labels: { confirm: t("unlink"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => save.mutate(null),
    });

  if (!link) return null;
  const managed = link.state !== undefined && link.state !== "linked";
  return (
    <FieldGroup title={t("selfService")} description={t("linkExplained")}>
      {managed ? (
        <Text size="sm">
          {link.user ? t("linkedManaged", { name: link.user.displayName }) : t("accountLinkState_preparing")}
        </Text>
      ) : link.linked && link.user ? (
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Group gap="xs">
            <Text size="sm">{t("linkedNotManaged", { name: link.user.displayName })}</Text>
            {!link.user.active && (
              <Badge color="gray" variant="light">
                {t("userDisabled")}
              </Badge>
            )}
          </Group>
          <Button
            variant="subtle"
            color="red"
            leftSection={<IconLinkOff size={16} />}
            loading={save.isPending}
            onClick={() => confirmUnlink(link.user?.displayName ?? "")}
          >
            {t("unlink")}
          </Button>
        </Group>
      ) : person.erasedAt ? (
        <Text size="sm" c="dimmed">
          {t("notLinked")}
        </Text>
      ) : (
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {t("notLinked")}
          </Text>
          <Group align="flex-end" wrap="wrap" gap="xs">
            <div style={{ flex: "1 1 16rem" }}>
              <UserPicker
                value={picked?.id ?? null}
                onChange={(user) => {
                  setPicked(user);
                  setError(undefined);
                }}
                error={error}
              />
            </div>
            <Button
              leftSection={<IconLink size={16} />}
              loading={save.isPending}
              onClick={() => (picked ? save.mutate(picked.id) : setError(t("pickUser")))}
            >
              {t("link")}
            </Button>
          </Group>
        </Stack>
      )}
    </FieldGroup>
  );
};
