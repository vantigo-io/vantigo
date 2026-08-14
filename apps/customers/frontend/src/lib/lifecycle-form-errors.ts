import { notifications } from "@mantine/notifications";
import { type ApiError, ApiValidationError } from "../api/request";

type FormLike = { setErrors: (errors: Record<string, string>) => void };

export const showLifecycleFormError = (
  error: unknown,
  form: FormLike,
  title: string,
  fallback: { accountExists: string; request: string },
) => {
  const apiError = error as ApiError & { code?: string };
  if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
  else if (apiError.code === "account_exists")
    form.setErrors({
      email: error instanceof Error ? error.message : fallback.accountExists,
    });
  const message = error instanceof Error ? error.message : fallback.request;
  notifications.show({ color: "red", title, message });
};
