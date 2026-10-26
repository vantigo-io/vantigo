import { Alert, Avatar, Button, Card, FileInput, Group, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconUser } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, setLanguagePreference, useTranslation } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../../i18n";
import { getProfile, profileQueryKey, removeProfilePhoto, updateProfile, uploadProfilePhoto } from "../../api/account";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { timeZoneOptions } from "../../api/workspace";
import { message, notify } from "./-helpers";

/** The time zone select's first choice, which saves as null: follow the workspace's zone. */
const followWorkspace = "workspace";

function ProfileForm() {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  const userId = session.data?.user.id;
  const query = useQuery({ queryKey: profileQueryKey(userId ?? "unknown"), queryFn: getProfile, enabled: !!userId });
  const [file, setFile] = useState<File | null>(null);
  const form = useForm<{ displayName: string; preferredLanguage: "auto" | "en" | "nb"; timeZone: string }>({
    initialValues: { displayName: "", preferredLanguage: "auto", timeZone: followWorkspace },
    validate: { displayName: (v) => (v.trim() ? null : t("enterYourName")) },
  });
  useEffect(() => {
    if (query.data)
      form.setValues({
        displayName: query.data.displayName,
        preferredLanguage: query.data.preferredLanguage,
        timeZone: query.data.timeZone ?? followWorkspace,
      });
  }, [query.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: (v: typeof form.values) =>
      updateProfile({ ...v, timeZone: v.timeZone === followWorkspace ? null : v.timeZone }),
    onSuccess: (v) => {
      if (userId) qc.setQueryData(profileQueryKey(userId), v);
      // The session answers the zone in effect, which every formatter reads.
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      setLanguagePreference(v.preferredLanguage);
      notifications.show({ title: t("saved"), message: t("profileUpdated"), color: "teal" });
    },
    onError: (e) => {
      // A zone the server refuses (CET, say) is said on the field itself.
      const onZone = (e as { fields?: Record<string, string[]> }).fields?.timeZone?.[0];
      if (onZone) form.setFieldError("timeZone", onZone);
      else notify(t("profileCouldNotSave"), e, t("requestCouldNotComplete"));
    },
  });
  const businessTimeZone = session.data?.businessTimeZone ?? "";
  /**
   * A photo write refused: identity's 409 `avatar_managed` (Staff's photo was
   * set meanwhile) is said in the page's words, and the profile read again so
   * the controls close; any other refusal as the server words it.
   */
  const photoRefused = (title: string, e: unknown) => {
    if ((e as { code?: string }).code !== "avatar_managed") {
      notify(title, e, t("requestCouldNotComplete"));
      return;
    }
    notifications.show({ title, message: t("photoManagedByStaff"), color: "red" });
    if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
  };
  const upload = useMutation({
    mutationFn: uploadProfilePhoto,
    onSuccess: () => {
      setFile(null);
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoUpdated"), color: "teal" });
    },
    onError: (e) => photoRefused(t("photoCouldNotUpload"), e),
  });
  const remove = useMutation({
    mutationFn: removeProfilePhoto,
    onSuccess: () => {
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoRemoved"), color: "teal" });
    },
    onError: (e) => photoRefused(t("photoCouldNotRemove"), e),
  });
  if (query.isPending) return <ContentSkeleton rows={4} rowHeight={52} />;
  if (query.isError)
    return (
      <Alert color="red" title={t("profileCouldNotLoad")}>
        {message(query.error, t("requestCouldNotComplete"))}
      </Alert>
    );
  const value = query.data;
  // While Staff's photo is the picture, identity refuses a change here (409 avatar_managed): say who to ask.
  const managed = value.avatarManagedBy === "staff";
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
              // Said on the field itself, so a screen reader hears why it is closed.
              description={managed ? t("photoManagedByStaff") : undefined}
              accept="image/jpeg,image/png"
              value={file}
              onChange={setFile}
              disabled={managed}
            />
            <Button disabled={!file || managed} loading={upload.isPending} onClick={() => file && upload.mutate(file)}>
              {t("uploadPhoto")}
            </Button>
            {value.avatarUrl && (
              <Button
                color="red"
                variant="subtle"
                disabled={managed}
                loading={remove.isPending}
                onClick={() => remove.mutate()}
              >
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
            <Select
              label={t("timeZone")}
              description={t("timeZoneDescription")}
              searchable
              allowDeselect={false}
              data={[
                { value: followWorkspace, label: t("sameAsWorkspace", { zone: businessTimeZone }) },
                ...timeZoneOptions(value.timeZone ?? businessTimeZone)
                  .filter((zone) => zone !== "")
                  .map((zone) => ({ value: zone, label: zone })),
              ]}
              {...form.getInputProps("timeZone")}
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
  return (
    <Stack gap="xl">
      <PageHeader title={t("profile")} description={t("profileDescription")} />
      <ProfileForm />
    </Stack>
  );
};

export const Route = createFileRoute("/settings/profile")({ component: ProfilePage });
