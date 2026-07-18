"use client";

import { Anchor, Button, Checkbox, PasswordInput, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

interface SignInFormValues {
  email: string;
  password: string;
  rememberMe: boolean;
}

export function SignInForm() {
  const t = useTranslations("signIn");
  const router = useRouter();
  const [isSubmitting, setIsSubmitting] = useState(false);

  const form = useForm<SignInFormValues>({
    mode: "uncontrolled",
    initialValues: {
      email: "",
      password: "",
      rememberMe: true,
    },
    validate: {
      email: (value) => (/^\S+@\S+\.\S+$/.test(value) ? null : t("invalidEmail")),
      password: (value) => (value.length > 0 ? null : t("passwordRequired")),
    },
  });

  const handleSubmit = async (values: SignInFormValues) => {
    setIsSubmitting(true);

    const { error } = await authClient.signIn.email({
      email: values.email,
      password: values.password,
      rememberMe: values.rememberMe,
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
        label={t("emailLabel")}
        placeholder={t("emailPlaceholder")}
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
      <Checkbox
        label={t("keepLoggedIn")}
        mt="xl"
        size="md"
        key={form.key("rememberMe")}
        {...form.getInputProps("rememberMe", { type: "checkbox" })}
      />
      <Text mt="md">
        <Anchor href="/forgot-password" fw={500}>
          {t("forgotPassword")}
        </Anchor>
      </Text>
      <Button type="submit" fullWidth mt="xl" size="md" radius="md" loading={isSubmitting}>
        {t("loginButton")}
      </Button>

      <Text ta="center" mt="md">
        {t("noAccount")}{" "}
        <Anchor href="/sign-up" fw={500}>
          {t("register")}
        </Anchor>
      </Text>
    </form>
  );
}
