#!/usr/bin/env bash
# The SAF-T oracle: Skatteetaten's SAF-T Financial 1.40 XSD over every golden
# the accounting module's tests write, and each of Skatteetaten's example
# files against the XSD of its own version, all under
# apps/server/internal/accounting/saft/testdata. Run it as
# `mise run saft:validate` (mise puts the pinned JDK on the PATH). The XSDs
# are in the repository, copied from Skatteetaten (testdata/README.md):
# nothing is fetched. The validator is the VAT return oracle's
# (tools/mva/Validate.java), which takes any XSD.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
testdata="$root/apps/server/internal/accounting/saft/testdata"
validator="$root/tools/mva/Validate.java"

if ! command -v java >/dev/null 2>&1; then
  echo "error: no java on the PATH; run this as 'mise run saft:validate'" >&2
  exit 1
fi

shopt -s nullglob
goldens=("$testdata"/golden/*.xml)
if [ "${#goldens[@]}" -eq 0 ]; then
  echo "error: no goldens under $testdata/golden" >&2
  exit 1
fi

java "$validator" --xsd "$testdata/xsd/Norwegian_SAF-T_Financial_Schema_v_1.40.xsd" \
  "${goldens[@]}" "$testdata/examples/example-1.40.xml"
java "$validator" --xsd "$testdata/xsd/Norwegian_SAF-T_Financial_Schema_v_1.30.xsd" \
  "$testdata/examples/example-1.30.xml"
