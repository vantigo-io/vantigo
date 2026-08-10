import { createFileRoute } from "@tanstack/react-router";
import { ResetPasswordPage } from "./reset-password";

const PasswordResetAliasRoute = () => {
  const search = Route.useSearch();
  return <ResetPasswordPage email={search.email} token={search.token} />;
};
export const Route = createFileRoute("/password-reset")({
  validateSearch: (search: Record<string, unknown>) => ({
    email: String(search.email ?? ""),
    token: String(search.token ?? ""),
  }),
  component: PasswordResetAliasRoute,
});
