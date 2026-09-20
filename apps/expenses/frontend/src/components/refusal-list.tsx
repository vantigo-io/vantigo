import { Group, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";

export interface RefusalListProps {
  messages: string[];
}

/**
 * Every sentence a refusal carried. Submitting is all or nothing and answers
 * one message per offending unit, so the caller is shown all of them rather
 * than the first — each one names what has to be put right.
 *
 * It is a **live region with an icon**, not red text: several of these are the
 * only thing that reports a failure (a per diem row's meal tick writes on
 * change and raises no notification), so a reader who cannot see the colour
 * has to be told, and told when it appears rather than when they next tab
 * past it.
 */
export const RefusalList = ({ messages }: RefusalListProps) =>
  messages.length === 0 ? null : (
    <Stack gap={2} role="alert">
      {messages.map((message) => (
        <Group key={message} gap={4} wrap="nowrap" align="start">
          <IconAlertCircle size={14} color="var(--mantine-color-red-6)" style={{ flexShrink: 0, marginTop: 2 }} />
          <Text size="sm" c="red">
            {message}
          </Text>
        </Group>
      ))}
    </Stack>
  );
