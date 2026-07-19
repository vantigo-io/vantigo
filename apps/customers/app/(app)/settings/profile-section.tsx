"use client";

import {
  Avatar,
  Button,
  Card,
  FileButton,
  Group,
  Input,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconTrash, IconUpload } from "@tabler/icons-react";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { setPreferredCustomerType } from "@vantigo/customers/lib/settings/actions";
import { AVATAR_MAX_FILE_BYTES, fileToAvatarDataUrl } from "@vantigo/customers/lib/settings/avatar";
import {
  defaultPreferredCustomerType,
  isPreferredCustomerType,
  preferredCustomerTypes,
} from "@vantigo/customers/lib/settings/preferences";
import { zod4Resolver } from "mantine-form-zod-resolver";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState, useTransition } from "react";
import { z } from "zod";
import type { SettingsUser } from "./settings-page";

const nameSchema = z.object({
  name: z.string().trim().min(1, "name_required").max(200, "name_required"),
});

export function ProfileSection({ user }: Readonly<{ user: SettingsUser }>) {
  const t = useTranslations("settings.profile");
  const router = useRouter();

  // --- Name ---
  const [isSavingName, setIsSavingName] = useState(false);
  const form = useForm({
    mode: "uncontrolled",
    initialValues: { name: user.name },
    validate: (values) => {
      const result = zod4Resolver(nameSchema)(values);
      if (result.name) result.name = t("nameRequired");
      return result;
    },
  });

  const handleNameSubmit = async ({ name }: { name: string }) => {
    setIsSavingName(true);
    const { error } = await authClient.updateUser({ name: name.trim() });
    setIsSavingName(false);

    if (error) {
      notifications.show({ color: "red", message: error.message ?? t("saveError") });
      return;
    }
    notifications.show({ color: "green", message: t("nameSaved") });
    router.refresh();
  };

  // --- Avatar ---
  const [isSavingAvatar, setIsSavingAvatar] = useState(false);
  const [image, setImage] = useState(user.image);

  const updateImage = async (value: string | null) => {
    setIsSavingAvatar(true);
    const { error } = await authClient.updateUser({ image: value });
    setIsSavingAvatar(false);

    if (error) {
      notifications.show({ color: "red", message: error.message ?? t("saveError") });
      return;
    }
    setImage(value);
    notifications.show({ color: "green", message: t("avatarSaved") });
    router.refresh();
  };

  const handleAvatarFile = async (file: File | null) => {
    if (!file) return;
    if (file.size > AVATAR_MAX_FILE_BYTES) {
      notifications.show({ color: "red", message: t("avatarTooLarge") });
      return;
    }
    try {
      await updateImage(await fileToAvatarDataUrl(file));
    } catch {
      notifications.show({ color: "red", message: t("avatarError") });
    }
  };

  // --- Preferences ---
  const [isPendingPreference, startTransition] = useTransition();
  const [preferredType, setPreferredType] = useState(
    isPreferredCustomerType(user.preferredCustomerType)
      ? user.preferredCustomerType
      : defaultPreferredCustomerType,
  );

  const handlePreferredTypeChange = (value: string) => {
    if (!isPreferredCustomerType(value)) return;
    setPreferredType(value);
    startTransition(async () => {
      await setPreferredCustomerType(value);
    });
  };

  return (
    <Stack gap="lg" maw={560}>
      <Card withBorder>
        <Stack gap="md">
          <Title order={4}>{t("avatarTitle")}</Title>
          <Group>
            <Avatar src={image} name={user.name} color="initials" size={72} radius="xl" />
            <Group gap="xs">
              <FileButton onChange={handleAvatarFile} accept="image/png,image/jpeg,image/webp">
                {(props) => (
                  <Button
                    {...props}
                    variant="default"
                    loading={isSavingAvatar}
                    leftSection={<IconUpload size={16} stroke={1.5} />}
                  >
                    {t("avatarUpload")}
                  </Button>
                )}
              </FileButton>
              {image && (
                <Button
                  variant="subtle"
                  color="red"
                  disabled={isSavingAvatar}
                  leftSection={<IconTrash size={16} stroke={1.5} />}
                  onClick={() => updateImage(null)}
                >
                  {t("avatarRemove")}
                </Button>
              )}
            </Group>
          </Group>
        </Stack>
      </Card>

      <Card withBorder>
        <form onSubmit={form.onSubmit(handleNameSubmit)}>
          <Stack gap="md">
            <Title order={4}>{t("profileTitle")}</Title>
            <TextInput
              label={t("nameLabel")}
              withAsterisk
              key={form.key("name")}
              {...form.getInputProps("name")}
            />
            <TextInput label={t("emailLabel")} value={user.email} disabled />
            <Group justify="flex-end">
              <Button type="submit" loading={isSavingName}>
                {t("save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Card>

      <Card withBorder>
        <Stack gap="md">
          <Title order={4}>{t("preferencesTitle")}</Title>

          <Input.Wrapper label={t("languageLabel")}>
            <div>
              <LanguageToggle />
            </div>
          </Input.Wrapper>

          <Input.Wrapper
            label={t("preferredCustomerTypeLabel")}
            description={t("preferredCustomerTypeDescription")}
          >
            <SegmentedControl
              mt={4}
              fullWidth
              value={preferredType}
              disabled={isPendingPreference}
              onChange={handlePreferredTypeChange}
              data={preferredCustomerTypes.map((value) => ({
                value,
                label: t(`preferredCustomerType.${value}`),
              }))}
            />
          </Input.Wrapper>

          <Text size="xs" c="dimmed">
            {t("preferencesHint")}
          </Text>
        </Stack>
      </Card>
    </Stack>
  );
}
