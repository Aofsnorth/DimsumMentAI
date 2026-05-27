package bot

import "strings"

func playerNameMatches(known, query string) bool {
	known = cleanPlayerName(known)
	query = cleanPlayerName(query)
	if strings.EqualFold(known, query) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(query), " "+strings.ToLower(known))
}

func cleanPlayerName(name string) string {
	name = stripMinecraftFormatting(strings.TrimSpace(name))
	for strings.HasPrefix(name, "[") {
		end := strings.Index(name, "]")
		if end < 0 {
			break
		}
		name = strings.TrimSpace(name[end+1:])
	}
	if idx := strings.LastIndex(name, ">"); idx >= 0 {
		name = strings.TrimSpace(name[idx+1:])
	}
	return name
}

func stripMinecraftFormatting(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	skipCode := false
	for _, r := range s {
		if skipCode {
			skipCode = false
			continue
		}
		if r == '\u00a7' {
			skipCode = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
