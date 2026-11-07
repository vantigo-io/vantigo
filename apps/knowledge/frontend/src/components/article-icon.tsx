import { IconFileText } from "@tabler/icons-react";

/** An article's emoji, or the plain page glyph when it has none. */
export const ArticleIcon = ({ icon, size = 16 }: { icon: string | null | undefined; size?: number }) =>
  icon ? (
    <span aria-hidden style={{ fontSize: size, lineHeight: 1, width: size + 2, display: "inline-block" }}>
      {icon}
    </span>
  ) : (
    <IconFileText aria-hidden size={size} stroke={1.5} style={{ flex: "none" }} />
  );
