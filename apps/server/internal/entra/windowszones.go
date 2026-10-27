package entra

import "sync"

// This file is the Windows time zone names Graph lays an all-day event in
// (calendar Outlook sync design O4, plan reading 10): an event's TimeZone is
// an IANA zone ("Europe/Oslo"), and its all-day start and end are written at
// midnight in that zone's Windows name ("W. Europe Standard Time"). The
// calendar module holds CLDR's windowsZones.xml for the other direction; no
// module imports another, so the table is mirrored here and inverted
// (windowsZoneOf), with the territory entries of the same file for the zones
// a territory names besides its golden one, and UTC for a zone neither knows.

// windowsZones is CLDR's windowsZones.xml, the territory-001 entry of every
// Windows time zone: the IANA zone a Windows name stands for — copied from
// internal/calendar/windowszones.go, CLDR's values, old spellings included.
var windowsZones = map[string]string{
	"Dateline Standard Time":          "Etc/GMT+12",
	"UTC-11":                          "Etc/GMT+11",
	"Aleutian Standard Time":          "America/Adak",
	"Hawaiian Standard Time":          "Pacific/Honolulu",
	"Marquesas Standard Time":         "Pacific/Marquesas",
	"Alaskan Standard Time":           "America/Anchorage",
	"UTC-09":                          "Etc/GMT+9",
	"Pacific Standard Time (Mexico)":  "America/Tijuana",
	"UTC-08":                          "Etc/GMT+8",
	"Pacific Standard Time":           "America/Los_Angeles",
	"US Mountain Standard Time":       "America/Phoenix",
	"Mountain Standard Time (Mexico)": "America/Mazatlan",
	"Mexico Standard Time 2":          "America/Chihuahua",
	"Mountain Standard Time":          "America/Denver",
	"Yukon Standard Time":             "America/Whitehorse",
	"Central America Standard Time":   "America/Guatemala",
	"Central Standard Time":           "America/Chicago",
	"Easter Island Standard Time":     "Pacific/Easter",
	"Central Standard Time (Mexico)":  "America/Mexico_City",
	"Mexico Standard Time":            "America/Mexico_City",
	"Canada Central Standard Time":    "America/Regina",
	"SA Pacific Standard Time":        "America/Bogota",
	"Eastern Standard Time (Mexico)":  "America/Cancun",
	"Eastern Standard Time":           "America/New_York",
	"Haiti Standard Time":             "America/Port-au-Prince",
	"Cuba Standard Time":              "America/Havana",
	"US Eastern Standard Time":        "America/Indianapolis",
	"Turks And Caicos Standard Time":  "America/Grand_Turk",
	"Paraguay Standard Time":          "America/Asuncion",
	"Atlantic Standard Time":          "America/Halifax",
	"Venezuela Standard Time":         "America/Caracas",
	"Central Brazilian Standard Time": "America/Cuiaba",
	"SA Western Standard Time":        "America/La_Paz",
	"Pacific SA Standard Time":        "America/Santiago",
	"Newfoundland Standard Time":      "America/St_Johns",
	"Tocantins Standard Time":         "America/Araguaina",
	"E. South America Standard Time":  "America/Sao_Paulo",
	"SA Eastern Standard Time":        "America/Cayenne",
	"Argentina Standard Time":         "America/Buenos_Aires",
	"Greenland Standard Time":         "America/Godthab",
	"Montevideo Standard Time":        "America/Montevideo",
	"Magallanes Standard Time":        "America/Punta_Arenas",
	"Saint Pierre Standard Time":      "America/Miquelon",
	"Bahia Standard Time":             "America/Bahia",
	"UTC-02":                          "Etc/GMT+2",
	"Mid-Atlantic Standard Time":      "Etc/GMT+2",
	"Azores Standard Time":            "Atlantic/Azores",
	"Cape Verde Standard Time":        "Atlantic/Cape_Verde",
	"UTC":                             "Etc/UTC",
	"GMT Standard Time":               "Europe/London",
	"Greenwich Standard Time":         "Atlantic/Reykjavik",
	"Sao Tome Standard Time":          "Africa/Sao_Tome",
	"Morocco Standard Time":           "Africa/Casablanca",
	"W. Europe Standard Time":         "Europe/Berlin",
	"Central Europe Standard Time":    "Europe/Budapest",
	"Romance Standard Time":           "Europe/Paris",
	"Central European Standard Time":  "Europe/Warsaw",
	"W. Central Africa Standard Time": "Africa/Lagos",
	"Jordan Standard Time":            "Asia/Amman",
	"GTB Standard Time":               "Europe/Bucharest",
	"Middle East Standard Time":       "Asia/Beirut",
	"Egypt Standard Time":             "Africa/Cairo",
	"E. Europe Standard Time":         "Europe/Chisinau",
	"Syria Standard Time":             "Asia/Damascus",
	"West Bank Standard Time":         "Asia/Hebron",
	"South Africa Standard Time":      "Africa/Johannesburg",
	"FLE Standard Time":               "Europe/Kiev",
	"Israel Standard Time":            "Asia/Jerusalem",
	"South Sudan Standard Time":       "Africa/Juba",
	"Kaliningrad Standard Time":       "Europe/Kaliningrad",
	"Sudan Standard Time":             "Africa/Khartoum",
	"Libya Standard Time":             "Africa/Tripoli",
	"Namibia Standard Time":           "Africa/Windhoek",
	"Arabic Standard Time":            "Asia/Baghdad",
	"Turkey Standard Time":            "Europe/Istanbul",
	"Arab Standard Time":              "Asia/Riyadh",
	"Belarus Standard Time":           "Europe/Minsk",
	"Russian Standard Time":           "Europe/Moscow",
	"E. Africa Standard Time":         "Africa/Nairobi",
	"Volgograd Standard Time":         "Europe/Volgograd",
	"Iran Standard Time":              "Asia/Tehran",
	"Arabian Standard Time":           "Asia/Dubai",
	"Astrakhan Standard Time":         "Europe/Astrakhan",
	"Azerbaijan Standard Time":        "Asia/Baku",
	"Russia Time Zone 3":              "Europe/Samara",
	"Mauritius Standard Time":         "Indian/Mauritius",
	"Saratov Standard Time":           "Europe/Saratov",
	"Georgian Standard Time":          "Asia/Tbilisi",
	"Caucasus Standard Time":          "Asia/Yerevan",
	"Armenian Standard Time":          "Asia/Yerevan",
	"Afghanistan Standard Time":       "Asia/Kabul",
	"West Asia Standard Time":         "Asia/Tashkent",
	"Qyzylorda Standard Time":         "Asia/Qyzylorda",
	"Ekaterinburg Standard Time":      "Asia/Yekaterinburg",
	"Pakistan Standard Time":          "Asia/Karachi",
	"India Standard Time":             "Asia/Calcutta",
	"Sri Lanka Standard Time":         "Asia/Colombo",
	"Nepal Standard Time":             "Asia/Katmandu",
	"Central Asia Standard Time":      "Asia/Bishkek",
	"Bangladesh Standard Time":        "Asia/Dhaka",
	"Omsk Standard Time":              "Asia/Omsk",
	"Myanmar Standard Time":           "Asia/Rangoon",
	"SE Asia Standard Time":           "Asia/Bangkok",
	"Altai Standard Time":             "Asia/Barnaul",
	"W. Mongolia Standard Time":       "Asia/Hovd",
	"North Asia Standard Time":        "Asia/Krasnoyarsk",
	"N. Central Asia Standard Time":   "Asia/Novosibirsk",
	"Tomsk Standard Time":             "Asia/Tomsk",
	"China Standard Time":             "Asia/Shanghai",
	"North Asia East Standard Time":   "Asia/Irkutsk",
	"Singapore Standard Time":         "Asia/Singapore",
	"W. Australia Standard Time":      "Australia/Perth",
	"Taipei Standard Time":            "Asia/Taipei",
	"Ulaanbaatar Standard Time":       "Asia/Ulaanbaatar",
	"Aus Central W. Standard Time":    "Australia/Eucla",
	"Transbaikal Standard Time":       "Asia/Chita",
	"Tokyo Standard Time":             "Asia/Tokyo",
	"North Korea Standard Time":       "Asia/Pyongyang",
	"Korea Standard Time":             "Asia/Seoul",
	"Yakutsk Standard Time":           "Asia/Yakutsk",
	"Cen. Australia Standard Time":    "Australia/Adelaide",
	"AUS Central Standard Time":       "Australia/Darwin",
	"E. Australia Standard Time":      "Australia/Brisbane",
	"AUS Eastern Standard Time":       "Australia/Sydney",
	"West Pacific Standard Time":      "Pacific/Port_Moresby",
	"Tasmania Standard Time":          "Australia/Hobart",
	"Vladivostok Standard Time":       "Asia/Vladivostok",
	"Lord Howe Standard Time":         "Australia/Lord_Howe",
	"Bougainville Standard Time":      "Pacific/Bougainville",
	"Russia Time Zone 10":             "Asia/Srednekolymsk",
	"Magadan Standard Time":           "Asia/Magadan",
	"Norfolk Standard Time":           "Pacific/Norfolk",
	"Sakhalin Standard Time":          "Asia/Sakhalin",
	"Central Pacific Standard Time":   "Pacific/Guadalcanal",
	"Russia Time Zone 11":             "Asia/Kamchatka",
	"Kamchatka Standard Time":         "Asia/Kamchatka",
	"New Zealand Standard Time":       "Pacific/Auckland",
	"UTC+12":                          "Etc/GMT-12",
	"Fiji Standard Time":              "Pacific/Fiji",
	"Chatham Islands Standard Time":   "Pacific/Chatham",
	"UTC+13":                          "Etc/GMT-13",
	"Tonga Standard Time":             "Pacific/Tongatapu",
	"Samoa Standard Time":             "Pacific/Apia",
	"Line Islands Standard Time":      "Pacific/Kiritimati",
}

// obsoleteWindowsZones are the Windows names windowsZones keeps for an IANA
// zone a current name also stands for; the inversion takes the current one.
var obsoleteWindowsZones = map[string]bool{
	"Mexico Standard Time":       true, // Central Standard Time (Mexico)
	"Mid-Atlantic Standard Time": true, // UTC-02
	"Armenian Standard Time":     true, // Caucasus Standard Time
	"Kamchatka Standard Time":    true, // Russia Time Zone 11
}

// territoryZones are CLDR windowsZones.xml's territory entries — an IANA
// zone a country's mapZone names under a Windows name, besides the golden
// zone windowsZones holds — for the zones the installations this serves are
// likely to name, and the current IANA spellings of windowsZones' old ones.
var territoryZones = map[string]string{
	// W. Europe Standard Time
	"Europe/Oslo": "W. Europe Standard Time", "Europe/Stockholm": "W. Europe Standard Time",
	"Europe/Amsterdam": "W. Europe Standard Time", "Europe/Vienna": "W. Europe Standard Time",
	"Europe/Zurich": "W. Europe Standard Time", "Europe/Rome": "W. Europe Standard Time",
	"Europe/Luxembourg": "W. Europe Standard Time", "Europe/Monaco": "W. Europe Standard Time",
	"Europe/Malta": "W. Europe Standard Time", "Europe/Andorra": "W. Europe Standard Time",
	"Europe/Gibraltar": "W. Europe Standard Time", "Europe/Vaduz": "W. Europe Standard Time",
	"Europe/San_Marino": "W. Europe Standard Time", "Europe/Vatican": "W. Europe Standard Time",
	"Europe/Busingen": "W. Europe Standard Time", "Arctic/Longyearbyen": "W. Europe Standard Time",
	// Romance Standard Time
	"Europe/Copenhagen": "Romance Standard Time", "Europe/Brussels": "Romance Standard Time",
	"Europe/Madrid": "Romance Standard Time", "Africa/Ceuta": "Romance Standard Time",
	// Central Europe Standard Time
	"Europe/Prague": "Central Europe Standard Time", "Europe/Bratislava": "Central Europe Standard Time",
	"Europe/Ljubljana": "Central Europe Standard Time", "Europe/Belgrade": "Central Europe Standard Time",
	"Europe/Podgorica": "Central Europe Standard Time", "Europe/Tirane": "Central Europe Standard Time",
	// Central European Standard Time
	"Europe/Zagreb": "Central European Standard Time", "Europe/Sarajevo": "Central European Standard Time",
	"Europe/Skopje": "Central European Standard Time",
	// GMT Standard Time
	"Europe/Dublin": "GMT Standard Time", "Europe/Lisbon": "GMT Standard Time",
	"Atlantic/Madeira": "GMT Standard Time", "Atlantic/Canary": "GMT Standard Time",
	"Atlantic/Faeroe": "GMT Standard Time", "Atlantic/Faroe": "GMT Standard Time",
	"Europe/Guernsey": "GMT Standard Time", "Europe/Isle_of_Man": "GMT Standard Time",
	"Europe/Jersey": "GMT Standard Time",
	// Greenwich Standard Time
	"Africa/Abidjan": "Greenwich Standard Time", "Africa/Accra": "Greenwich Standard Time",
	"Africa/Dakar": "Greenwich Standard Time", "Africa/Monrovia": "Greenwich Standard Time",
	"America/Danmarkshavn": "Greenwich Standard Time", "Atlantic/St_Helena": "Greenwich Standard Time",
	// FLE Standard Time
	"Europe/Helsinki": "FLE Standard Time", "Europe/Mariehamn": "FLE Standard Time",
	"Europe/Tallinn": "FLE Standard Time", "Europe/Riga": "FLE Standard Time",
	"Europe/Vilnius": "FLE Standard Time", "Europe/Sofia": "FLE Standard Time",
	"Europe/Kyiv": "FLE Standard Time", "Europe/Uzhgorod": "FLE Standard Time",
	"Europe/Zaporozhye": "FLE Standard Time",
	// GTB Standard Time
	"Europe/Athens": "GTB Standard Time", "Asia/Nicosia": "GTB Standard Time",
	"Asia/Famagusta": "GTB Standard Time",
	// Russian Standard Time
	"Europe/Kirov": "Russian Standard Time", "Europe/Simferopol": "Russian Standard Time",
	// Greenland, Atlantic
	"America/Nuuk": "Greenland Standard Time", "America/Thule": "Atlantic Standard Time",
	"Atlantic/Bermuda": "Atlantic Standard Time", "America/Moncton": "Atlantic Standard Time",
	"America/Glace_Bay": "Atlantic Standard Time", "America/Goose_Bay": "Atlantic Standard Time",
	// North America
	"America/Toronto": "Eastern Standard Time", "America/Montreal": "Eastern Standard Time",
	"America/Detroit": "Eastern Standard Time", "America/Nassau": "Eastern Standard Time",
	"America/Iqaluit": "Eastern Standard Time", "America/Louisville": "Eastern Standard Time",
	"America/Kentucky/Louisville":  "Eastern Standard Time",
	"America/Indiana/Indianapolis": "US Eastern Standard Time", "America/Indianapolis": "US Eastern Standard Time",
	"America/Winnipeg": "Central Standard Time", "America/Matamoros": "Central Standard Time",
	"America/Menominee": "Central Standard Time", "America/Indiana/Knox": "Central Standard Time",
	"America/Edmonton": "Mountain Standard Time", "America/Boise": "Mountain Standard Time",
	"America/Yellowknife": "Mountain Standard Time", "America/Ciudad_Juarez": "Mountain Standard Time",
	"America/Vancouver":  "Pacific Standard Time",
	"America/Hermosillo": "US Mountain Standard Time", "America/Creston": "US Mountain Standard Time",
	"America/Dawson_Creek": "US Mountain Standard Time", "America/Fort_Nelson": "US Mountain Standard Time",
	"America/Monterrey": "Central Standard Time (Mexico)", "America/Merida": "Central Standard Time (Mexico)",
	"America/Bahia_Banderas": "Central Standard Time (Mexico)",
	// South and Central America
	"America/Lima": "SA Pacific Standard Time", "America/Guayaquil": "SA Pacific Standard Time",
	"America/Panama": "SA Pacific Standard Time", "America/Jamaica": "SA Pacific Standard Time",
	"America/Cayman":                 "SA Pacific Standard Time",
	"America/Argentina/Buenos_Aires": "Argentina Standard Time", "America/Argentina/Cordoba": "Argentina Standard Time",
	"America/Cordoba":    "Argentina Standard Time",
	"America/Costa_Rica": "Central America Standard Time", "America/El_Salvador": "Central America Standard Time",
	"America/Tegucigalpa": "Central America Standard Time", "America/Managua": "Central America Standard Time",
	"America/Puerto_Rico": "SA Western Standard Time", "America/Santo_Domingo": "SA Western Standard Time",
	"America/Manaus": "SA Western Standard Time",
	// Africa and the Middle East
	"Africa/Algiers": "W. Central Africa Standard Time", "Africa/Tunis": "W. Central Africa Standard Time",
	"Africa/Kinshasa": "W. Central Africa Standard Time", "Africa/Luanda": "W. Central Africa Standard Time",
	"Africa/Maputo": "South Africa Standard Time", "Africa/Harare": "South Africa Standard Time",
	"Africa/Lusaka": "South Africa Standard Time", "Africa/Kigali": "South Africa Standard Time",
	"Africa/Addis_Ababa": "E. Africa Standard Time", "Africa/Dar_es_Salaam": "E. Africa Standard Time",
	"Africa/Kampala": "E. Africa Standard Time", "Africa/Mogadishu": "E. Africa Standard Time",
	"Asia/Qatar": "Arab Standard Time", "Asia/Kuwait": "Arab Standard Time",
	"Asia/Bahrain": "Arab Standard Time", "Asia/Aden": "Arab Standard Time",
	"Asia/Muscat": "Arabian Standard Time", "Asia/Tel_Aviv": "Israel Standard Time",
	// Asia
	"Asia/Kolkata": "India Standard Time", "Asia/Kathmandu": "Nepal Standard Time",
	"Asia/Yangon": "Myanmar Standard Time", "Asia/Hong_Kong": "China Standard Time",
	"Asia/Macau": "China Standard Time", "Asia/Kuala_Lumpur": "Singapore Standard Time",
	"Asia/Manila": "Singapore Standard Time", "Asia/Makassar": "Singapore Standard Time",
	"Asia/Brunei":  "Singapore Standard Time",
	"Asia/Jakarta": "SE Asia Standard Time", "Asia/Ho_Chi_Minh": "SE Asia Standard Time",
	"Asia/Saigon": "SE Asia Standard Time", "Asia/Phnom_Penh": "SE Asia Standard Time",
	"Asia/Vientiane": "SE Asia Standard Time", "Asia/Jayapura": "Tokyo Standard Time",
	"Asia/Almaty": "Central Asia Standard Time",
	// Oceania
	"Australia/Melbourne": "AUS Eastern Standard Time", "Australia/Canberra": "AUS Eastern Standard Time",
	"Australia/Lindeman": "E. Australia Standard Time", "Australia/Broken_Hill": "Cen. Australia Standard Time",
	"Antarctica/McMurdo": "New Zealand Standard Time", "Pacific/Guam": "West Pacific Standard Time",
	// UTC
	"UTC": "UTC", "Etc/GMT": "UTC", "GMT": "UTC", "Etc/UCT": "UTC", "Etc/Zulu": "UTC",
}

// ianaToWindows is windowsZones inverted — an obsolete name never chosen
// — with territoryZones over it, built once.
var ianaToWindows = sync.OnceValue(func() map[string]string {
	out := make(map[string]string, len(windowsZones)+len(territoryZones))
	for windows, iana := range windowsZones {
		if !obsoleteWindowsZones[windows] {
			out[iana] = windows
		}
	}
	for iana, windows := range territoryZones {
		out[iana] = windows
	}
	return out
})

// windowsZoneOf is the Windows name an all-day event is laid in for the
// IANA zone iana: the table's, "UTC" for "" and for a zone it does not know.
func windowsZoneOf(iana string) string {
	if w, ok := ianaToWindows()[iana]; ok {
		return w
	}
	return "UTC"
}
