"use client";

import { notifications } from "@mantine/notifications";
import { useRouter } from "next/navigation";
import { useState } from "react";

interface AuthActionResult {
  error?: { message?: string | null } | null;
}

interface RunOptions {
  /** Green notification on success. */
  success?: string;
  /** Fallback red notification when the API returns no message. */
  error: string;
  /** Refresh server components after success (default true). */
  refresh?: boolean;
  onSuccess?: () => void;
}

/**
 * Shared submit-state + notification + refresh handling for authClient calls.
 * Returns the action result on success, or null when it failed.
 */
export function useAuthAction() {
  const router = useRouter();
  const [isPending, setIsPending] = useState(false);

  const run = async <T extends AuthActionResult>(
    action: () => Promise<T>,
    options: RunOptions,
  ): Promise<T | null> => {
    setIsPending(true);
    const result = await action();
    setIsPending(false);

    if (result.error) {
      notifications.show({ color: "red", message: result.error.message ?? options.error });
      return null;
    }

    if (options.success) {
      notifications.show({ color: "green", message: options.success });
    }
    options.onSuccess?.();
    if (options.refresh !== false) {
      router.refresh();
    }
    return result;
  };

  return { run, isPending };
}
