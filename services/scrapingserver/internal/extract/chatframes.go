package extract

import (
	"net/url"
	"slices"
	"strings"
)

// chatHosts are sites whose frames are a conversation or a social widget beside the player, never the player.
// A host matches itself and its subdomains. Opening one as the page only loads a chat room, and clicking one starts nothing.
var chatHosts = []string{
	"chatango.com",
	"disqus.com",
	"tlk.io",
	"minnit.chat",
	"cbox.ws",
	"discord.com",
	"discordapp.com",
	"widgetbot.io",
	"facebook.com",
	"twitter.com",
	"x.com",
	"instagram.com",
}

// chatWords are words that name a chat in a frame's path (/embed/channel/chat, /live_chat, /comments), whatever the host.
var chatWords = []string{
	"chat",
	"chats",
	"comment",
	"comments",
}

// chatFrame is whether src is a chat or social widget rather than a player.
func chatFrame(src string) bool {
	u, err := url.Parse(src)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range chatHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	words := strings.FieldsFunc(strings.ToLower(u.Path), func(r rune) bool { return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) })
	return slices.ContainsFunc(words, func(w string) bool { return slices.Contains(chatWords, w) })
}
