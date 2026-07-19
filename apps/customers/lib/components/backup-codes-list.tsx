"use client";

import { Button, Code, CopyButton, Group, SimpleGrid } from "@mantine/core";
import { IconCopy, IconDownload } from "@tabler/icons-react";
import { useTranslations } from "next-intl";

/** Backup-code grid with copy and download actions. */
export function BackupCodesList({ codes }: Readonly<{ codes: string[] }>) {
  const t = useTranslations("twoFactor.backupCodes");

  const download = () => {
    const blob = new Blob([codes.join("\n")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "vantigo-backup-codes.txt";
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <>
      <SimpleGrid cols={2} spacing="xs" data-testid="backup-codes">
        {codes.map((code) => (
          <Code key={code} ta="center">
            {code}
          </Code>
        ))}
      </SimpleGrid>

      <Group>
        <CopyButton value={codes.join("\n")}>
          {({ copied, copy }) => (
            <Button variant="default" size="xs" leftSection={<IconCopy size={14} />} onClick={copy}>
              {copied ? t("copied") : t("copy")}
            </Button>
          )}
        </CopyButton>
        <Button
          variant="default"
          size="xs"
          leftSection={<IconDownload size={14} />}
          onClick={download}
        >
          {t("download")}
        </Button>
      </Group>
    </>
  );
}
