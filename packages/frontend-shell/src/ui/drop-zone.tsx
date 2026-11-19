import { Box, type BoxProps } from "@mantine/core";
import { type DragEvent, type ReactNode, useState } from "react";

export interface DropZoneProps extends BoxProps {
  /** Called with the files dropped on the zone, which may be empty. */
  onDropFiles: (files: File[]) => void;
  /** Draws the border in red, for a zone whose form field has an error. */
  invalid?: boolean;
  role?: string;
  "aria-label"?: string;
  children: ReactNode;
}

/**
 * A dashed area that accepts dropped files. It holds the native file inputs or
 * buttons the caller already renders, so choosing a file by click keeps working.
 * The border and the highlight follow the colour scheme.
 */
export function DropZone({ onDropFiles, invalid, style, children, ...rest }: DropZoneProps) {
  const [over, setOver] = useState(false);
  return (
    <Box
      {...rest}
      style={[
        {
          border: `1px dashed ${invalid ? "var(--mantine-color-red-6)" : "var(--mantine-color-default-border)"}`,
          borderRadius: "var(--mantine-radius-sm)",
          background: over ? "light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6))" : undefined,
        },
        style,
      ]}
      onDragOver={(event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        setOver(false);
        onDropFiles(Array.from(event.dataTransfer?.files ?? []));
      }}
    >
      {children}
    </Box>
  );
}
