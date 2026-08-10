import { notifications } from "@mantine/notifications";
import { type ApiError, ApiValidationError } from "../api/request";

type FormLike = { setErrors: (errors: Record<string, string>) => void };

export const showLifecycleFormError = (error: unknown, form: FormLike, title: string) => {
  const apiError = error as ApiError & { code?: string };
  if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
  else if (apiError.code === "account_exists")
    form.setErrors({
      email: error instanceof Error ? error.message : "An account already exists for this email address.",
    });
  const message = error instanceof Error ? error.message : "The request could not be completed.";
  notifications.show({ color: "red", title, message });
};
