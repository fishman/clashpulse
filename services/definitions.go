// Package services checks whether a node is usable for a named service. The
// definitions are fixed data: no user-supplied URL or code reaches a check.
package services

import "time"

// UserAgent is a fixed desktop browser string: services can serve a
// different page to Go's default agent.
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// CheckTimeout bounds one service check across all its requests and redirects.
const CheckTimeout = 15 * time.Second

const maxRedirects = 10

var definitions = []Definition{
	{ID: "chatgpt", Name: "ChatGPT", Rule: RuleBodyMarker, Markers: []string{"unsupported_country"},
		Hosts:    []string{"openai.com", "chatgpt.com"},
		Requests: []Request{{URL: "https://chat.openai.com/cdn-cgi/trace", MaxBytes: 4 << 10, Redirects: maxRedirects}, {URL: "https://api.openai.com/compliance/cookie_requirements", MaxBytes: 4 << 10, Redirects: maxRedirects}},
		Domains:  []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"}},
	{ID: "claude", Name: "Claude", Rule: RuleTraceLoc, Blocked: []string{"AF", "BY", "CN", "CU", "HK", "IR", "KP", "MO", "RU", "SY"},
		Hosts:    []string{"claude.ai", "anthropic.com"},
		Requests: []Request{{URL: "https://claude.ai/cdn-cgi/trace", MaxBytes: 4 << 10, Redirects: maxRedirects}},
		Domains:  []string{"claude.ai", "anthropic.com", "claudeusercontent.com"}},
	{ID: "gemini", Name: "Gemini", Rule: RuleGoogleCountry, Shares: "youtube", Blocked: []string{"BY", "CN", "CU", "HK", "IR", "KP", "MO", "RU", "SY"},
		Domains: []string{"gemini.google.com", "bard.google.com", "generativelanguage.googleapis.com"}},
	{ID: "youtube", Name: "YouTube Premium", Rule: RuleBodyMarker, Markers: []string{"premium is not available in your region"},
		Hosts:    []string{"youtube.com", "google.com"},
		Requests: []Request{{URL: "https://www.youtube.com/premium?hl=en", MaxBytes: 96 << 10, Redirects: maxRedirects}},
		Domains:  []string{"youtube.com", "youtu.be", "googlevideo.com", "ytimg.com", "youtubei.googleapis.com"}},
	{ID: "netflix", Name: "Netflix", Rule: RuleStatusPair, Hosts: []string{"netflix.com"},
		Requests: []Request{{URL: "https://www.netflix.com/title/81280792"}, {URL: "https://www.netflix.com/title/70143836"}, {URL: "https://www.netflix.com/title/80018499"}},
		Domains:  []string{"netflix.com", "netflix.net", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net"}},
	{ID: "spotify", Name: "Spotify", Rule: RuleJSONCountry, Markers: []string{"not available in your country"},
		Hosts:    []string{"spotify.com"},
		Requests: []Request{{URL: "https://www.spotify.com/api/content/v1/country-selector?platform=web&format=json", MaxBytes: 16 << 10, Redirects: maxRedirects}},
		Domains:  []string{"spotify.com", "scdn.co", "spotifycdn.com", "spoti.fi"}},
}

// Definitions returns the built-in services in display order.
func Definitions() []Definition {
	return append([]Definition(nil), definitions...)
}

func Lookup(id string) (Definition, bool) {
	for _, def := range definitions {
		if def.ID == id {
			return def, true
		}
	}
	return Definition{}, false
}

func IDs() []string {
	ids := make([]string, len(definitions))
	for i, def := range definitions {
		ids[i] = def.ID
	}
	return ids
}
