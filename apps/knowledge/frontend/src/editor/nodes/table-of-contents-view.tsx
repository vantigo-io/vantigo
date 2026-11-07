import { Anchor, Stack, Text } from "@mantine/core";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewWrapper, useEditorState } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { documentHeadings, goToHeading } from "./headings";

export const TableOfContentsView = ({ editor }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const headings = useEditorState({ editor, selector: ({ editor: current }) => documentHeadings(current.state.doc) });
  return (
    <NodeViewWrapper
      contentEditable={false}
      data-type="tableOfContents"
      style={{
        margin: "var(--mantine-spacing-sm) 0",
        padding: "var(--mantine-spacing-xs) var(--mantine-spacing-sm)",
        borderLeft: "2px solid var(--mantine-color-default-border)",
      }}
    >
      <nav aria-label={t("tocLabel")}>
        {headings.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("tocEmpty")}
          </Text>
        ) : (
          <Stack gap={2}>
            {headings.map((heading) => (
              <Anchor
                key={`${heading.pos}-${heading.id ?? ""}`}
                href={heading.id ? `#${heading.id}` : undefined}
                size="sm"
                pl={(heading.level - 1) * 16}
                onClick={(event) => {
                  event.preventDefault();
                  goToHeading(editor, heading);
                }}
              >
                {heading.text}
              </Anchor>
            ))}
          </Stack>
        )}
      </nav>
    </NodeViewWrapper>
  );
};
