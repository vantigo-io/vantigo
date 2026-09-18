import { Stack, Text } from "@mantine/core";
import type { ReactNode } from "react";

export interface FieldProps {
  label: ReactNode;
  children: ReactNode;
}

/** One labelled read-only value in a detail card's grid. */
export const Field = ({ label, children }: FieldProps) => (
  <Stack gap={2}>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{children}</Text>
  </Stack>
);
