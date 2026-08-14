import { DatesProvider } from "@mantine/dates";
import { type SupportedLocale, useLocale } from "@vantigo/frontend-shell";
import "dayjs/locale/en";
import "dayjs/locale/nb";
import type { ReactNode } from "react";

const dayjsLocales: Record<SupportedLocale, string> = {
  en: "en",
  nb: "nb",
};

interface LocaleDatesProviderProps {
  children: ReactNode;
}

export const LocaleDatesProvider = ({ children }: LocaleDatesProviderProps) => {
  const locale = useLocale();

  return <DatesProvider settings={{ locale: dayjsLocales[locale] }}>{children}</DatesProvider>;
};
