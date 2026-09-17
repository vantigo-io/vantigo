import { notifications } from "@mantine/notifications";

export const mfaKey = ["account", "mfa"] as const;
export const passkeyKey = ["account", "passkeys"] as const;
export const message = (e: unknown, fallback = "The request could not be completed.") =>
  e instanceof Error ? e.message : fallback;
export const isOidcError = (e: unknown) => {
  const value = e as { code?: string };
  return value.code === "local_password_unavailable";
};
export const notify = (title: string, e: unknown, fallback?: string) =>
  notifications.show({ title, message: message(e, fallback), color: "red" });
