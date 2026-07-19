"use client";

import { Combobox, Loader, TextInput, useCombobox } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconBuildingBank } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import type { BrregCompany } from "@vantigo/customers/lib/customers/brreg";
import { useTranslations } from "next-intl";
import { useState } from "react";

async function searchBrreg(query: string): Promise<BrregCompany[]> {
  const response = await fetch(`/api/brreg/search?q=${encodeURIComponent(query)}`);
  if (!response.ok) throw new Error("BRREG_UNAVAILABLE");
  const { results } = (await response.json()) as { results: BrregCompany[] };
  return results;
}

/**
 * Company search against Brønnøysundregistrene. Selecting a result is the
 * only thing that fills form fields — typing here never touches the form.
 */
export function BrregSearch({ onSelect }: Readonly<{ onSelect: (company: BrregCompany) => void }>) {
  const t = useTranslations("customers.form");
  const combobox = useCombobox();
  const [value, setValue] = useState("");
  const [debounced] = useDebouncedValue(value, 300);

  const enabled = debounced.trim().length >= 2;
  const {
    data: results,
    isFetching,
    isError,
  } = useQuery({
    queryKey: ["brreg", debounced],
    queryFn: () => searchBrreg(debounced),
    enabled,
    staleTime: 60_000,
    retry: false,
  });

  const options = (results ?? []).map((company) => (
    <Combobox.Option value={company.orgnr} key={company.orgnr}>
      {company.name}{" "}
      <span style={{ opacity: 0.6 }}>
        — {company.orgnr}
        {company.city ? ` (${company.city})` : ""}
      </span>
    </Combobox.Option>
  ));

  return (
    <Combobox
      store={combobox}
      withinPortal={false}
      onOptionSubmit={(orgnr) => {
        const company = results?.find((candidate) => candidate.orgnr === orgnr);
        if (company) {
          onSelect(company);
          setValue("");
        }
        combobox.closeDropdown();
      }}
    >
      <Combobox.Target>
        <TextInput
          label={t("brregSearch")}
          placeholder={t("brregSearchPlaceholder")}
          description={t("brregSearchDescription")}
          leftSection={<IconBuildingBank size={16} />}
          rightSection={isFetching ? <Loader size="xs" /> : null}
          value={value}
          onChange={(event) => {
            setValue(event.currentTarget.value);
            combobox.openDropdown();
          }}
          onFocus={() => combobox.openDropdown()}
          onBlur={() => combobox.closeDropdown()}
          error={isError ? t("brregSearchError") : undefined}
        />
      </Combobox.Target>

      <Combobox.Dropdown hidden={!enabled || isFetching || isError}>
        <Combobox.Options>
          {options.length > 0 ? options : <Combobox.Empty>{t("brregSearchEmpty")}</Combobox.Empty>}
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  );
}
