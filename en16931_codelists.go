package pdf0

// This file holds the EN 16931 code lists used by the BR-CL-* code-list rules,
// transcribed verbatim from the official CEN/TC 434 validation Schematron
// (ConnectingEurope/eInvoicing-EN16931, EUPL-1.2). en16931_codelists_test.go
// re-extracts them from the Schematron when the artefact suite is present and
// asserts these tables match, so they stay faithful to the source of truth.

// en16931Currencies is the ISO 4217 alpha-3 currency set EN 16931 permits
// (BR-CL-03/04/05).
var en16931Currencies = map[string]bool{
	"AED": true, "AFN": true, "ALL": true, "AMD": true, "AOA": true, "ARS": true, "AUD": true, "AWG": true,
	"AZN": true, "BAM": true, "BBD": true, "BDT": true, "BHD": true, "BIF": true, "BMD": true, "BND": true,
	"BOB": true, "BOV": true, "BRL": true, "BSD": true, "BTN": true, "BWP": true, "BYN": true, "BZD": true,
	"CAD": true, "CDF": true, "CHE": true, "CHF": true, "CHW": true, "CLF": true, "CLP": true, "CNH": true,
	"CNY": true, "COP": true, "COU": true, "CRC": true, "CUP": true, "CVE": true, "CZK": true, "DJF": true,
	"DKK": true, "DOP": true, "DZD": true, "EGP": true, "ERN": true, "ETB": true, "EUR": true, "FJD": true,
	"FKP": true, "GBP": true, "GEL": true, "GHS": true, "GIP": true, "GMD": true, "GNF": true, "GTQ": true,
	"GYD": true, "HKD": true, "HNL": true, "HTG": true, "HUF": true, "IDR": true, "ILS": true, "INR": true,
	"IQD": true, "IRR": true, "ISK": true, "JMD": true, "JOD": true, "JPY": true, "KES": true, "KGS": true,
	"KHR": true, "KMF": true, "KPW": true, "KRW": true, "KWD": true, "KYD": true, "KZT": true, "LAK": true,
	"LBP": true, "LKR": true, "LRD": true, "LSL": true, "LYD": true, "MAD": true, "MDL": true, "MGA": true,
	"MKD": true, "MMK": true, "MNT": true, "MOP": true, "MRU": true, "MUR": true, "MVR": true, "MWK": true,
	"MXN": true, "MXV": true, "MYR": true, "MZN": true, "NAD": true, "NGN": true, "NIO": true, "NOK": true,
	"NPR": true, "NZD": true, "OMR": true, "PAB": true, "PEN": true, "PGK": true, "PHP": true, "PKR": true,
	"PLN": true, "PYG": true, "QAR": true, "RON": true, "RSD": true, "RUB": true, "RWF": true, "SAR": true,
	"SBD": true, "SCR": true, "SDG": true, "SEK": true, "SGD": true, "SHP": true, "SLE": true, "SOS": true,
	"SRD": true, "SSP": true, "STD": true, "SVC": true, "SYP": true, "SZL": true, "THB": true, "TJS": true,
	"TMT": true, "TND": true, "TOP": true, "TRY": true, "TTD": true, "TWD": true, "TZS": true, "UAH": true,
	"UGX": true, "USD": true, "USN": true, "UYI": true, "UYU": true, "UYW": true, "UZS": true, "VES": true,
	"VED": true, "VND": true, "VUV": true, "WST": true, "XAF": true, "XAG": true, "XAU": true, "XBA": true,
	"XBB": true, "XBC": true, "XBD": true, "XCD": true, "XCG": true, "XDR": true, "XOF": true, "XPD": true,
	"XPF": true, "XPT": true, "XSU": true, "XTS": true, "XUA": true, "XXX": true, "YER": true, "ZAR": true,
	"ZMW": true, "ZWG": true,
}

// en16931TypeCodes is the permitted UNTDID 1001 document type code set for the
// Invoice/Credit note type code BT-3 (BR-CL-01); it is the CII union of the UBL
// invoice and credit-note code lists.
var en16931TypeCodes = map[string]bool{
	"71": true, "80": true, "81": true, "82": true, "83": true, "84": true, "102": true, "130": true,
	"202": true, "203": true, "204": true, "211": true, "218": true, "219": true, "261": true, "262": true,
	"295": true, "296": true, "308": true, "325": true, "326": true, "331": true, "380": true, "381": true,
	"382": true, "383": true, "384": true, "385": true, "386": true, "387": true, "388": true, "389": true,
	"390": true, "393": true, "394": true, "395": true, "396": true, "420": true, "456": true, "457": true,
	"458": true, "471": true, "472": true, "473": true, "500": true, "501": true, "502": true, "503": true,
	"527": true, "532": true, "553": true, "575": true, "623": true, "633": true, "751": true, "780": true,
	"817": true, "870": true, "875": true, "876": true, "877": true, "935": true,
}
