---
title: "Objektlagring"
description: "Filsystemleverandøren, modulomfang, nøkkelvalidering og strømmede nedlastinger."
sidebar:
  order: 41
sources:
  - apps/server/internal/storage
---
Objektlagring er applikasjonsporten Kommunikasjon mellomlagrer og serverer vedlegg
gjennom. Moduler snakker aldri med en leverandør-SDK: de får et `ObjectStore` avgrenset
til sitt eget navnerom og sender relative nøkler.

## Leverandørsettet er en regresjon fra .NET-implementasjonen

**Det lokale filsystemet er den eneste støttede leverandøren.** Implementasjonen
(`apps/server/internal/storage`) inneholder én driver, `fs.go`, og konfigurasjonen
godtar bare `fs`:

```text
STORAGE_PROVIDER: must be "fs" if set
```

S3/MinIO- og Azure Blob-leverandørene som .NET-implementasjonen tilbød, **finnes ikke i
denne porten**. Det finnes ingen `STORAGE_S3_*`, ingen `STORAGE_AZURE_BLOB_*`, ingen
autentisering med tilgangsnøkkel, SAS, tilkoblingsstreng eller administrert identitet,
og ingen provisjonering av containere. En installasjon som trenger ekstern
objektlagring i dag, må montere varig lagring som den eier eksklusivt inn i
containeren og peke `fs`-driveren på den. Å legge til en driver er additivt — porten
er leverandørnøytral — men ingenting i dette repositoriet implementerer en.

## Konfigurasjon

| Variabel | Formål | Standard |
| --- | --- | --- |
| `STORAGE_PROVIDER` | `fs`, eller ikke satt | ikke satt |
| `STORAGE_FS_ROOT` | Absolutt rotkatalog; påkrevd når leverandøren er `fs` | ikke satt |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Lemp på kontrollen av gruppe-/verdensskrivbar rot | `0` |

```text
STORAGE_PROVIDER=fs
STORAGE_FS_ROOT=/var/lib/vantigo/objects
```

`STORAGE_FS_ROOT` må være en absolutt sti, og å sette den uten `STORAGE_PROVIDER=fs`
er en konfigurasjonsfeil, ikke en stilltiende no-op.

**Lagring som ikke er satt, feiler lukket ved hver operasjon, ikke ved oppstart.** Med
`STORAGE_PROVIDER` ikke satt starter prosessen likevel og betjener alt annet; hver
lagringsoperasjon rapporterer da `storage: not configured`. Det er med vilje: en
installasjon som aldri laster opp et vedlegg, trenger ikke lagring provisjonert for å
starte.

`STORAGE_FS_ALLOW_INSECURE_ROOT` godtas **bare når `APP_ENV=development`**; utenfor
development er den en oppstartsfeil uansett hva den ville ha gjort — samme form som
`MAIL_DRIVER=log` og `OWNERS_ALLOW_INSECURE_NO_MFA`.

## Filsystemdriveren

Roten må finnes eller kunne opprettes ved initialisering, må ikke selv være en
symbolsk lenke, og — utenfor development — må ikke være gruppe- eller verdensskrivbar.
Kataloger driveren oppretter har modus `0700`: applikasjonsidentiteten alene. Gi den
et volum applikasjonsbrukeren eier eksklusivt; pek den ikke på en delt skrivbar
katalog.

Innesperringen håndheves av operativsystemet, ikke av et par av kontroller-så-åpne.
Driveren åpner roten én gang som en `os.Root` og utfører hver operasjon gjennom den,
så hvert navn løses opp med `openat`-stil innesperring i bruksøyeblikket: en symlenke
plantet inne i roten i etterkant — selv en tidsbestemt til å lande mellom en kontroll
og systemkallet — kan fortsatt ikke få en operasjon til å havne utenfor roten.
Nøkkelvalideringen kjører uansett foran dette og avviser former med traversering,
URL-koding, absolutte stier, omvendt skråstrek, kontrolltegn og duplisert prefiks, så
meningsløs inndata får en tydelig typet avvisning før noe systemkall. De to utfyller
hverandre og er ikke overflødige.

Skrivinger går til en midlertidig fil under roten og flyttes atomisk på plass ved
omdøping, så et krasj midt i en skriving etterlater aldri et delvis skrevet objekt som
lesbart. Nøkler er begrenset til 1024 UTF-8-byte. **Ingen API returnerer noen gang en
fysisk filsystemsti.**

## Modulomfang og den fysiske nøkkelen

Hver modul får et avgrenset lager, og omfanget prefikserer hver nøkkel før den når
driveren. Den fysiske nøkkelen er:

```text
{scope}/{relative-key}
```

**Det finnes ikke noe leietakersegment.** .NET-implementasjonens fysiske nøkkel var
`tenants/{tenant-id}/{scope}/{relative-key}`; dette er en applikasjon for én leietaker,
og leietakersegmentet er borte sammen med resten av flerleietakerskapet. Ingenting
slår opp en leietaker, og ingen lagringsoperasjon feiler i mangel av en.

Omfangsnavn er kanonisk ASCII med små bokstaver `[a-z0-9-]`, høyst 64 tegn, uten
skråstreker eller punktum. Kommunikasjon bruker omfanget `communications`, så ett av
dens vedlegg havner på `communications/<relative-key>`. Utgifter bruker omfanget
`expenses`: en kvitterings relative nøkkel er `receipts/<entryId>/<uuid>`, så dens
fysiske nøkkel er `expenses/receipts/<entryId>/<uuid>`. Fakturaer bruker omfanget
`invoices`: PDF-en til et utstedt dokument har den relative nøkkelen
`documents/<id>/<number>-<sha256>.pdf`, så dens fysiske nøkkel er
`invoices/documents/<id>/<number>-<sha256>.pdf` — lagret én gang etter at utstedelsen
er bekreftet, og aldri slettet eller overskrevet, siden et utstedt dokument er
regnskapsmateriale ([Fakturaer](/en/reference/invoices/#the-pdf)). Et avgrenset lager
avviser en relativ nøkkel som er lik omfanget sitt eller allerede begynner med
`{scope}/`: kallere sender bare relative nøkler og må aldri bygge prefikset selv.

**Utgifters kvitteringer, spesielt.** De er det eneste denne modulen lagrer. En
kvittering leses av den som får se utgiften den hører til — samme regel som styrer
utgiften selv, ikke en egen regel — og alltid gjennom applikasjonens eget
nedlastingsendepunkt (nedenfor), aldri en direkte lesing fra lagringen. Et objekt
fjernes sammen med utgiften sin: ved en vanlig sletting forsvinner objektet når
fjerningen av databaseraden er bekreftet, og ved en mislykket eller avvist skriving
fjernes objektet som ble mellomlagret for den igjen, så en feil etterlater aldri et
objekt ingenting peker på. Det ene vinduet ingenting lukker, er at prosessen dør
mellom skrivingen av objektet og fullføringen av den kompenserende fjerningen eller
bekreftelsen — det finnes ingen bakgrunnsopprydder i dag, så en kvittering som blir
foreldreløs på den måten overlever forespørselen som forårsaket den. Se
[Utgifter](/en/reference/expenses/#receipts) for opplastingsreglene (typer, størrelse,
antall, sniffing) og hastighetsbegrensningen.

## Nedlastinger

Nedlastinger strømmes alltid gjennom et autorisert applikasjonsendepunkt.

**Lagringskontrakten har ingen operasjon for forhåndssignerte URL-er**, og ingen
leverandør omdirigerer noen gang en kaller til en direkte lagrings-URL.
Kommunikasjons nedlastingsendepunkt autoriserer vedlegget, laster det gjennom det
avgrensede lageret og returnerer bytene med den lagrede innholdstypen og et renset
filnavn; feltet `downloadPath` i DTO-ene er det samme applikasjonsendepunktet på samme
opprinnelse, aldri en lagringsplassering. Se
[Kommunikasjon](/en/reference/communications/) for vedleggets livssyklus.

Utgifters `GET /attachments/{id}` følger samme form: det autoriserer kvitteringen
gjennom utgiften den hører til, laster den gjennom modulens eget `expenses`-avgrensede
lager, og strømmer bytene tilbake med innholdstypen de ble sniffet som ved opplasting
og filnavnet slik det ble oppgitt — aldri en lagringsplassering av noe slag. Se
[Utgifter](/en/reference/expenses/#receipts).
