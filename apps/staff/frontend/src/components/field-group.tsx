import { Card, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";

export interface FieldGroupProps {
  /** The group's name, which is also the section's accessible name. */
  title: string;
  description?: ReactNode;
  /** What the caller may do with the group — a button, or nothing. */
  actions?: ReactNode;
  children: ReactNode;
}

/**
 * One field group of design §5 — Work, Private, HR's notes … — as a labelled
 * section. A page renders a group only when the response carries it, so the
 * groups on screen are exactly what the server let this reader see.
 */
export const FieldGroup = ({ title, description, actions, children }: FieldGroupProps) => (
  <Card withBorder radius="md" padding="lg" component="section" aria-label={title}>
    <Stack gap="sm">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
        <div>
          <Title order={3} size="h4">
            {title}
          </Title>
          {description && (
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          )}
        </div>
        {actions}
      </Group>
      {children}
    </Stack>
  </Card>
);

/** A label and its value; a value of undefined, null or "" is a field the response did not carry. */
export type FieldItem = readonly [label: string, value: ReactNode];

const carried = ([, value]: FieldItem) => value !== undefined && value !== null && value !== "" && value !== false;

/**
 * Fields as a description list: one column on a phone, two from `sm` up.
 * A field without a value is left out rather than drawn empty — absent from
 * the response is absent from the page.
 */
export const Fields = ({ items }: { items: readonly FieldItem[] }) => {
  const shown = items.filter(carried);
  if (shown.length === 0) return null;
  return (
    <SimpleGrid component="dl" cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="xs" m={0}>
      {shown.map(([label, value]) => (
        <div key={label}>
          <Text component="dt" size="xs" c="dimmed">
            {label}
          </Text>
          <Text component="dd" m={0} size="sm" style={{ overflowWrap: "anywhere" }}>
            {value}
          </Text>
        </div>
      ))}
    </SimpleGrid>
  );
};
