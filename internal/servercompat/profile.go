package servercompat

import "strings"

type Profile struct {
	Venity      bool
	NetherGames bool
}

func Detect(host string) Profile {
	h := strings.ToLower(strings.TrimSpace(host))
	return Profile{
		Venity:      strings.Contains(h, "venity.net") || strings.Contains(h, "venity"),
		NetherGames: strings.Contains(h, "nethergames") || strings.Contains(h, "ngmc.co"),
	}
}
