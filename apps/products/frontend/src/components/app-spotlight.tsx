import { Center, Loader, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { Spotlight } from "@mantine/spotlight";
import { IconPackage, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import { productsQueryOptions } from "../api/products";

const MIN_SEARCH_LENGTH = 2;
const MAX_RESULTS = 5;

export const AppSpotlight = () => {
  const { t } = useI18n("products");
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [debouncedQuery] = useDebouncedValue(query, 300);
  const search = debouncedQuery.trim();
  const searchEnabled = search.length >= MIN_SEARCH_LENGTH;
  const products = useQuery({ ...productsQueryOptions({ search, pageSize: MAX_RESULTS }), enabled: searchEnabled });
  const productResults = searchEnabled ? (products.data?.data ?? []) : [];
  const isSearching = searchEnabled && products.isFetching;

  return (
    <Spotlight.Root shortcut="mod + K" query={query} onQueryChange={setQuery} onSpotlightClose={() => setQuery("")}>
      <Spotlight.Search
        placeholder={t("spotlight.searchPlaceholder")}
        leftSection={<IconSearch size={20} stroke={1.5} />}
        rightSection={isSearching && <Loader size="xs" />}
      />
      <Spotlight.ActionsList>
        {productResults.length > 0 && (
          <Spotlight.ActionsGroup label={t("spotlight.group")}>
            {productResults.map((product) => (
              <Spotlight.Action
                key={product.id}
                label={product.name}
                description={`${product.sku} · ${t(`type.${product.type}`)}`}
                leftSection={<IconPackage size={20} stroke={1.5} />}
                onClick={() => navigate({ to: "/products/$productId", params: { productId: product.id } })}
              />
            ))}
          </Spotlight.ActionsGroup>
        )}
        {productResults.length === 0 &&
          (isSearching ? (
            <Center py="md">
              <Loader size="sm" />
            </Center>
          ) : (
            <Spotlight.Empty>
              <Text c="dimmed" size="sm">
                {searchEnabled ? t("spotlight.nothingFound") : t("spotlight.searchForProduct")}
              </Text>
            </Spotlight.Empty>
          ))}
      </Spotlight.ActionsList>
    </Spotlight.Root>
  );
};
