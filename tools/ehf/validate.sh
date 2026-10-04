#!/usr/bin/env bash
# The EHF oracle: the official UBL 2.1 XSD and the EN 16931 and Peppol BIS
# Billing 3.0 Schematron over every golden and every invalid fixture under
# apps/server/internal/invoices/ehf/testdata. Run it as `mise run ehf:validate`
# (mise puts the pinned JDK on the PATH). See tools/ehf/README.md.
#
# 1. Fetch each artefact artefacts.lock names into tools/ehf/.cache and verify
#    its SHA-256; a mismatch stops the run.
# 2. Once per lock (the work directory is keyed on the lock's hash): unpack the
#    XSD, the CEN XSLT and the Peppol Schematron, and compile the Schematron to
#    XSLT with SchXslt through Saxon.
# 3. Validate.java runs the XSD and both XSLTs over each file and judges the SVRL.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
cache="$here/.cache"
lock="$here/artefacts.lock"
testdata="$root/apps/server/internal/invoices/ehf/testdata"

if ! command -v java >/dev/null 2>&1; then
  echo "error: no java on the PATH; run this as 'mise run ehf:validate'" >&2
  exit 1
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# The lock's file name for the artefact whose name starts with $1.
artefact() {
  local name
  name="$(awk -v p="$1" 'index($1, p) == 1 { print $1 }' "$lock")"
  if [ -z "$name" ]; then
    echo "error: artefacts.lock names no $1* artefact" >&2
    exit 1
  fi
  printf '%s/%s\n' "$cache" "$name"
}

mkdir -p "$cache"
while read -r name sum url; do
  case "$name" in '' | '#'*) continue ;; esac
  file="$cache/$name"
  if [ -f "$file" ] && [ "$(sha256 "$file")" = "$sum" ]; then
    continue
  fi
  echo "fetching $name"
  curl -fsSL --retry 3 -o "$file.part" "$url"
  got="$(sha256 "$file.part")"
  if [ "$got" != "$sum" ]; then
    rm -f "$file.part"
    echo "error: $name has SHA-256 $got but artefacts.lock pins $sum ($url)" >&2
    exit 1
  fi
  mv "$file.part" "$file"
done <"$lock"

# Saxon and its dependencies: every jar the lock names except SchXslt's, which
# is a bundle of stylesheets.
classpath="$(awk -v c="$cache" '$1 ~ /\.jar$/ && $1 !~ /^schxslt-/ { printf "%s%s/%s", sep, c, $1; sep = ":" }' "$lock")"
schxslt="$(artefact schxslt-)"
cen="$(artefact en16931-ubl-)"
peppol="$(artefact peppol-bis-invoice-3-)"
ubl="$(artefact UBL-)"

key="$(sha256 "$lock" | cut -c1-16)"
work="$cache/work-$key"
if [ ! -f "$work/.ready" ]; then
  echo "unpacking the artefacts and compiling the Peppol Schematron (once per artefacts.lock)"
  find "$cache" -maxdepth 1 -name 'work-*' -exec rm -rf {} +
  mkdir -p "$work"
  unzip -qo "$ubl" 'xsd/*' -d "$work/ubl"
  unzip -qjo "$cen" 'xslt/EN16931-UBL-validation.xslt' -d "$work/cen"
  unzip -qjo "$peppol" '*/rules/sch/PEPPOL-EN16931-UBL.sch' -d "$work/peppol"
  unzip -qo "$schxslt" 'xslt/2.0/*' -d "$work/schxslt"
  java -cp "$classpath" net.sf.saxon.Transform \
    -s:"$work/peppol/PEPPOL-EN16931-UBL.sch" \
    -xsl:"$work/schxslt/xslt/2.0/pipeline-for-svrl.xsl" \
    -o:"$work/peppol/PEPPOL-EN16931-UBL.xsl"
  touch "$work/.ready"
fi

java -cp "$classpath" "$here/Validate.java" \
  --xsd "$work/ubl/xsd/maindoc" \
  --xslt "$work/cen/EN16931-UBL-validation.xslt" \
  --xslt "$work/peppol/PEPPOL-EN16931-UBL.xsl" \
  --manifest "$testdata/invalid/manifest.json" \
  --known-failures "$here/known-failures.txt" \
  "$testdata"/golden/*.xml "$testdata"/invalid/*.xml
