import { Mark, Text } from "@mantine/core";
import type { KnowledgeExcerptSegment } from "../api/articles";

/**
 * A search hit's excerpt (knowledge design K12): plain-text runs, the matched
 * words highlighted. Every run is rendered as text, never as markup, so an
 * article that contains something tag-like shows it as written.
 */
export const Excerpt = ({ segments }: { segments: readonly KnowledgeExcerptSegment[] }) => (
  <Text size="xs" c="dimmed" lineClamp={2}>
    {segments.map((segment, index) =>
      segment.match ? <Mark key={index}>{segment.text}</Mark> : <span key={index}>{segment.text}</span>,
    )}
  </Text>
);
