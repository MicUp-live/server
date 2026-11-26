package reference

import "strings"

// ResolveProvider identifies known karaoke backing track manufacturers from disc codes or brand tags.
// Returns empty string if no authoritative provider indicator is present.
func ResolveProvider(discCode, brandTag string) string {
	code := strings.ToUpper(strings.TrimSpace(discCode))
	brand := strings.ToUpper(strings.TrimSpace(brandTag))
	combined := strings.TrimSpace(brand + " " + code)

	switch {
	case strings.Contains(combined, "SOUND CHOICE") || strings.HasPrefix(code, "SC") || strings.HasPrefix(code, "NSC") || strings.HasPrefix(code, "VPSC") || strings.HasPrefix(code, "RSQSC") || strings.HasPrefix(code, "VPS") || strings.HasPrefix(code, "CRK") || strings.HasPrefix(code, "SAV") || strings.HasPrefix(code, "SDK"):
		return "Sound Choice"
	case strings.Contains(combined, "SUNFLY SYBERSOUND") || strings.HasPrefix(code, "SYB"):
		return "Sunfly Sybersound"
	case strings.Contains(combined, "SUNFLY") || strings.HasPrefix(code, "SF"):
		return "Sunfly"
	case strings.Contains(combined, "CHARTBUSTER") || strings.HasPrefix(code, "CB"):
		return "Chartbuster"
	case strings.Contains(combined, "POP HITS MONTHLY") || strings.HasPrefix(code, "PHM"):
		return "Pop Hits Monthly"
	case strings.Contains(combined, "TOP HITS MONTHLY") || strings.HasPrefix(code, "THM") || strings.HasPrefix(code, "TH"):
		return "Top Hits Monthly"
	case strings.Contains(combined, "MUSIC MAESTRO") || strings.HasPrefix(code, "MM"):
		return "Music Maestro"
	case strings.Contains(combined, "POCKET SONGS") || strings.HasPrefix(code, "PC") || strings.HasPrefix(code, "PS"):
		return "Pocket Songs"
	case strings.Contains(combined, "DKKARAOKE") || strings.HasPrefix(code, "DK"):
		return "DKKaraoke"
	case strings.Contains(combined, "PARTY TYME") || strings.HasPrefix(code, "PT") || strings.HasPrefix(code, "PH") || strings.HasPrefix(code, "PY"):
		return "Party Tyme"
	case strings.Contains(combined, "KARAFUN") || strings.HasPrefix(code, "KFN"):
		return "KaraFun"
	case strings.Contains(combined, "KARAOKE VERSION") || strings.HasPrefix(code, "KV"):
		return "Karaoke Version"
	case strings.Contains(combined, "LEGENDS") || strings.HasPrefix(code, "LG"):
		return "Legends"
	case strings.Contains(combined, "ZOOM") || strings.HasPrefix(code, "ZM"):
		return "Zoom Karaoke"
	case strings.Contains(combined, "MR ENTERTAINER"):
		return "Mr Entertainer"
	default:
		return ""
	}
}
