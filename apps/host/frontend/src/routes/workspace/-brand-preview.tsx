import { Box, Group, SimpleGrid, Stack, Text } from "@mantine/core";
import { type BrandTokens, cssVariables, useI18n } from "@vantigo/frontend-shell";
import type { CSSProperties, ReactNode } from "react";
import "../../i18n";

/**
 * The colours of the token layer (theme.css) the brand does not change (brands design B2). The preview
 * paints them as literals because it shows the other colour scheme than the page does, and a variable
 * would resolve to the page's.
 */
const NEUTRAL = {
  light: { page: "#f7f8fc", surface: "#ffffff", text: "#182033", dimmed: "#5c6577", border: "#d5d9e4" },
  dark: { page: "#15182a", surface: "#1c1f32", text: "#dde1ef", dimmed: "#8c93ad", border: "#3d4361" },
} as const;

type Scheme = "light" | "dark";

interface PreviewProps {
  tokens: BrandTokens;
  scheme: Scheme;
  title: string;
  /** The brand mark for this scheme, if the brand has one. */
  mark?: string;
  icon?: string;
}

/** A button the way the brand paints it: shade 6 with the filled text colour. */
const Fake = ({ style, children }: { style: CSSProperties; children: ReactNode }) => (
  <Box component="span" style={{ display: "inline-block", ...style }}>
    {children}
  </Box>
);

/** One colour scheme of a mini shell: header with the logo, a button, a table, a badge, a link. */
const SchemePreview = ({ tokens, scheme, title, mark, icon }: PreviewProps) => {
  const { t } = useI18n("host");
  const n = NEUTRAL[scheme];
  const s = tokens[scheme];
  const vars = cssVariables(tokens, scheme);
  const radius = "var(--mantine-radius-default)";
  const light = scheme === "light";
  return (
    <Box
      data-testid={`brand-preview-${scheme}`}
      data-scheme={scheme}
      style={
        {
          ...vars,
          background: n.page,
          color: n.text,
          border: `1px solid ${n.border}`,
          borderRadius: radius,
          overflow: "hidden",
          fontSize: 13,
        } as CSSProperties
      }
    >
      <Group
        justify="space-between"
        px="sm"
        py={8}
        style={{ background: n.surface, borderBottom: `1px solid ${n.border}` }}
      >
        <Group gap="xs">
          {mark ? (
            <img src={mark} alt="" style={{ height: 22, width: "auto", display: "block" }} />
          ) : (
            <Box w={22} h={22} style={{ background: s.shades[6], borderRadius: radius }} />
          )}
          <Text fw={600} size="sm" c="inherit">
            {title}
          </Text>
        </Group>
        <Text size="xs" style={{ color: n.dimmed }}>
          {t(`brands.preview.${scheme}`)}
        </Text>
      </Group>
      <Group align="stretch" gap={0} wrap="nowrap">
        <Stack gap={4} p="xs" w={96} style={{ background: n.surface, borderRight: `1px solid ${n.border}` }}>
          <Fake
            style={{ background: s.subtleBg, color: s.link, borderRadius: radius, padding: "3px 8px", fontWeight: 600 }}
          >
            {t("brands.preview.dashboard")}
          </Fake>
          <Fake style={{ padding: "3px 8px", color: n.dimmed }}>{t("brands.preview.customers")}</Fake>
          <Fake style={{ padding: "3px 8px", color: n.dimmed }}>{t("brands.preview.invoices")}</Fake>
        </Stack>
        <Stack gap="xs" p="sm" style={{ flex: 1 }}>
          <Group gap="xs">
            <Fake
              style={{
                background: s.shades[6],
                color: s.filledText,
                borderRadius: radius,
                padding: "5px 12px",
                fontWeight: 600,
              }}
            >
              {t("brands.preview.primary")}
            </Fake>
            <Fake
              style={{
                background: s.shades[7],
                color: s.filledText,
                borderRadius: radius,
                padding: "5px 12px",
                fontWeight: 600,
                opacity: 0.9,
              }}
            >
              {t("brands.preview.primary")}
            </Fake>
            <Fake
              style={{
                border: `1px solid ${n.border}`,
                background: n.surface,
                color: n.text,
                borderRadius: radius,
                padding: "5px 12px",
              }}
            >
              {t("brands.preview.secondary")}
            </Fake>
          </Group>
          <Box
            component="table"
            style={{
              width: "100%",
              borderCollapse: "collapse",
              background: n.surface,
              border: `1px solid ${n.border}`,
            }}
          >
            <thead>
              <tr style={{ color: n.dimmed, textAlign: "left" }}>
                <th style={{ padding: "4px 8px", fontWeight: 500 }}>{t("brands.preview.customer")}</th>
                <th style={{ padding: "4px 8px", fontWeight: 500 }}>{t("brands.preview.status")}</th>
                <th style={{ padding: "4px 8px", fontWeight: 500 }}>{t("brands.preview.amount")}</th>
              </tr>
            </thead>
            <tbody>
              <tr style={{ background: s.subtleBg }}>
                <td style={{ padding: "4px 8px", color: s.link }}>{t("brands.preview.link")}</td>
                <td style={{ padding: "4px 8px" }}>
                  <Fake
                    style={{
                      background: light ? s.shades[1] : s.derived["--mantine-color-vantigo-light"],
                      color: light ? s.shades[9] : s.shades[0],
                      borderRadius: 999,
                      padding: "1px 8px",
                      fontSize: 11,
                      fontWeight: 600,
                    }}
                  >
                    {t("brands.preview.paid")}
                  </Fake>
                </td>
                <td style={{ padding: "4px 8px" }}>12 400</td>
              </tr>
              <tr>
                <td style={{ padding: "4px 8px" }}>
                  <span style={{ color: s.link, textDecoration: "underline" }}>{t("brands.preview.link")}</span>
                </td>
                <td style={{ padding: "4px 8px", color: n.dimmed }}>—</td>
                <td style={{ padding: "4px 8px" }}>3 900</td>
              </tr>
            </tbody>
          </Box>
        </Stack>
      </Group>
      <Group
        gap={6}
        px="sm"
        py={4}
        data-testid={`brand-tab-${scheme}`}
        style={{ background: light ? "#e8eaf0" : "#0f1120", color: light ? "#182033" : "#dde1ef", fontSize: 11 }}
      >
        {icon ? (
          <img src={icon} alt="" style={{ height: 14, width: 14, display: "block" }} />
        ) : (
          <Box w={14} h={14} style={{ background: s.shades[6], borderRadius: 3 }} />
        )}
        <span>{title}</span>
        <span style={{ opacity: 0.6 }}>{t(light ? "brands.preview.tabLight" : "brands.preview.tabDark")}</span>
      </Group>
    </Box>
  );
};

/** The live preview of a brand's draft in both colour schemes side by side (brands design B21). */
export const BrandPreview = ({
  tokens,
  title,
  logoFor,
}: {
  tokens: BrandTokens | null;
  title: string;
  /** The URLs of the logos being edited, by slot. */
  logoFor: (slot: "mark_light" | "mark_dark" | "icon") => string | undefined;
}) => {
  const { t } = useI18n("host");
  if (!tokens) {
    return (
      <Text c="dimmed" size="sm">
        {t("brands.preview.invalidColour")}
      </Text>
    );
  }
  return (
    <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
      <SchemePreview tokens={tokens} scheme="light" title={title} mark={logoFor("mark_light")} icon={logoFor("icon")} />
      <SchemePreview
        tokens={tokens}
        scheme="dark"
        title={title}
        mark={logoFor("mark_dark") ?? logoFor("mark_light")}
        icon={logoFor("icon")}
      />
    </SimpleGrid>
  );
};
