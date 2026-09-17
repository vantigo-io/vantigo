import { Alert, Avatar, Button, Card, FileInput, Group, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconUser } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { PageHeader, setLanguagePreference, useTranslation } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../../i18n";
import { getProfile, profileQueryKey, removeProfilePhoto, updateProfile, uploadProfilePhoto } from "../../api/account";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { message, notify } from "./-helpers";

function ProfileForm() {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  const userId = session.data?.user.id;
  const query = useQuery({ queryKey: profileQueryKey(userId ?? "unknown"), queryFn: getProfile, enabled: !!userId });
  const [file, setFile] = useState<File | null>(null);
  const form = useForm<{ displayName: string; preferredLanguage: "auto" | "en" | "nb" }>({
    initialValues: { displayName: "", preferredLanguage: "auto" },
    validate: { displayName: (v) => (v.trim() ? null : t("enterYourName")) },
  });
  useEffect(() => {
    if (query.data)
      form.setValues({ displayName: query.data.displayName, preferredLanguage: query.data.preferredLanguage });
  }, [query.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: updateProfile,
    onSuccess: (v) => {
      if (userId) qc.setQueryData(profileQueryKey(userId), v);
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      setLanguagePreference(v.preferredLanguage);
      notifications.show({ title: t("saved"), message: t("profileUpdated"), color: "teal" });
    },
    onError: (e) => notify(t("profileCouldNotSave"), e, t("requestCouldNotComplete")),
  });
  const upload = useMutation({
    mutationFn: uploadProfilePhoto,
    onSuccess: () => {
      setFile(null);
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoUpdated"), color: "teal" });
    },
    onError: (e) => notify(t("photoCouldNotUpload"), e, t("requestCouldNotComplete")),
  });
  const remove = useMutation({
    mutationFn: removeProfilePhoto,
    onSuccess: () => {
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoRemoved"), color: "teal" });
    },
    onError: (e) => notify(t("photoCouldNotRemove"), e, t("requestCouldNotComplete")),
  });
  if (query.isPending) return <Text c="dimmed">{t("loadingProfile")}</Text>;
  if (query.isError)
    return (
      <Alert color="red" title={t("profileCouldNotLoad")}>
        {message(query.error, t("requestCouldNotComplete"))}
      </Alert>
    );
  const value = query.data;
  return (
    <Stack gap="lg">
      <Card withBorder>
        <Stack>
          <Group>
            <Avatar src={value.avatarUrl} size="lg" radius="xl">
              <IconUser />
            </Avatar>
            <div>
              <Text fw={600}>{t("profilePhoto")}</Text>
              <Text size="sm" c="dimmed">
                {t("photoRequirements")}
              </Text>
            </div>
          </Group>
          <Group align="end">
            <FileInput
              flex={1}
              label={t("choosePhoto")}
              accept="image/jpeg,image/png"
              value={file}
              onChange={setFile}
            />
            <Button disabled={!file} loading={upload.isPending} onClick={() => file && upload.mutate(file)}>
              {t("uploadPhoto")}
            </Button>
            {value.avatarUrl && (
              <Button color="red" variant="subtle" loading={remove.isPending} onClick={() => remove.mutate()}>
                {t("remove")}
              </Button>
            )}
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
          <Stack>
            <TextInput label={t("name")} {...form.getInputProps("displayName")} />
            <TextInput label={t("email")} value={value.email ?? ""} readOnly description={t("emailDescription")} />
            <Select
              label={t("preferredLanguage")}
              data={[
                { value: "auto", label: t("automatic") },
                { value: "nb", label: t("norwegian") },
                { value: "en", label: t("english") },
              ]}
              {...form.getInputProps("preferredLanguage")}
              description={t("languageDescription")}
            />
            <Button type="submit" loading={save.isPending} w="fit-content">
              {t("saveProfile")}
            </Button>
          </Stack>
        </form>
      </Card>
    </Stack>
  );
}

const ProfilePage = () => {
  const { t } = useTranslation("settings");
  const { t: hostT } = useTranslation("host");
  return (
    <Stack maw={1180} mx="auto" gap="xl">
      <PageHeader eyebrow={hostT("navigation.settings")} title={t("profile")} description={t("profileDescription")} />
      <ProfileForm />
    </Stack>
  );
};

export const Route = createFileRoute("/settings/profile")({ component: ProfilePage });
