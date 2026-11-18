import { Button, Group, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { dateOfBirthQueryOptions, putDateOfBirth } from "../api/date-of-birth";
import "../i18n";

/**
 * A person's date of birth on the contact card (invoices phase 6 design I30):
 * used to find the customer for eFaktura, never printed. Shown and edited for
 * a person only; `canEdit` is the host's `customers:update`.
 */
export const CustomerDateOfBirth = ({ customerId, canEdit }: { customerId: number; canEdit?: boolean }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const options = dateOfBirthQueryOptions(customerId);
  const stored = useQuery(options);
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");
  const save = useMutation({
    mutationFn: () => putDateOfBirth(customerId, value || null),
    onSuccess: async (fresh) => {
      queryClient.setQueryData(options.queryKey, fresh);
      setEditing(false);
      // The PUT moved the customer's revision: the next contact or type edit
      // must read it, not the cached one.
      await queryClient.invalidateQueries({ queryKey: ["customers", customerId] });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("dateOfBirthCouldNotSave"),
        message: error instanceof Error ? error.message : String(error),
      }),
  });
  if (!stored.data) return null;
  const shown = stored.data.dateOfBirth ?? null;
  return (
    <Stack gap={4} data-testid="date-of-birth">
      {editing ? (
        <Group align="flex-end">
          <TextInput
            type="date"
            label={t("dateOfBirth")}
            description={t("dateOfBirthHelp")}
            value={value}
            onChange={(e) => setValue(e.currentTarget.value)}
          />
          <Button size="xs" loading={save.isPending} onClick={() => save.mutate()}>
            {t("dateOfBirthSave")}
          </Button>
        </Group>
      ) : (
        <Group gap="xs">
          <Text size="sm" c="dimmed" miw={64}>
            {t("dateOfBirth")}
          </Text>
          <Text size="sm">{shown ?? "—"}</Text>
          {canEdit && (
            <Button
              size="compact-xs"
              variant="subtle"
              onClick={() => {
                setValue(shown ?? "");
                setEditing(true);
              }}
            >
              {t("dateOfBirthEdit")}
            </Button>
          )}
        </Group>
      )}
      {!editing && (
        <Text size="xs" c="dimmed">
          {t("dateOfBirthHelp")}
        </Text>
      )}
    </Stack>
  );
};
