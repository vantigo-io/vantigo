#!/usr/bin/env bash
# The VAT return oracle: Skatteetaten's skattemeldingformerverdiavgift v1.0 XSD
# over every golden the accounting module's tests write and Skatteetaten's own
# examples beside them, under apps/server/internal/accounting/mva/testdata.
# Run it as `mise run mva:validate` (mise puts the pinned JDK on the PATH).
# The XSD is in the repository, copied from Skatteetaten (testdata/README.md):
# nothing is fetched.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
testdata="$root/apps/server/internal/accounting/mva/testdata"

if ! command -v java >/dev/null 2>&1; then
  echo "error: no java on the PATH; run this as 'mise run mva:validate'" >&2
  exit 1
fi

shopt -s nullglob
goldens=("$testdata"/golden/*.xml)
if [ "${#goldens[@]}" -eq 0 ]; then
  echo "error: no goldens under $testdata/golden" >&2
  exit 1
fi

java "$here/Validate.java" \
  --xsd "$testdata/xsd/no.skatteetaten.fastsetting.avgift.mva.skattemeldingformerverdiavgift.v1.0.xsd" \
  "${goldens[@]}" "$testdata"/examples/*.xml
