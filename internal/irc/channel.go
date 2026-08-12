package irc

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ChannelFromMPLink derives the only IRC channel that may control a room.
// The room ID is persisted in the official osu! multiplayer URL.
func ChannelFromMPLink(link string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(link))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "osu.ppy.sh") || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid multiplayer link")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "community" || parts[1] != "matches" || parts[2] == "" {
		return "", fmt.Errorf("multiplayer link has no room ID")
	}
	id := parts[2]
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return "", fmt.Errorf("multiplayer link has no positive numeric room ID")
	}
	return "#mp_" + id, nil
}

func MatchChannel(channel string) bool {
	if !strings.HasPrefix(channel, "#mp_") {
		return false
	}
	id := strings.TrimPrefix(channel, "#mp_")
	value, err := strconv.ParseInt(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
}
