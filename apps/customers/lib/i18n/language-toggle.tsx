"use client";

import { SegmentedControl } from "@mantine/core";
import { setLanguage } from "@vantigo/customers/lib/i18n/actions";
import { localeNames, locales } from "@vantigo/customers/lib/i18n/locales";
import { useLocale } from "next-intl";
import { useTransition } from "react";

export function LanguageToggle() {
  const locale = useLocale();
  const [isPending, startTransition] = useTransition();

  return (
    <SegmentedControl
      size="xs"
      value={locale}
      disabled={isPending}
      data={locales.map((value) => ({ value, label: localeNames[value] }))}
      onChange={(value) => {
        startTransition(async () => {
          await setLanguage(value);
        });
      }}
    />
  );
}
