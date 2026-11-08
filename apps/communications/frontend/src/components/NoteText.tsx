import { Anchor, Text } from "@mantine/core";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { reviewItemIn } from "../lib/noteLinks";

/**
 * An internal note's text. A note that says something waits for review in the agents' queue
 * carries the link to it; it is shown as "Open in review".
 */
export function NoteText({ text }: { text: string }) {
  const { t } = useI18n("communications");
  const link = reviewItemIn(text);
  return (
    <Text mt="xs" style={{ whiteSpace: "pre-wrap" }}>
      {link ? (
        <>
          {link.before}{" "}
          <Anchor href={appUrl(`/agents/review/${link.id}`)} data-testid="review-link">
            {t("openInReview")}
          </Anchor>
        </>
      ) : (
        text
      )}
    </Text>
  );
}
