import { Badge } from "@mantine/core";

/**
 * The work type an entry was logged as (work types design D5), shown after
 * the trackable code: a name, never the multiplier — that is money-adjacent
 * and lives in the billing block's rate line.
 */
export const WorkTypeBadge = ({ name }: { name: string }) => (
  <Badge variant="light" color="grape" size="sm" tt="none" data-testid="work-type-badge">
    {name}
  </Badge>
);
