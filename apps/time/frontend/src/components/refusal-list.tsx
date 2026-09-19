import { Stack, Text } from "@mantine/core";
import { refusalMessages } from "../lib/errors";

export interface RefusalListProps {
  error: Error;
}

/**
 * Every sentence a refusal carried. Approving, rejecting and unapproving are
 * all or nothing and answer one message per offending entry, so the caller is
 * shown all of them rather than the first — they name the ids that have to be
 * taken out of the batch.
 */
export const RefusalList = ({ error }: RefusalListProps) => (
  <Stack gap={2}>
    {refusalMessages(error).map((message) => (
      <Text key={message} size="sm">
        {message}
      </Text>
    ))}
  </Stack>
);
