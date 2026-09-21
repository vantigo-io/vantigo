package customers

// iso3166Alpha2 is the ISO 3166-1 alpha-2 set validateCountryCode checks
// against (customers foundation design D2): the 249 officially assigned
// two-letter country codes, lower-case to match this module's stored
// convention. User-assigned codes (AA, QM-QZ, XA-XZ, ZZ) and
// not-yet-assigned or withdrawn codes some sources include unofficially —
// "uk" (the common but never-official alias for "gb"), "xk" (Kosovo, still
// only user-assigned, not officially assigned) — are deliberately absent.
// Sourced from the officially-assigned list, cross-checked against
// go-webauthn's own ISO 3166-1 alpha-2 table
// (github.com/go-webauthn/webauthn/protocol/iso3166.go), which this module
// cannot depend on (depguard; and it validates upper-case codes for a
// different purpose) but which independently agrees on all 249.
var iso3166Alpha2 = map[string]struct{}{
	"ad": {}, "ae": {}, "af": {}, "ag": {}, "ai": {}, "al": {}, "am": {}, "ao": {}, "aq": {}, "ar": {}, "as": {}, "at": {},
	"au": {}, "aw": {}, "ax": {}, "az": {}, "ba": {}, "bb": {}, "bd": {}, "be": {}, "bf": {}, "bg": {}, "bh": {}, "bi": {},
	"bj": {}, "bl": {}, "bm": {}, "bn": {}, "bo": {}, "bq": {}, "br": {}, "bs": {}, "bt": {}, "bv": {}, "bw": {}, "by": {},
	"bz": {}, "ca": {}, "cc": {}, "cd": {}, "cf": {}, "cg": {}, "ch": {}, "ci": {}, "ck": {}, "cl": {}, "cm": {}, "cn": {},
	"co": {}, "cr": {}, "cu": {}, "cv": {}, "cw": {}, "cx": {}, "cy": {}, "cz": {}, "de": {}, "dj": {}, "dk": {}, "dm": {},
	"do": {}, "dz": {}, "ec": {}, "ee": {}, "eg": {}, "eh": {}, "er": {}, "es": {}, "et": {}, "fi": {}, "fj": {}, "fk": {},
	"fm": {}, "fo": {}, "fr": {}, "ga": {}, "gb": {}, "gd": {}, "ge": {}, "gf": {}, "gg": {}, "gh": {}, "gi": {}, "gl": {},
	"gm": {}, "gn": {}, "gp": {}, "gq": {}, "gr": {}, "gs": {}, "gt": {}, "gu": {}, "gw": {}, "gy": {}, "hk": {}, "hm": {},
	"hn": {}, "hr": {}, "ht": {}, "hu": {}, "id": {}, "ie": {}, "il": {}, "im": {}, "in": {}, "io": {}, "iq": {}, "ir": {},
	"is": {}, "it": {}, "je": {}, "jm": {}, "jo": {}, "jp": {}, "ke": {}, "kg": {}, "kh": {}, "ki": {}, "km": {}, "kn": {},
	"kp": {}, "kr": {}, "kw": {}, "ky": {}, "kz": {}, "la": {}, "lb": {}, "lc": {}, "li": {}, "lk": {}, "lr": {}, "ls": {},
	"lt": {}, "lu": {}, "lv": {}, "ly": {}, "ma": {}, "mc": {}, "md": {}, "me": {}, "mf": {}, "mg": {}, "mh": {}, "mk": {},
	"ml": {}, "mm": {}, "mn": {}, "mo": {}, "mp": {}, "mq": {}, "mr": {}, "ms": {}, "mt": {}, "mu": {}, "mv": {}, "mw": {},
	"mx": {}, "my": {}, "mz": {}, "na": {}, "nc": {}, "ne": {}, "nf": {}, "ng": {}, "ni": {}, "nl": {}, "no": {}, "np": {},
	"nr": {}, "nu": {}, "nz": {}, "om": {}, "pa": {}, "pe": {}, "pf": {}, "pg": {}, "ph": {}, "pk": {}, "pl": {}, "pm": {},
	"pn": {}, "pr": {}, "ps": {}, "pt": {}, "pw": {}, "py": {}, "qa": {}, "re": {}, "ro": {}, "rs": {}, "ru": {}, "rw": {},
	"sa": {}, "sb": {}, "sc": {}, "sd": {}, "se": {}, "sg": {}, "sh": {}, "si": {}, "sj": {}, "sk": {}, "sl": {}, "sm": {},
	"sn": {}, "so": {}, "sr": {}, "ss": {}, "st": {}, "sv": {}, "sx": {}, "sy": {}, "sz": {}, "tc": {}, "td": {}, "tf": {},
	"tg": {}, "th": {}, "tj": {}, "tk": {}, "tl": {}, "tm": {}, "tn": {}, "to": {}, "tr": {}, "tt": {}, "tv": {}, "tw": {},
	"tz": {}, "ua": {}, "ug": {}, "um": {}, "us": {}, "uy": {}, "uz": {}, "va": {}, "vc": {}, "ve": {}, "vg": {}, "vi": {},
	"vn": {}, "vu": {}, "wf": {}, "ws": {}, "ye": {}, "yt": {}, "za": {}, "zm": {}, "zw": {},
}
