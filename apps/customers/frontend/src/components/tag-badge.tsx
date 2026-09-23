import { Badge } from "@mantine/core";
import type { CustomerTag } from "../api/tags";

/**
 * One tag, as a chip (owner and tags design D2). The list's rows and the
 * customer page's Relationship card show the same thing, and a tag's colour is
 * half of how it is recognised at a glance — so the chip is one component
 * rather than the same three props written out in two places and drifting.
 *
 * `color` is already one of Mantine's named colours or null, because the API
 * validates it against exactly that list, so it goes straight to the `Badge`;
 * a tag with no colour is grey.
 */
export const TagBadge = ({ tag }: { tag: CustomerTag }) => (
  <Badge variant="light" color={tag.color ?? "gray"} size="sm">
    {tag.name}
  </Badge>
);
