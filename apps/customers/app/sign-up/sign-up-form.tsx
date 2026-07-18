"use client";

import { Anchor, Button, PasswordInput, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

interface SignUpFormValues {
  name: string;
  email: string;
  password: string;
  confirmPassword: string;
}

export function SignUpForm() {
  const t = useTranslations("signUp");
  const router = useRouter();
  const [isSubmitting, setIsSubmitting] = useState(false);

  const form = useForm<SignUpFormValues>({
    mode: "uncontrolled",
    initialValues: {
      name: "",
      email: "",
      password: "",
      confirmPassword: "",
    },
    validate: {
      name: (value) => (value.trim().length > 0 ? null : t("nameRequired")),
      email: (value) => (/^\S+@\S+\.\S+$/.test(value) ? null : t("invalidEmail")),
      password: (value) => (value.length >= 8 ? null : t("passwordTooShort")),
      confirmPassword: (value, values) =>
        value === values.password ? null : t("passwordsDoNotMatch"),
    },
  });

  const handleSubmit = async (values: SignUpFormValues) => {
    setIsSubmitting(true);

    const { error } = await authClient.signUp.email({
      name: values.name.trim(),
      email: values.email,
      password: values.password,
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
    router.push("/");
    router.refresh();
  };

  return (
    <form onSubmit={form.onSubmit(handleSubmit)}>
      <TextInput
        label={t("nameLabel")}
        placeholder={t("namePlaceholder")}
        size="md"
        radius="md"
        key={form.key("name")}
        {...form.getInputProps("name")}
      />
      <TextInput
        label={t("emailLabel")}
        placeholder={t("emailPlaceholder")}
        mt="md"
        size="md"
        radius="md"
        key={form.key("email")}
        {...form.getInputProps("email")}
      />
      <PasswordInput
        label={t("passwordLabel")}
        placeholder={t("passwordPlaceholder")}
        mt="md"
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
        {t("signUpButton")}
      </Button>

      <Text ta="center" mt="md">
        {t("haveAccount")}{" "}
        <Anchor href="/sign-in" fw={500}>
          {t("signIn")}
        </Anchor>
      </Text>
    </form>
  );
}
