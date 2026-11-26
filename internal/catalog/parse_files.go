package catalog

import (
	"regexp"
	"strings"
)

const knownDiscCodePattern = `(?:(?:CBEP|CBE|CB|SCTP|NSC)\d{1,6}[A-Z]?(?:[-_]\d{1,3}){0,2}|(?:(?:VP|RSQ)?SC(?:VP|PC|GPX|RSS)?|CRK|VPS|SAV|SDK|SYB|SF|PHM|THM|TH|MM|PC|PS|DK|PT|PH|PY|LG|ZM|KV)\d{1,6}[A-Z0-9]*(?:[-_]\d{1,3})?|(?:SCDEMO|SCFPI)[-_]\d{1,3}|KFN[-_]?\d{2,6})`

var (
	bracketedDiscCodeRe = regexp.MustCompile(`(?i)\s*[\(\[]\s*(` + knownDiscCodePattern + `)\s*[\)\]]`)
	leadingDiscCodeRe   = regexp.MustCompile(`(?i)^\s*(` + knownDiscCodePattern + `)\s*(?:[-–—]+|_+)\s*`)
	trailingDiscCodeRe  = regexp.MustCompile(`(?i)\s*(?:[-–—]+|_+)\s*(` + knownDiscCodePattern + `)\s*$`)
)

// extractDiscCodeFromRawStem extracts a recognized manufacturer disc code from
// the raw filename stem BEFORE delimiter splitting or underscore replacement,
// preserving underscore-delimited, multipart, and letter-suffixed track codes.
func extractDiscCodeFromRawStem(stem string) (disc string, cleaned string) {
	if m := bracketedDiscCodeRe.FindStringSubmatchIndex(stem); m != nil {
		disc = strings.ToUpper(strings.TrimSpace(stem[m[2]:m[3]]))
		cleaned = strings.TrimSpace(stem[:m[0]] + " " + stem[m[1]:])
		return disc, cleaned
	}
	if m := leadingDiscCodeRe.FindStringSubmatchIndex(stem); m != nil {
		rest := strings.TrimSpace(stem[m[1]:])
		if rest != "" {
			return strings.ToUpper(strings.TrimSpace(stem[m[2]:m[3]])), rest
		}
	}
	if m := trailingDiscCodeRe.FindStringSubmatchIndex(stem); m != nil {
		rest := strings.TrimSpace(stem[:m[0]])
		if rest != "" {
			return strings.ToUpper(strings.TrimSpace(stem[m[2]:m[3]])), rest
		}
	}
	return "", stem
}
