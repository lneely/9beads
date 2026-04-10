package p9

import "strings"

// lintDescription checks a bead description for quality signals and returns
// a list of warning messages. An empty slice means the description passed.
func lintDescription(description string) []string {
	var warnings []string
	lower := strings.ToLower(description)

	hasFilePath := strings.Contains(description, ".go") ||
		strings.Contains(description, ".py") ||
		strings.Contains(description, ".ts") ||
		strings.Contains(description, ".js") ||
		strings.Contains(description, ".rs") ||
		strings.Contains(description, "/cmd/") ||
		strings.Contains(description, "/internal/") ||
		strings.Contains(description, "/src/") ||
		strings.Contains(description, "/pkg/")
	if !hasFilePath {
		warnings = append(warnings, "missing file path (add .go/.py/etc or /cmd//internal/ to help bots locate the code)")
	}

	hasFuncOrLine := strings.Contains(description, "()") ||
		strings.Contains(description, "func ") ||
		strings.Contains(lower, "line ") ||
		strings.Contains(lower, ":line") ||
		containsLineRef(description) ||
		containsAcmeRegexAddr(description)
	if !hasFuncOrLine {
		warnings = append(warnings, "missing function name or location (add func name, Acme address file.go:123, or L123)")
	}

	if len(description) < 80 {
		warnings = append(warnings, "description too short (aim for 80+ chars with What/Where/How/Accept)")
	}

	acceptKeywords := []string{"should", "returns", "displays", "must", "assert", "verify", "accept", "expect"}
	hasAccept := false
	for _, kw := range acceptKeywords {
		if strings.Contains(lower, kw) {
			hasAccept = true
			break
		}
	}
	if !hasAccept {
		warnings = append(warnings, "missing acceptance criterion (add: should/returns/must/accept)")
	}

	if containsHyphenRange(description) {
		warnings = append(warnings, "invalid Acme address: use comma range file.go:123,125 not file.go:123-125")
	}

	nonImperativeStarters := []string{
		"need to ", "needs to ", "should ", "we need", "we want", "we should",
		"the ", "this ", "looking at", "looking into",
	}
	firstWordLower := strings.ToLower(strings.TrimSpace(description))
	for _, starter := range nonImperativeStarters {
		if strings.HasPrefix(firstWordLower, starter) {
			warnings = append(warnings, "start with imperative verb (Fix/Add/Update/Refactor) not '"+starter+"...'")
			break
		}
	}

	vaguePhrases := []string{
		"somehow", " maybe ", "probably ", "try to ", " a bit ", " etc.", " etc,",
		"and so on", " stuff", "some kind of", "whatever", "sort of ", "kind of ",
	}
	for _, phrase := range vaguePhrases {
		if strings.Contains(lower, phrase) {
			warnings = append(warnings, "vague language '"+strings.TrimSpace(phrase)+"': replace with specific behavior")
			break
		}
	}

	howSignals := []string{
		"following ", "pattern from", "same as ", "similar to ", "like in ",
		"mirrors ", "as in ", "modeled on", "following the ", "see ", "cf.",
	}
	hasHow := false
	for _, sig := range howSignals {
		if strings.Contains(lower, sig) {
			hasHow = true
			break
		}
	}
	if !hasHow {
		warnings = append(warnings, "missing 'how' signal (add: 'following pattern in X' or 'same as Y')")
	}

	firstPersonPhrases := []string{
		"i need", "i want", "i think", "i'll ", "i will ", "i should",
		"we need", "we want", "we should", "we'll ", "we will ",
	}
	for _, fp := range firstPersonPhrases {
		if strings.Contains(lower, fp) {
			warnings = append(warnings, "avoid first-person ('"+strings.TrimSpace(fp)+"'): use imperative voice")
			break
		}
	}

	forbiddenPhrases := []string{
		"fix this", "fix it", "update this", "make it work", "clean this up",
		"refactor this", "look at this", "deal with this", "handle this",
	}
	for _, fp := range forbiddenPhrases {
		if strings.Contains(lower, fp) {
			warnings = append(warnings, "forbidden vague phrase '"+fp+"': specify What/Where/How/Accept")
			break
		}
	}

	if hasFilePath && !strings.Contains(description, "`") {
		warnings = append(warnings, "no inline code found: wrap identifiers in backticks (`funcName()`, `--flag`)")
	}

	if len(description) > 150 {
		hasBdRef := containsBdRef(description)
		hasURL := strings.Contains(lower, "http://") || strings.Contains(lower, "https://")
		if !hasBdRef && !hasURL {
			warnings = append(warnings, "long description missing cross-reference (add bd-XXX or URL in Refs)")
		}
	}

	return warnings
}

func containsLineRef(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		c := s[i]
		next := s[i+1]
		if (c == 'L' || c == ':' || c == '#') && next >= '0' && next <= '9' {
			return true
		}
	}
	return false
}

func containsAcmeRegexAddr(s string) bool {
	const minPatternLen = 4
	for i := 0; i < len(s)-2; i++ {
		if s[i] != '/' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != '/' && s[j] != ' ' && s[j] != '\n' {
			j++
		}
		if j < len(s) && s[j] == '/' && (j-i-1) >= minPatternLen {
			return true
		}
	}
	return false
}

func containsHyphenRange(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			continue
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i < len(s)-1 && s[i] == '-' && s[i+1] >= '0' && s[i+1] <= '9' {
			return true
		}
	}
	return false
}

func containsBdRef(s string) bool {
	lower := strings.ToLower(s)
	idx := strings.Index(lower, "bd-")
	for idx != -1 {
		if idx+3 < len(lower) && isAlphanumeric(lower[idx+3]) {
			return true
		}
		next := strings.Index(lower[idx+1:], "bd-")
		if next == -1 {
			break
		}
		idx = idx + 1 + next
	}
	return false
}

func isAlphanumeric(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
