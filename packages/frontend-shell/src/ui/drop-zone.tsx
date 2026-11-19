import { Box, type BoxProps } from "@mantine/core";
import { type DragEvent, type ReactNode, useState } from "react";

export interface DropZoneProps extends BoxProps {
  /** Called with the files dropped on the zone, which may be empty. */
  onDropFiles: (files: File[]) => void;
  /** Draws the border in red, for a zone whose form field has an error. */
  invalid?: boolean;
  /** Neither highlights nor accepts a drop, for a zone that is busy. */
  disabled?: boolean;
  role?: string;
  "aria-label"?: string;
  children: ReactNode;
}

const carriesFiles = (event: DragEvent<HTMLDivElement>) =>
  Array.from(event.dataTransfer?.types ?? []).includes("Files");

/**
 * A dashed area that accepts dropped files. It holds the native file inputs or
 * buttons the caller already renders, so choosing a file by click keeps working.
 * The border and the highlight follow the colour scheme. `data-over`,
 * `data-invalid` and `data-disabled` expose its state.
 */
export function DropZone({ onDropFiles, invalid, disabled, style, children, ...rest }: DropZoneProps) {
  const [over, setOver] = useState(false);
  const highlighted = over && !disabled;
  return (
    <Box
      {...rest}
      data-over={highlighted || undefined}
      data-invalid={invalid || undefined}
      data-disabled={disabled || undefined}
      style={[
        {
          border: `1px dashed ${invalid ? "var(--mantine-color-red-6)" : "var(--mantine-color-default-border)"}`,
          borderRadius: "var(--mantine-radius-sm)",
          background: highlighted ? "light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6))" : undefined,
        },
        style,
      ]}
      onDragOver={(event: DragEvent<HTMLDivElement>) => {
        if (disabled || !carriesFiles(event)) return;
        event.preventDefault();
        setOver(true);
      }}
      onDragLeave={(event: DragEvent<HTMLDivElement>) => {
        // Moving onto a child of the zone also fires a leave on the zone.
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOver(false);
      }}
      onDrop={(event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        setOver(false);
        if (disabled) return;
        onDropFiles(Array.from(event.dataTransfer?.files ?? []));
      }}
    >
      {children}
    </Box>
  );
}
