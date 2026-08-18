import { Group, Text, Title } from "@mantine/core";
import type { CSSProperties, ReactNode } from "react";

const eyebrowStyle: CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  letterSpacing: "0.12em",
  textTransform: "uppercase",
  color: "var(--mantine-color-vantigo-5)",
};

export interface PageHeaderProps {
  /** Module name, e.g. "Communications" */
  eyebrow: string;
  /** Page name, ideally matching the navigation label */
  title: ReactNode;
  /** One or two lines helping a first-time user understand the page */
  description?: ReactNode;
  /** Optional right-aligned content (badges, action buttons, ...) */
  actions?: ReactNode;
}

export function PageHeader({ eyebrow, title, description, actions }: PageHeaderProps) {
  return (
    <Group justify="space-between" align="end">
      <div>
        <Text style={eyebrowStyle}>{eyebrow}</Text>
        <Title order={2}>{title}</Title>
        {description && (
          <Text c="dimmed" component="div">
            {description}
          </Text>
        )}
      </div>
      {actions}
    </Group>
  );
}
