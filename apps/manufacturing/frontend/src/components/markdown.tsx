import { Anchor, Table, Typography } from "@mantine/core";
import type { ComponentPropsWithoutRef } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

const plugins = [remarkGfm];

const components: Components = {
  a: ({ href, children }: ComponentPropsWithoutRef<"a">) => (
    <Anchor href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </Anchor>
  ),
  table: ({ children }: ComponentPropsWithoutRef<"table">) => (
    <Table.ScrollContainer minWidth={0}>
      <Table withTableBorder withColumnBorders>
        {children}
      </Table>
    </Table.ScrollContainer>
  ),
  thead: ({ children }: ComponentPropsWithoutRef<"thead">) => <Table.Thead>{children}</Table.Thead>,
  tbody: ({ children }: ComponentPropsWithoutRef<"tbody">) => <Table.Tbody>{children}</Table.Tbody>,
  tr: ({ children }: ComponentPropsWithoutRef<"tr">) => <Table.Tr>{children}</Table.Tr>,
  th: ({ children }: ComponentPropsWithoutRef<"th">) => <Table.Th>{children}</Table.Th>,
  td: ({ children }: ComponentPropsWithoutRef<"td">) => <Table.Td>{children}</Table.Td>,
};

/**
 * An operation's instructions (M31), Markdown with GitHub's tables, as the
 * Assistant renders its answers. Raw HTML is never rendered — no plugin parses
 * it — and images are dropped, their alt text kept; a link opens in a new tab.
 */
export const Markdown = ({ children }: { children: string }) => (
  <Typography fz="md">
    <ReactMarkdown remarkPlugins={plugins} components={components} disallowedElements={["img"]} unwrapDisallowed>
      {children}
    </ReactMarkdown>
  </Typography>
);
