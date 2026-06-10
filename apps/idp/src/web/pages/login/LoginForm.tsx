import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  Group,
  PasswordInput,
  Stack,
  TextInput,
} from "@mantine/core";
import type React from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authClient } from "../../auth";

interface LoginFormProps {
  redirectUrl?: string;
  type: "login" | "register";
}

export function LoginForm({ redirectUrl, type }: LoginFormProps) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    if (!email) {
      setError(t("errorEmailRequired"));
      return;
    }
    if (!password) {
      setError(t("errorPasswordRequired"));
      return;
    }
    if (type === "register" && !name) {
      setError(t("errorNameRequired"));
      return;
    }

    setLoading(true);
    try {
      if (type === "login") {
        const { error: signInError } = await authClient.signIn.email({
          email,
          password,
        });
        if (signInError) {
          setError(signInError.message || "Invalid credentials");
        } else {
          window.location.href = redirectUrl || "/idp/account";
        }
      } else {
        const { error: signUpError } = await authClient.signUp.email({
          email,
          password,
          name,
        });
        if (signUpError) {
          setError(signUpError.message || "Failed to register account");
        } else {
          window.location.href = "/idp/account";
        }
      }
    } catch (err) {
      const errorObject = err as Error;
      setError(errorObject.message || "An unexpected error occurred");
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      {error && (
        <Alert color="red" title="Error" mb="md" variant="light">
          {error}
        </Alert>
      )}

      <form onSubmit={handleSubmit}>
        <Stack>
          {type === "register" && (
            <TextInput
              label={t("name")}
              placeholder={t("placeholderName")}
              value={name}
              onChange={(event) => setName(event.currentTarget.value)}
              required
            />
          )}

          <TextInput
            label={t("email")}
            placeholder={t("placeholderEmail")}
            value={email}
            onChange={(event) => setEmail(event.currentTarget.value)}
            required
          />

          <PasswordInput
            label={t("password")}
            placeholder={t("placeholderPassword")}
            value={password}
            onChange={(event) => setPassword(event.currentTarget.value)}
            required
          />

          {type === "login" && (
            <Group justify="space-between" mt="xs">
              <Checkbox label={t("rememberMe")} />
              <Anchor component="button" size="sm" type="button">
                {t("forgotPassword")}
              </Anchor>
            </Group>
          )}

          <Button type="submit" fullWidth mt="xl" loading={loading}>
            {type === "login" ? t("signIn") : t("signUp")}
          </Button>
        </Stack>
      </form>
    </>
  );
}
