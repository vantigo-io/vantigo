import { Stack, Text } from "@mantine/core";
import type { ComponentType, ReactNode } from "react";

export interface EmptyStateProps {
  icon?: ComponentType<{ size?: number; color?: string }>;
  /** What is missing, e.g. "No customers found". */
  title: ReactNode;
  /** Why, or what to do about it. */
  description?: ReactNode;
  /** A button that fills the gap, e.g. "Add contact". */
  action?: ReactNode;
  /** `sm` inside a card section; `md` for a whole list or page body. */
  size?: "sm" | "md";
}

/**
 * The block every list, table and card shows when there is nothing to
 * show: nothing matched, or nothing exists yet. Centered, muted, with an
 * optional icon and action.
 */
export const EmptyState = ({ icon: Icon, title, description, action, size = "md" }: EmptyStateProps) => (
  <Stack align="center" gap="xs" py={size === "md" ? "xl" : "md"} ta="center" data-testid="empty-state">
    {Icon && <Icon size={size === "md" ? 38 : 28} color="var(--mantine-color-gray-5)" />}
    <Text c="dimmed" fw={500} size={size === "md" ? "md" : "sm"}>
      {title}
    </Text>
    {description && (
      <Text c="dimmed" size="sm">
        {description}
      </Text>
    )}
    {action}
  </Stack>
);
