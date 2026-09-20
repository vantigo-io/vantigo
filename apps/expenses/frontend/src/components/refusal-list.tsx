import { Stack, Text } from "@mantine/core";

export interface RefusalListProps {
  messages: string[];
}

/**
 * Every sentence a refusal carried. Submitting is all or nothing and answers
 * one message per offending expense, so the caller is shown all of them
 * rather than the first — each one names what has to be put right.
 */
export const RefusalList = ({ messages }: RefusalListProps) =>
  messages.length === 0 ? null : (
    <Stack gap={2}>
      {messages.map((message) => (
        <Text key={message} size="sm" c="red">
          {message}
        </Text>
      ))}
    </Stack>
  );
