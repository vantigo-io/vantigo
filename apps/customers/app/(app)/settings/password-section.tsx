"use client";

import { Button, Card, Group, PasswordInput, Stack, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { zod4Resolver } from "mantine-form-zod-resolver";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { z } from "zod";

const passwordSchema = z
  .object({
    currentPassword: z.string().min(1, "current_required"),
    newPassword: z.string().min(8, "too_short"),
    confirmPassword: z.string(),
  })
  .refine((values) => values.newPassword === values.confirmPassword, {
    message: "mismatch",
    path: ["confirmPassword"],
  });

export function PasswordSection() {
  const t = useTranslations("settings.password");
  const router = useRouter();
  const [isSubmitting, setIsSubmitting] = useState(false);

  const form = useForm({
    mode: "uncontrolled",
    initialValues: { currentPassword: "", newPassword: "", confirmPassword: "" },
    validate: (values) => {
      const result = zod4Resolver(passwordSchema)(values);
      if (result.currentPassword) result.currentPassword = t("currentRequired");
      if (result.newPassword) result.newPassword = t("tooShort");
      if (result.confirmPassword) result.confirmPassword = t("mismatch");
      return result;
    },
  });

  const handleSubmit = async (values: typeof form.values) => {
    setIsSubmitting(true);
    const { error } = await authClient.changePassword({
      currentPassword: values.currentPassword,
      newPassword: values.newPassword,
      revokeOtherSessions: true,
    });
    setIsSubmitting(false);

    if (error) {
      notifications.show({
        color: "red",
        title: t("errorTitle"),
        message: error.message ?? t("errorMessage"),
      });
      return;
    }

    form.reset();
    notifications.show({ color: "green", title: t("successTitle"), message: t("successMessage") });
    router.refresh();
  };

  return (
    <Card withBorder maw={560}>
      <form onSubmit={form.onSubmit(handleSubmit)}>
        <Stack gap="md">
          <Title order={4}>{t("title")}</Title>
          <PasswordInput
            label={t("currentLabel")}
            withAsterisk
            autoComplete="current-password"
            key={form.key("currentPassword")}
            {...form.getInputProps("currentPassword")}
          />
          <PasswordInput
            label={t("newLabel")}
            placeholder={t("newPlaceholder")}
            withAsterisk
            autoComplete="new-password"
            key={form.key("newPassword")}
            {...form.getInputProps("newPassword")}
          />
          <PasswordInput
            label={t("confirmLabel")}
            withAsterisk
            autoComplete="new-password"
            key={form.key("confirmPassword")}
            {...form.getInputProps("confirmPassword")}
          />
          <Group justify="flex-end">
            <Button type="submit" loading={isSubmitting}>
              {t("submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
}
