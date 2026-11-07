import { Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewWrapper } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { assignableUsersQueryOptions } from "../../api/articles";

export const MentionView = ({ node }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const id = String(node.attrs.id ?? "");
  const stored = (node.attrs.label as string | null) ?? "";
  // The directory is searched by name, so the stored name finds the person; their current name is shown.
  const users = useQuery({ ...assignableUsersQueryOptions(stored), enabled: Boolean(stored) });
  const current = users.data?.find((user) => user.userId === id)?.displayName;
  return (
    <NodeViewWrapper as="span" data-type="mention" data-id={id} style={{ display: "inline" }}>
      <Text
        component="span"
        size="sm"
        fw={500}
        c="var(--mantine-primary-color-filled)"
        style={{
          padding: "0 4px",
          borderRadius: "var(--mantine-radius-sm)",
          background: "var(--mantine-primary-color-light)",
        }}
      >
        @{current ?? (stored || t("formerUser"))}
      </Text>
    </NodeViewWrapper>
  );
};
