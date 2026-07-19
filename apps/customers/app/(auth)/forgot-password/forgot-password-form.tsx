"use client";

import { Anchor, Button, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useTranslations } from "next-intl";
import { useState } from "react";

interface ForgotPasswordFormValues {
  email: string;
}

export function ForgotPasswordForm() {
  const t = useTranslations("forgotPassword");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const form = useForm<ForgotPasswordFormValues>({
    mode: "uncontrolled",
    initialValues: {
      email: "",
    },
    validate: {
      email: (value) => (/^\S+@\S+\.\S+$/.test(value) ? null : t("invalidEmail")),
    },
  });

  const handleSubmit = async (values: ForgotPasswordFormValues) => {
    setIsSubmitting(true);

    const { error } = await authClient.requestPasswordReset({
      email: values.email,
      redirectTo: "/reset-password",
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

    // Same message whether or not the account exists (no account enumeration).
    notifications.show({
      color: "green",
      title: t("successTitle"),
      message: t("successMessage"),
    });
    form.reset();
  };

  return (
    <form onSubmit={form.onSubmit(handleSubmit)}>
      <Text mb="md">{t("description")}</Text>

      <TextInput
        label={t("emailLabel")}
        placeholder={t("emailPlaceholder")}
        size="md"
        radius="md"
        key={form.key("email")}
        {...form.getInputProps("email")}
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
