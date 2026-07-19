"use client";

import { Anchor, Button, PasswordInput, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

interface ResetPasswordFormValues {
  password: string;
  confirmPassword: string;
}

export function ResetPasswordForm() {
  const t = useTranslations("resetPassword");
  const router = useRouter();
  const searchParams = useSearchParams();
  const [isSubmitting, setIsSubmitting] = useState(false);

  const token = searchParams.get("token");
  const isInvalidLink = !token || searchParams.get("error") !== null;

  const form = useForm<ResetPasswordFormValues>({
    mode: "uncontrolled",
    initialValues: {
      password: "",
      confirmPassword: "",
    },
    validate: {
      password: (value) => (value.length >= 8 ? null : t("passwordTooShort")),
      confirmPassword: (value, values) =>
        value === values.password ? null : t("passwordsDoNotMatch"),
    },
  });

  if (isInvalidLink) {
    return (
      <>
        <Text mb="md">{t("invalidLink")}</Text>
        <Text ta="center" mt="md">
          <Anchor href="/forgot-password" fw={500}>
            {t("requestNewLink")}
          </Anchor>
        </Text>
      </>
    );
  }

  const handleSubmit = async (values: ResetPasswordFormValues) => {
    setIsSubmitting(true);

    const { error } = await authClient.resetPassword({
      newPassword: values.password,
      token,
    });

    if (error) {
      setIsSubmitting(false);
      notifications.show({
        color: "red",
        title: t("errorTitle"),
        message: error.message ?? t("errorMessage"),
      });
      return;
    }

    notifications.show({
      color: "green",
      title: t("successTitle"),
      message: t("successMessage"),
    });
    router.push("/sign-in");
  };

  return (
    <form onSubmit={form.onSubmit(handleSubmit)}>
      <PasswordInput
        label={t("passwordLabel")}
        placeholder={t("passwordPlaceholder")}
        size="md"
        radius="md"
        key={form.key("password")}
        {...form.getInputProps("password")}
      />
      <PasswordInput
        label={t("confirmPasswordLabel")}
        placeholder={t("confirmPasswordPlaceholder")}
        mt="md"
        size="md"
        radius="md"
        key={form.key("confirmPassword")}
        {...form.getInputProps("confirmPassword")}
      />
      <Button type="submit" fullWidth mt="xl" size="md" radius="md" loading={isSubmitting}>
        {t("submitButton")}
      </Button>

      <Text ta="center" mt="md">
        <Anchor href="/sign-in" fw={500}>
          {t("backToSignIn")}
        </Anchor>
      </Text>
    </form>
  );
}
