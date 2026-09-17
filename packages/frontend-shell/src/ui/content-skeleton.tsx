import { type MantineSpacing, Skeleton, Stack } from "@mantine/core";

export interface ContentSkeletonProps {
  /** Placeholder rows; roughly the number of items the content will show. */
  rows?: number;
  /** Row height in pixels: ~52 for table rows or cards, ~32 for compact lists. */
  rowHeight?: number;
  /** Padding around the rows, for skeletons inside an unpadded card. */
  p?: MantineSpacing;
}

/**
 * The placeholder every page and card body shows while its data loads.
 * Spinners (`Loader`) are for inline waits only: a button, an input's
 * right section, the spotlight.
 */
export const ContentSkeleton = ({ rows = 3, rowHeight = 40, p }: ContentSkeletonProps) => (
  <Stack gap="sm" p={p} aria-busy="true" data-testid="content-skeleton">
    {Array.from({ length: rows }, (_, index) => (
      <Skeleton key={index} height={rowHeight} radius="sm" />
    ))}
  </Stack>
);
