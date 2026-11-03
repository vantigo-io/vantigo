# Elavgift (forbruksavgift på elektrisk kraft) 2026: research for a metering-point class hint

Researched 2026-11-03. Classes for the hint: `ordinary`, `reduced`, `exempt`.

## Sources

- Stortingsvedtak om avgift på elektrisk kraft for 2026, FOR-2025-12-18-2760 (vedtatt 18.12.2025, endret ved FOR-2025-12-19-2788): https://lovdata.no/dokument/STV/forskrift/2025-12-18-2760
- Forskrift om særavgifter (saf.), kap. 3-12: https://lovdata.no/dokument/SF/forskrift/2001-12-11-1451 (§§ 3-12-1 to 3-12-20; § 3-12-4 last amended by FOR-2025-12-19-2783, in force 1.1.2026)
- Skattedirektoratet, Årsrundskriv for avgift på elektrisk kraft 2026 (version 1 July 2026): https://www.skatteetaten.no/rettskilder/type/rundskriv-retningslinjer-og-andre-rettskilder/avgiftsrundskriv/elektrisk-kraft-2026/ — PDF: https://www.skatteetaten.no/globalassets/rettskilder/avgiftsrundskriv/avgift-pa-elektrisk-kraft-2026-versjon-2.pdf
- Skatteetaten, rate page: https://www.skatteetaten.no/bedrift-og-organisasjon/avgifter/saravgifter/om/elektrisk-kraft/
- Regjeringen, budget proposal (original proposal 4,18 øre, changed by Stortinget): https://www.regjeringen.no/no/aktuelt/foreslar-reduserte-stromutgifter/id3124548/
- Bring postnummerregister (downloaded 2026-11-03): https://www.bring.no/postnummerregister-ansi.txt (from https://www.bring.no/tjenester/adressetjenester/postnummer)
- Kartverket/Geonorge address API (straddle check): https://ws.geonorge.no/adresser/v1/

## 1. Rates 2026

Stortingsvedtaket § 1:

| Class | Rate 2026 | Basis |
| --- | --- | --- |
| Ordinary (alminnelig sats) | **7,13 øre/kWh** | § 1 første ledd |
| Reduced (redusert sats) | **0,60 øre/kWh** ("0,6 øre") | § 1 annet ledd a–d |
| Exempt (fritak) | 0 | §§ 2 and 3 |

- **No seasonal split in 2026**: one rate "hele året" from 1 January 2026 (Skatteetaten rate page; vedtaket says "Fra 1. januar 2026 ... 7,13 øre per kWh"). (Earlier years had a Jan–Mar / Apr–Dec split; 2026 does not.)
- The Government proposed 4,18 øre; Stortinget adopted 7,13 øre.
- Rates exclude VAT. The tax is computed per kWh per quarter (saf. § 3-12-3); if the rate changes inside a reading period the quantity is split pro rata.
- The vedtak is for "budsjettåret 2026"; a new vedtak applies from 1.1.2027 — the software should treat rates as dated data.

## 2. Geographic rule: tiltakssonen (Finnmark + Nord-Troms)

### Legal text (vedtaket)

- § 1 annet ledd **b**: reduced rate (0,6 øre) for "bruk i Finnmark fylke og kommunene Karlsøy, Kvænangen, Kåfjord, Lyngen, Nordreisa, Skjervøy og Storfjord i Troms fylke".
- § 3 bokstav **a**: full exemption for "husholdninger og offentlig forvaltning" in the same area.

The rundskriv (pkt. 4.3 and 5.7) defines the zone negatively as Troms og Finnmark minus Balsfjord, Bardu, Dyrøy, Gratangen, Harstad, Ibestad, Kvæfjord, Lavangen, Målselv, Salangen, Senja, Sørreisa, Tjeldsund and Tromsø — the same set.

### Municipalities (current numbers, after the 2024 split)

Finnmark (county 56), all 18:
5601 Alta, 5603 Hammerfest, 5605 Sør-Varanger, 5607 Vadsø, 5610 Kárášjohka-Karasjok, 5612 Guovdageaidnu-Kautokeino, 5614 Loppa, 5616 Hasvik, 5618 Måsøy, 5620 Nordkapp, 5622 Porsanger, 5624 Lebesby, 5626 Gamvik, 5628 Deatnu-Tana, 5630 Berlevåg, 5632 Båtsfjord, 5634 Vardø, 5636 Unjárga-Nesseby.

Troms (county 55), 7 municipalities:
5534 Karlsøy, 5536 Lyngen, 5538 Storfjord, 5540 Gáivuotna-Kåfjord, 5542 Skjervøy, 5544 Nordreisa, 5546 Kvænangen.

Not in the zone (Troms): 5501 Tromsø, 5503 Harstad, 5510 Kvæfjord, 5512 Tjeldsund, 5514 Ibestad, 5516 Gratangen, 5518 Lavangen, 5520 Bardu, 5522 Salangen, 5524 Målselv, 5526 Sørreisa, 5528 Dyrøy, 5530 Senja, 5532 Balsfjord.

Rule for software: **kommunenummer starts with 56, or is one of 5534, 5536, 5538, 5540, 5542, 5544, 5546.**

### Scope — it is NOT households-only

- **Households and public administration** in the zone: **exempt** (§ 3 a).
- **Everything else delivered in the zone** (all business, of any NACE): **reduced** 0,6 øre (§ 1 annet ledd b). Rundskriv 1.2: redusert avgift for "all næringsvirksomhet i enkelte kommuner i Troms og Finnmark".
- So in the zone nothing is `ordinary` except — by saf. § 3-12-9 — deliveries to *administrasjonsbygg* (≥ 80 % admin area). Note: § 3-12-9 speaks of "redusert avgiftssats eller fritak" generally, but the rundskriv applies it to "avgiftslettelsen for industrien mv."; whether it bites on the geographic § 1 b reduction is not stated explicitly. Practical hint: business in zone → `reduced`.
- "Husholdning" (rundskriv 5.7) covers eneboliger, leiligheter, hybler, fritidsboliger (hytter, setrer, koier), uthus, garasjer, fellesvaskeri and fellesanlegg tied to households, and also aldershjem, barnehjem, daginstitusjoner for barn.
- "Offentlig forvaltning" is not further defined in the rundskriv. A reasonable proxy: Enhetsregisteret institusjonell sektorkode 6100 (statsforvaltningen) / 6500 (kommuneforvaltningen) and NACE 84; state/municipal *companies* (foretak, KF/IKS selling goods/services) are business → reduced. Treat as a heuristic, not law.

### Postal codes in the zone (Bring register, kommunenummer per code)

Bring's register gives exactly one municipality per postal code. **199 codes** map to zone municipalities.

**Compressed ranges, register-contiguous** (no non-zone postal code exists anywhere inside each range — gaps contain only unused numbers):

- 9046
- 9060–9069
- 9130
- 9132–9140
- 9142–9169
- 9180–9197
- 9501–9991

i.e. every Bring code ≥ 9501 is in Finnmark. Codes that exist inside 9046–9197 but are **not** zone: 9049–9059 (Balsfjord/Tromsø: 9049, 9050, 9055, 9056, 9057, 9059), 9100–9128 (Tromsø), 9131 Kårvik (Tromsø), 9141 Melvika (Tromsø), 9170–9178 (Svalbard). If you test ranges against arbitrary 4-digit input, use the strict numeric ranges below or the raw list.

**Compressed ranges, strictly numeric** (only codes that exist):

9046, 9060, 9062, 9064, 9068-9069, 9130, 9132, 9134-9138, 9140, 9142-9149, 9151-9159, 9161-9163, 9169, 9180-9182, 9184-9187, 9189-9190, 9192-9195, 9197, 9501-9525, 9527-9529, 9531-9533, 9536-9538, 9540-9541, 9545, 9550, 9552, 9580, 9582-9587, 9590-9591, 9593, 9595, 9600-9603, 9609-9612, 9615-9616, 9620-9621, 9624, 9650-9651, 9653, 9657, 9664, 9670, 9672, 9690-9692, 9700, 9709-9717, 9722, 9730-9737, 9740, 9742, 9750-9751, 9760, 9762-9766, 9768, 9770-9773, 9775, 9782, 9790, 9800-9804, 9810-9811, 9815, 9820, 9826, 9840-9846, 9900-9901, 9910-9912, 9914-9917, 9925-9926, 9930, 9935, 9950-9952, 9955, 9960, 9980-9982, 9990-9991

**Raw list (199 codes):**

9046, 9060, 9062, 9064, 9068, 9069, 9130, 9132, 9134, 9135, 9136, 9137, 9138, 9140, 9142, 9143, 9144, 9145, 9146, 9147, 9148, 9149, 9151, 9152, 9153, 9154, 9155, 9156, 9157, 9158, 9159, 9161, 9162, 9163, 9169, 9180, 9181, 9182, 9184, 9185, 9186, 9187, 9189, 9190, 9192, 9193, 9194, 9195, 9197, 9501, 9502, 9503, 9504, 9505, 9506, 9507, 9508, 9509, 9510, 9511, 9512, 9513, 9514, 9515, 9516, 9517, 9518, 9519, 9520, 9521, 9522, 9523, 9524, 9525, 9527, 9528, 9529, 9531, 9532, 9533, 9536, 9537, 9538, 9540, 9541, 9545, 9550, 9552, 9580, 9582, 9583, 9584, 9585, 9586, 9587, 9590, 9591, 9593, 9595, 9600, 9601, 9602, 9603, 9609, 9610, 9611, 9612, 9615, 9616, 9620, 9621, 9624, 9650, 9651, 9653, 9657, 9664, 9670, 9672, 9690, 9691, 9692, 9700, 9709, 9710, 9711, 9712, 9713, 9714, 9715, 9716, 9717, 9722, 9730, 9731, 9732, 9733, 9734, 9735, 9736, 9737, 9740, 9742, 9750, 9751, 9760, 9762, 9763, 9764, 9765, 9766, 9768, 9770, 9771, 9772, 9773, 9775, 9782, 9790, 9800, 9801, 9802, 9803, 9804, 9810, 9811, 9815, 9820, 9826, 9840, 9841, 9842, 9843, 9844, 9845, 9846, 9900, 9901, 9910, 9911, 9912, 9914, 9915, 9916, 9917, 9925, 9926, 9930, 9935, 9950, 9951, 9952, 9955, 9960, 9980, 9981, 9982, 9990, 9991

Notable: 9186 Andsnes belongs to Loppa (Finnmark, 5614) although it sits in the 918x Kvænangen/Skjervøy series; 9181 Hamneidet belongs to Nordreisa.

### Postal codes straddling a municipality border

Checked every Troms/Finnmark code (395) against Kartverket's address register (count of addresses with that postnummer, total vs. in Bring's municipality):

| Postnr | Bring kommune | Addresses by kommune | Crosses zone border? |
| --- | --- | --- | --- |
| **9140 Rebbenes** | 5534 Karlsøy (zone) | 108 Karlsøy, **68 Tromsø (5501, not zone)** | **Yes** — ~39 % of addresses outside the zone |
| 9180 Skjervøy | 5542 Skjervøy | 1327 Skjervøy, 39 Kvænangen | No (both zone) |
| 9715 Kokelv | 5603 Hammerfest | 233 Hammerfest, 1 Porsanger | No (both zone) |
| 9955 Hamningberg | 5634 Vardø | 233 Vardø, 111 Båtsfjord | No (both zone) |
| 9309 Finnsnes | 5530 Senja | 611 Senja, 220 Sørreisa, 101 Målselv | No (neither zone) |
| 9321 Moen | 5524 Målselv | 1755 Målselv, 256 Balsfjord | No (neither) |
| 9334 Øverbygd | 5524 Målselv | 1037 Målselv, 172 Balsfjord | No (neither) |
| 9446 Grovfjord | 5512 Tjeldsund | 631 Tjeldsund, 45 Gratangen | No (neither) |

No non-zone code contains zone addresses. So **9140 is the only ambiguous code**; for it the hint should prefer the metering point's kommunenummer (Elhub/matrikkel) over the postal code. Generally, prefer kommunenummer from the address (matrikkel / Elhub metering point) and fall back to postal code.

Svalbard (9170–9178, kommune 2100) is outside Finnmark and not in the zone. (Mainland særavgifter are generally not levied on Svalbard, but this was not verified against a source here — check before relying on it.)

## 3. Reduced rate (0,6 øre) — who qualifies

### Legal bases
Vedtaket § 1 annet ledd: (a) industri, bergverk, produksjon av fjernvarme, arbeidsmarkedsbedrifter som utøver industriproduksjon — "elektrisk kraft som benyttes i forbindelse med selve produksjonsprosessen"; (b) tiltakssonen; (c) skip i næring; (d) produksjon eller omforming av energiprodukt.

### Industri/bergverk — saf. § 3-12-4 (from 1.1.2026 on **SN2025**, not SN2007)
The undertaking must be registered in Enhetsregisteret in one of these næringsundergrupper (SN2025):
- a. section **B** Bergverksdrift og utvinning, divisions **05–09**
- b. section **C** Industri, divisions **10–33**
- c. **38.210** Materialgjenvinning (SN2007 equivalent was 38.32)
- d. **88.993** Arbeidstrening og varig tilrettelagt arbeid — *only if* the activity by nature falls under B or C

Rundskriv 4.1: the registered code governs — an entity registered in a qualifying code "skal belastes redusert sats" even if it actually does something else; a wrongly registered entity must correct Enhetsregisteret. Change of code has effect from registration; no retroactive refund. Documentation: confirmation of næringskode from Enhetsregisteret (≤ 1 year old), plus an egenerklæring on the split if the meter also serves other uses (saf. § 3-12-11).

Conditions not derivable from NACE:
- Only power used "i forbindelse med selve produksjonsprosessen".
- **Administrasjonsbygg** (saf. § 3-12-9): a building where ≥ 80 % of the area is administration pays full rate, whatever the NACE. Assessed per building / per the area the business uses. Admin includes ledelse, regnskap, IT, salg, marketing, resepsjon, fjernvarelager, remote operation.
- Mixed use on one meter (industry + admin/household) must be split by the customer's declaration.
- No capacity threshold for industry.

### Fjernvarme — saf. § 3-12-5
Registered in **35.300** *or* holding an NVE fjernvarmekonsesjon, **and** ≥ 50 % of energy from waste, bioenergy, waste heat and/or heat pumps (3-year build-up exception), **and** delivers to consumers outside its own business. Not fjernkjøling. Needs documentation of energy sources — cannot be told from NACE alone.

### Skip i næring — saf. § 3-12-7
Shore power to ships in commercial use (incl. drill ships/rigs); not laid-up ships, ships used as hotel/office/storage, in dock or rebuild. Self-declaration per delivery. Not a NACE question (a shore-power metering point at a port).

### Production or conversion of energy products — saf. § 3-12-8
Power used in the production process for products in tolltariff 15.07–15.18 (as fuel), 27.01–27.15, 29.01–29.02, 29.05.1100 (non-synthetic, as fuel), 34.03, 38.11, 38.17, 38.26 (biodiesel). Most such producers are already in B (06 oil/gas) or C (19, 20); the rule adds producers outside B/C, e.g. biogas plants (SN 35.2 gas manufacture, or 38.x waste treatment). Self-declaration of the quantity.

### Datasentre — no longer reduced
The former data-centre provision (saf. § 3-12-6) was repealed with effect from 1.1.2023 (FOR-2022-12-19-2290); data centres (NACE 63.1x) pay the **ordinary** rate in 2026 unless in tiltakssonen.

### Greenhouses — exempt, not reduced (see 3b)

## 3b. Full exemptions tied to business activity

| Exemption | Basis | From NACE? |
| --- | --- | --- |
| Kjemisk reduksjon, elektrolyse, metallurgiske og mineralogiske prosesser | vedtak § 3 d, saf. § 3-12-13 | **No.** Process-based, only power to those specific process steps (HR-2023-2401-A; CJEU C-465/15 — fans/blowers are not "chemical reduction"). Mineralogical = processes under SN division 23 (cement, concrete, lime, glass, mineral wool, ceramics, brick) but registration in 23 is not required. One customer may have 0 / reduced / ordinary splits. |
| Veksthusnæringen (greenhouses with electric heating) | § 3 e, saf. § 3-12-14 | Partly; requires egenerklæring "veksthus med elektrisk varmeanlegg" shown at delivery time; only production-process power. |
| Use directly connected with electricity production (excitation, pumping to upper reservoirs, station lighting/heating, turbine pitch/yaw, de-icing) | § 3 f, saf. § 3-12-1 (3) | Partly (35.11); not distribution substations; pure reserve plants excluded. Documentation required. |
| Traction power for trains, trams, metro, trolleybus, incl. heating/lighting in the vehicles | § 3 g, saf. § 3-12-16 | Partly (rail operators / infrastructure); only traction and in-vehicle use, not stations/workshops. |
| Households and public administration in tiltakssonen | § 3 a | Geographic + customer type |
| NATO / PfP forces; Nordic Investment Bank | § 3 b, c | No |

Documentation (saf. §§ 3-12-19, 3-12-20; rundskriv 7.3): the network company must hold documentation (≤ 1 year old) **before** delivering at reduced/zero rate; without it, full rate. If conditions lapse the customer must notify and full rate is charged.

## 3c. NACE mapping for a software hint

Use SN2025 codes from Enhetsregisteret (from 2026 the forskrift uses SN2025; SN2007 prefixes are the same at division level for B and C).

**→ `reduced` (strong hint)** — still needs Enhetsregister confirmation + production-process use, not an admin building:
- 05, 06, 07, 08, 09 (section B)
- 10–33 (section C)
- 38.21 / 38.210 Materialgjenvinning (SN2007: 38.32)

**→ `reduced` (may qualify — needs documentation)**:
- 35.30 / 35.300 Fjernvarme (50 % renewable/waste-heat condition, NVE concession alternative)
- 88.993 Arbeidstrening og varig tilrettelagt arbeid (only if activity is industrial by nature)
- 35.2x gas manufacture / biogas, other energy-product producers outside B/C (§ 3-12-8)
- Shore-power points for ships in commercial use (port operators, 52.22; really per metering point)

**→ `exempt` (may qualify — never auto-assign; default those within C to `reduced`)**:
- 24.10 iron/steel/ferroalloys, 24.42 aluminium, 24.43–24.46 other non-ferrous metals, 24.5 casting (metallurgical / chemical reduction / electrolysis)
- 20.11 industrial gases (e.g. hydrogen by electrolysis), 20.13 other inorganic basic chemicals (chlor-alkali electrolysis), 20.15 fertilisers (if process involves reduction/electrolysis)
- 23.xx non-metallic mineral products (mineralogical processes)
- 01.13, 01.19, 01.30 greenhouse horticulture (only "veksthus med elektrisk varmeanlegg")
- 35.11 electricity production (own consumption in production)
- 49.1x/49.2x rail, 49.31 urban transport (trams, metro), 52.21 rail infrastructure (traction power)

**→ `ordinary`**: everything else outside tiltakssonen — households, services, retail, offices, agriculture without electrically heated greenhouses, data centres (63.1x), fjernkjøling, any admin building (≥ 80 % admin area) even for B/C companies.

## Decision sketch

1. Metering point in zone municipality (56xx or 5534/5536/5538/5540/5542/5544/5546)? → household or public administration: `exempt`; otherwise `reduced`.
2. Else NACE in B (05–09), C (10–33) or 38.21 → `reduced` (suggest; may be `exempt` for metallurgical/electrolysis/mineralogical process loads — flag "may qualify").
3. Else 35.30, 88.993, energy-product producers → "may qualify for reduced".
4. Else greenhouse / rail traction / power-plant own use → "may qualify for exempt".
5. Else `ordinary`.
Always: the class requires documentation held by the supplier/network company; a hint is not a determination.

## 4. Other reasons elavgift does not apply (matters for a customer list)

- **Own production / local sharing** (vedtak § 2, saf. §§ 3-12-12, 3-12-17, 3-12-18): power from energy-recovery plants delivered directly to end users; micro power plants (generator < 100 kVA, per generator); emergency generators during outage; back-pressure plants (used in the producing business); solar used directly by the producer via internal wiring (not via the grid); renewable power shared on the same property (≤ 1 MW AC per property, under FOR kraftomsetning § 3-12) or the same næringsområde (≤ 5 MW AC, § 3-13, new from 1.1.2026). These reduce the taxable quantity rather than change the metering point's class; the Elhub settlement allocates locally shared volumes. Surplus fed to the grid is taxable at the buyer's rate.
- **Rail traction** (exempt, see above).
- **Power used in production/distribution** (saf. § 3-12-1 (3)): consumption directly connected with power production and distribution is outside the tax (incl. transmission losses before the delivery point; rundskriv 3.1.3).
- **BESS** (rundskriv 3.1.7, from 1.7.2026): storage at the production site or in the grid is not a delivery; storage behind the customer's meter is taxed at delivery to the battery; losses before delivery are untaxed.
- **Offshore**: power delivered on the continental shelf is outside the tax; power from the shelf delivered on land is taxable.
- **Unmetered deliveries**: H1/H2 tariffs taxed on 6 000 kWh per kW subscribed; street lighting on 4 000 burning hours.
- **Who is liable**: the network company (the business that transports power to the consumer) registers and reports, quarterly in Elsær 2.0; the network company collects the documentation. A power *supplier* (kraftleverandør) does not normally invoice elavgift in Norway — it is on the nettleie invoice. Relevant if the software models suppliers vs. network companies.
- **State-power contracts** ("1976-kontrakter", "2 TWh-kontrakter"; saf. § 3-12-10): delivered without invoiced tax with a price reduction instead — edge case.
- Separate from elavgift: VAT on electricity is zero-rated for households in Nordland, Troms and Finnmark (merverdiavgiftsloven § 6-6) — a different geography; do not conflate.

## Appendix: zone postal codes with place and municipality (Bring, category letter)

Category: G = street addresses, P = PO boxes, B = both, S = service, F = multiple.

| Postnr | Poststed | Kommune | Cat |
| --- | --- | --- | --- |
| 9046 | OTEREN | 5538 STORFJORD | G |
| 9060 | LYNGSEIDET | 5536 LYNGEN | G |
| 9062 | FURUFLATEN | 5536 LYNGEN | G |
| 9064 | SVENSBY | 5536 LYNGEN | G |
| 9068 | NORD-LENANGEN | 5536 LYNGEN | G |
| 9069 | LYNGSEIDET | 5536 LYNGEN | P |
| 9130 | HANSNES | 5534 KARLSØY | G |
| 9132 | STAKKVIK | 5534 KARLSØY | G |
| 9134 | HANSNES | 5534 KARLSØY | P |
| 9135 | VANNVÅG | 5534 KARLSØY | G |
| 9136 | VANNAREID | 5534 KARLSØY | G |
| 9137 | VANNVÅG | 5534 KARLSØY | P |
| 9138 | KARLSØY | 5534 KARLSØY | B |
| 9140 | REBBENES | 5534 KARLSØY | G |
| 9142 | SKIBOTN | 5538 STORFJORD | P |
| 9143 | SKIBOTN | 5538 STORFJORD | G |
| 9144 | SAMUELSBERG | 5540 GÁIVUOTNA KÅFJORD | G |
| 9145 | SAMUELSBERG | 5540 GÁIVUOTNA KÅFJORD | P |
| 9146 | OLDERDALEN | 5540 GÁIVUOTNA KÅFJORD | G |
| 9147 | BIRTAVARRE | 5540 GÁIVUOTNA KÅFJORD | G |
| 9148 | OLDERDALEN | 5540 GÁIVUOTNA KÅFJORD | P |
| 9149 | BIRTAVARRE | 5540 GÁIVUOTNA KÅFJORD | P |
| 9151 | STORSLETT | 5544 NORDREISA | G |
| 9152 | SØRKJOSEN | 5544 NORDREISA | G |
| 9153 | ROTSUND | 5544 NORDREISA | G |
| 9154 | STORSLETT | 5544 NORDREISA | G |
| 9155 | SØRKJOSEN | 5544 NORDREISA | P |
| 9156 | STORSLETT | 5544 NORDREISA | P |
| 9157 | STORSLETT | 5544 NORDREISA | G |
| 9158 | STORSLETT | 5544 NORDREISA | G |
| 9159 | HAVNNES | 5544 NORDREISA | B |
| 9161 | BURFJORD | 5546 KVÆNANGEN | G |
| 9162 | SØRSTRAUMEN | 5546 KVÆNANGEN | G |
| 9163 | JØKELFJORD | 5546 KVÆNANGEN | G |
| 9169 | BURFJORD | 5546 KVÆNANGEN | P |
| 9180 | SKJERVØY | 5542 SKJERVØY | G |
| 9181 | HAMNEIDET | 5544 NORDREISA | G |
| 9182 | SEGLVIK | 5546 KVÆNANGEN | G |
| 9184 | REINFJORD | 5546 KVÆNANGEN | G |
| 9185 | SPILDRA | 5546 KVÆNANGEN | G |
| 9186 | ANDSNES | 5614 LOPPA | G |
| 9187 | VALANHAMN | 5546 KVÆNANGEN | G |
| 9189 | SKJERVØY | 5542 SKJERVØY | P |
| 9190 | AKKARVIK | 5542 SKJERVØY | G |
| 9192 | ARNØYHAMN | 5542 SKJERVØY | G |
| 9193 | NIKKEBY | 5542 SKJERVØY | G |
| 9194 | LAUKSLETTA | 5542 SKJERVØY | G |
| 9195 | ÅRVIKSAND | 5542 SKJERVØY | G |
| 9197 | ULØYBUKT | 5542 SKJERVØY | G |
| 9501 | ALTA | 5601 ALTA | P |
| 9502 | ALTA | 5601 ALTA | P |
| 9503 | ALTA | 5601 ALTA | P |
| 9504 | ALTA | 5601 ALTA | P |
| 9505 | ALTA | 5601 ALTA | P |
| 9506 | ALTA | 5601 ALTA | P |
| 9507 | ALTA | 5601 ALTA | P |
| 9508 | ALTA | 5601 ALTA | P |
| 9509 | ALTA | 5601 ALTA | P |
| 9510 | ALTA | 5601 ALTA | G |
| 9511 | ALTA | 5601 ALTA | G |
| 9512 | ALTA | 5601 ALTA | G |
| 9513 | ALTA | 5601 ALTA | G |
| 9514 | ALTA | 5601 ALTA | G |
| 9515 | ALTA | 5601 ALTA | G |
| 9516 | ALTA | 5601 ALTA | G |
| 9517 | ALTA | 5601 ALTA | G |
| 9518 | ALTA | 5601 ALTA | G |
| 9519 | KVIBY | 5601 ALTA | G |
| 9520 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9521 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | P |
| 9522 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9523 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9524 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9525 | MAZE | 5612 GUOVDAGEAIDNU KAUTOKEINO | B |
| 9527 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9528 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9529 | KAUTOKEINO | 5612 GUOVDAGEAIDNU KAUTOKEINO | G |
| 9531 | KVALFJORD | 5601 ALTA | B |
| 9532 | HAKKSTABBEN | 5601 ALTA | G |
| 9533 | KONGSHUS | 5601 ALTA | B |
| 9536 | KORSFJORDEN | 5601 ALTA | G |
| 9537 | TVERRELVDALEN | 5601 ALTA | G |
| 9538 | ALTA | 5601 ALTA | G |
| 9540 | TALVIK | 5601 ALTA | G |
| 9541 | ALTA | 5601 ALTA | G |
| 9545 | LANGFJORDBOTN | 5601 ALTA | G |
| 9550 | ØKSFJORD | 5614 LOPPA | G |
| 9552 | ØKSFJORDEN | 5614 LOPPA | G |
| 9580 | BERGSFJORD | 5614 LOPPA | B |
| 9582 | NUVSVÅG | 5614 LOPPA | G |
| 9583 | LANGFJORDHAMN | 5614 LOPPA | G |
| 9584 | SØR-TVERRFJORD | 5614 LOPPA | B |
| 9585 | SANDLAND | 5614 LOPPA | G |
| 9586 | LOPPA | 5614 LOPPA | G |
| 9587 | SKAVNAKK | 5614 LOPPA | G |
| 9590 | HASVIK | 5616 HASVIK | G |
| 9591 | HASVIK | 5616 HASVIK | P |
| 9593 | BREIVIKBOTN | 5616 HASVIK | B |
| 9595 | SØRVÆR | 5616 HASVIK | B |
| 9600 | HAMMERFEST | 5603 HAMMERFEST | G |
| 9601 | HAMMERFEST | 5603 HAMMERFEST | G |
| 9602 | HAMMERFEST | 5603 HAMMERFEST | G |
| 9603 | HAMMERFEST | 5603 HAMMERFEST | G |
| 9609 | NORDRE SEILAND | 5603 HAMMERFEST | G |
| 9610 | RYPEFJORD | 5603 HAMMERFEST | G |
| 9611 | RYPEFJORD | 5603 HAMMERFEST | P |
| 9612 | FORSØL | 5603 HAMMERFEST | G |
| 9615 | HAMMERFEST | 5603 HAMMERFEST | P |
| 9616 | HAMMERFEST | 5603 HAMMERFEST | P |
| 9620 | KVALSUND | 5603 HAMMERFEST | G |
| 9621 | KVALSUND | 5603 HAMMERFEST | P |
| 9624 | REVSNESHAMN | 5603 HAMMERFEST | G |
| 9650 | AKKARFJORD | 5603 HAMMERFEST | B |
| 9651 | LANGSTRAND | 5603 HAMMERFEST | G |
| 9653 | HELLEFJORD | 5603 HAMMERFEST | G |
| 9657 | KÅRHAMN | 5603 HAMMERFEST | G |
| 9664 | SANDØYBOTN | 5603 HAMMERFEST | G |
| 9670 | TUFJORD | 5618 MÅSØY | B |
| 9672 | INGØY | 5618 MÅSØY | B |
| 9690 | HAVØYSUND | 5618 MÅSØY | G |
| 9691 | HAVØYSUND | 5618 MÅSØY | P |
| 9692 | MÅSØY | 5618 MÅSØY | B |
| 9700 | LAKSELV | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9709 | PORSANGMOEN | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9710 | INDRE BILLEFJORD | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9711 | LAKSELV | 5622 PORSANGER PORSÁNGU PORSANKI | P |
| 9712 | LAKSELV | 5622 PORSANGER PORSÁNGU PORSANKI | P |
| 9713 | RUSSENES | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9714 | SNEFJORD | 5618 MÅSØY | G |
| 9715 | KOKELV | 5603 HAMMERFEST | G |
| 9716 | BØRSELV | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9717 | VEIDNES | 5624 LEBESBY | B |
| 9722 | SKOGANVARRE | 5622 PORSANGER PORSÁNGU PORSANKI | G |
| 9730 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9731 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9732 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9733 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9734 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9735 | KARASJOK | 5610 KARASJOHKA KARASJOK | P |
| 9736 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9737 | KARASJOK | 5610 KARASJOHKA KARASJOK | G |
| 9740 | LEBESBY | 5624 LEBESBY | G |
| 9742 | KUNES | 5624 LEBESBY | B |
| 9750 | HONNINGSVÅG | 5620 NORDKAPP | G |
| 9751 | HONNINGSVÅG | 5620 NORDKAPP | P |
| 9760 | NORDVÅGEN | 5620 NORDKAPP | G |
| 9762 | KAMØYVÆR | 5620 NORDKAPP | G |
| 9763 | SKARSVÅG | 5620 NORDKAPP | G |
| 9764 | NORDKAPP | 5620 NORDKAPP | G |
| 9765 | GJESVÆR | 5620 NORDKAPP | G |
| 9766 | SARNES | 5620 NORDKAPP | G |
| 9768 | REPVÅG | 5620 NORDKAPP | G |
| 9770 | MEHAMN | 5626 GAMVIK | B |
| 9771 | SKJÅNES | 5626 GAMVIK | G |
| 9772 | LANGFJORDNES | 5626 GAMVIK | G |
| 9773 | NERVEI | 5626 GAMVIK | B |
| 9775 | GAMVIK | 5626 GAMVIK | B |
| 9782 | DYFJORD | 5624 LEBESBY | G |
| 9790 | KJØLLEFJORD | 5624 LEBESBY | B |
| 9800 | VADSØ | 5607 VADSØ | G |
| 9801 | VADSØ | 5607 VADSØ | G |
| 9802 | VESTRE JAKOBSELV | 5607 VADSØ | G |
| 9803 | VADSØ | 5607 VADSØ | G |
| 9804 | VADSØ | 5607 VADSØ | G |
| 9810 | VESTRE JAKOBSELV | 5607 VADSØ | P |
| 9811 | VADSØ | 5607 VADSØ | P |
| 9815 | VADSØ | 5607 VADSØ | P |
| 9820 | VARANGERBOTN | 5636 UNJARGGA NESSEBY | P |
| 9826 | SIRMA | 5628 DEATNU TANA | G |
| 9840 | VARANGERBOTN | 5636 UNJARGGA NESSEBY | G |
| 9841 | TANA | 5628 DEATNU TANA | G |
| 9842 | TANA | 5628 DEATNU TANA | G |
| 9843 | TANA | 5628 DEATNU TANA | G |
| 9844 | TANA | 5628 DEATNU TANA | G |
| 9845 | TANA | 5628 DEATNU TANA | G |
| 9846 | TANA | 5628 DEATNU TANA | P |
| 9900 | KIRKENES | 5605 SØR-VARANGER | G |
| 9901 | KIRKENES | 5605 SØR-VARANGER | G |
| 9910 | BJØRNEVATN | 5605 SØR-VARANGER | G |
| 9911 | JARFJORD | 5605 SØR-VARANGER | G |
| 9912 | HESSENG | 5605 SØR-VARANGER | G |
| 9914 | BJØRNEVATN | 5605 SØR-VARANGER | P |
| 9915 | KIRKENES | 5605 SØR-VARANGER | P |
| 9916 | HESSENG | 5605 SØR-VARANGER | P |
| 9917 | KIRKENES | 5605 SØR-VARANGER | P |
| 9925 | SVANVIK | 5605 SØR-VARANGER | G |
| 9926 | PASVIK | 5605 SØR-VARANGER | G |
| 9930 | NEIDEN | 5605 SØR-VARANGER | G |
| 9935 | BUGØYNES | 5605 SØR-VARANGER | B |
| 9950 | VARDØ | 5634 VARDØ | G |
| 9951 | VARDØ | 5634 VARDØ | P |
| 9952 | VARDØ | 5634 VARDØ | G |
| 9955 | SVARTNES | 5634 VARDØ | G |
| 9960 | KIBERG | 5634 VARDØ | G |
| 9980 | BERLEVÅG | 5630 BERLEVÅG | G |
| 9981 | BERLEVÅG | 5630 BERLEVÅG | P |
| 9982 | KONGSFJORD | 5630 BERLEVÅG | G |
| 9990 | BÅTSFJORD | 5632 BÅTSFJORD | G |
| 9991 | BÅTSFJORD | 5632 BÅTSFJORD | P |

## Method notes

- Register: `postnr/reg.txt` (Bring, ISO-8859-1, tab-separated: postnr, poststed, kommunenr, kommunenavn, kategori; 5 112 lines), parsed with `tools/zone.py` (outside the data directory).
- Straddle check: for each of the 395 codes Bring maps to counties 55/56, the Geonorge address API count for `postnummer` was compared with the count for `postnummer`+`kommunenummer`; mismatches were broken down per municipality.
