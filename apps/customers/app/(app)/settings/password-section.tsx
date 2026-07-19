"use client";

import { Button, Card, Group, PasswordInput, Stack, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useAuthAction } from "@vantigo/customers/lib/settings/use-auth-action";
import { zod4Resolver } from "mantine-form-zod-resolver";
import { useTranslations } from "next-intl";
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
  const { run, isPending } = useAuthAction();

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

  const handleSubmit = (values: typeof form.values) =>
    run(
      () =>
        authClient.changePassword({
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
          revokeOtherSessions: true,
        }),
      { success: t("successMessage"), error: t("errorMessage"), onSuccess: () => form.reset() },
    );

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
            <Button type="submit" loading={isPending}>
              {t("submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
}
