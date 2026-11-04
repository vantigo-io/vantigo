import { Button, Group, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type GoogleConnection, updateConnection } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { fieldRefusals, isStale } from "../lib/words";
import { OwnerOnly } from "./step-card";

const addressPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

interface AdminValues {
  adminSubject: string;
}

/** The admin to impersonate, the Owner's: saved under If-Match on the version read. */
export const AdminForm = ({ connection }: { connection: GoogleConnection }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const form = useForm<AdminValues>({
    initialValues: { adminSubject: connection.adminSubject ?? "" },
    validate: {
      adminSubject: (value) => (addressPattern.test(value.trim()) ? null : t("adminSubjectInvalid")),
    },
  });

  // A connection read anew (another tab's write, a reload after a 412) seeds the form while it is untouched.
  const loaded = connection.adminSubject ?? "";
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues({ adminSubject: loaded });
      form.setValues({ adminSubject: loaded });
    }
  }

  const save = useMutation({
    mutationFn: (values: AdminValues) =>
      updateConnection({ adminSubject: values.adminSubject.trim() }, connection.version),
    onSuccess: async (answer) => {
      queryClient.setQueryData([GOOGLE_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("adminSaved"), message: t("adminSavedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: async (error) => {
      if (isStale(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("versionMismatch"), message: t("versionMismatchHint") });
        await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["adminSubject"], t);
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSaveAdmin"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))} aria-label={t("step4Title")}>
      <Stack gap="sm">
        <TextInput
          label={t("adminSubject")}
          description={t("adminSubjectDescription")}
          type="email"
          autoComplete="off"
          withAsterisk
          {...form.getInputProps("adminSubject")}
        />
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** The admin to impersonate for anyone else: what is set, and that the Owner sets it. */
export const AdminRead = ({ connection }: { connection: GoogleConnection }) => {
  const { t } = useI18n("google");
  return (
    <Stack gap="xs">
      <Text size="sm">{t("adminSubjectValue", { value: connection.adminSubject ?? t("notSet") })}</Text>
      <OwnerOnly />
    </Stack>
  );
};
