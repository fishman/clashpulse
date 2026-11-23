package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Outcome string

const (
	Usable  Outcome = "usable"
	Blocked Outcome = "blocked"
	Failed  Outcome = "error"
)

type Rule string

const (
	RuleTraceLoc      Rule = "trace_loc"
	RuleBodyMarker    Rule = "body_marker"
	RuleStatusPair    Rule = "status_pair"
	RuleJSONCountry   Rule = "json_country"
	RuleGoogleCountry Rule = "google_country"
)

// Request is one GET of a check. Redirects 0 means a 3xx is the final
// response and its status and Location are the signal.
type Request struct {
	URL       string
	MaxBytes  int64
	Redirects int
}

type Definition struct {
	ID, Name string
	Rule     Rule
	Requests []Request
	Blocked  []string // ISO 3166-1 alpha-2
	Markers  []string // lower-case body phrases meaning "refused here"
	Hosts    []string // redirect targets: equal to or a subdomain of one
	Domains  []string // default DOMAIN-SUFFIX routing set
	Shares   string   // ID whose read this definition evaluates
}

type Result struct {
	Service string
	Outcome Outcome
	Region  string
}

var errPolicy = errors.New("services: redirect outside policy")

type response struct {
	status   int
	location string
	final    *url.URL
	body     []byte
}

// ProxyTransport sends every request through one pinned probe listener.
func ProxyTransport(proxy *url.URL) *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyURL(proxy),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// Check runs defs for one node through transport. Each read gets its own
// cookie jar and CheckTimeout budget; a definition with Shares evaluates the
// read of the definition it names, fetching it once when absent.
func Check(ctx context.Context, transport http.RoundTripper, defs []Definition) []Result {
	reads := make(map[string][]response)
	results := make([]Result, len(defs))
	for i, def := range defs {
		source := def
		if def.Shares != "" {
			index := slices.IndexFunc(defs, func(other Definition) bool { return other.ID == def.Shares })
			if index >= 0 {
				source = defs[index]
			} else {
				source, _ = Lookup(def.Shares)
			}
		}
		responses, ok := reads[source.ID]
		if !ok {
			responses = fetchAll(ctx, transport, source)
			reads[source.ID] = responses
		}
		results[i] = evaluate(def, responses)
	}
	return results
}

func fetchAll(ctx context.Context, transport http.RoundTripper, def Definition) []response {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	out := make([]response, 0, len(def.Requests))
	for _, request := range def.Requests {
		got, err := fetch(ctx, transport, jar, def.Hosts, request)
		if err != nil {
			return nil
		}
		out = append(out, got)
	}
	return out
}

func fetch(ctx context.Context, transport http.RoundTripper, jar http.CookieJar, hosts []string, request Request) (response, error) {
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if request.Redirects == 0 {
			return http.ErrUseLastResponse
		}
		if len(via) > request.Redirects || next.URL.Scheme != "https" || !allowedHost(next.URL.Hostname(), hosts) {
			return errPolicy
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return response{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, request.MaxBytes))
	if err != nil {
		return response{}, err
	}
	if request.Redirects > 0 && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return response{}, errPolicy
	}
	return response{status: resp.StatusCode, location: resp.Header.Get("Location"), final: resp.Request.URL, body: body}, nil
}

func allowedHost(host string, hosts []string) bool {
	host = strings.ToLower(host)
	for _, allowed := range hosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func evaluate(def Definition, responses []response) Result {
	result := Result{Service: def.ID, Outcome: Failed}
	if len(responses) == 0 {
		return result
	}
	switch def.Rule {
	case RuleTraceLoc, RuleGoogleCountry:
		first := responses[0]
		if def.Rule == RuleTraceLoc {
			result.Region = traceLoc(first.body)
		} else {
			result.Region = quotedField(first.body, `"GL":"`)
		}
		if first.status/100 == 2 && result.Region != "" {
			result.Outcome = verdict(def.Blocked, result.Region)
		}
	case RuleBodyMarker:
		last := responses[len(responses)-1]
		if len(responses) > 1 {
			result.Region = traceLoc(responses[0].body)
		} else {
			result.Region = quotedField(last.body, `"GL":"`)
		}
		switch {
		case refused(last, def.Markers):
			result.Outcome = Blocked
		case last.status/100 != 2:
		case len(responses) > 1 || result.Region != "":
			result.Outcome = Usable
		}
	case RuleStatusPair:
		if len(responses) < 3 {
			return result
		}
		first, second := responses[0].status, responses[1].status
		switch {
		case first == http.StatusForbidden || second == http.StatusForbidden:
			result.Outcome = Blocked
		case first == http.StatusNotFound && second == http.StatusNotFound:
			result.Outcome = Blocked
		case available(first) || available(second):
			result.Outcome = Usable
			result.Region = localePath(responses[2].location)
		}
	case RuleJSONCountry:
		last := responses[0]
		result.Region = quotedField(last.body, `"countryCode":"`)
		if result.Region == "" && last.final != nil {
			result.Region = localePath(last.final.String())
		}
		switch {
		case refused(last, def.Markers):
			result.Outcome = Blocked
		case last.status/100 != 2:
		default:
			result.Outcome = Usable
		}
	}
	if result.Outcome == Failed {
		result.Region = ""
	}
	return result
}

// refused reports a refusal: a marker in the body, whatever the status, or a
// 403 or 451.
func refused(last response, markers []string) bool {
	return last.status == http.StatusForbidden || last.status == http.StatusUnavailableForLegalReasons || hasMarker(last.body, markers)
}

func available(status int) bool {
	return status/100 == 2 || status == http.StatusMovedPermanently || status == http.StatusFound
}

func verdict(blocked []string, region string) Outcome {
	if slices.Contains(blocked, region) {
		return Blocked
	}
	return Usable
}

func traceLoc(body []byte) string {
	for _, line := range bytes.Split(body, []byte("\n")) {
		if value, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("loc=")); ok {
			return region(string(value))
		}
	}
	return ""
}

func quotedField(body []byte, key string) string {
	_, rest, ok := bytes.Cut(body, []byte(key))
	if !ok {
		return ""
	}
	value, _, ok := bytes.Cut(rest, []byte(`"`))
	if !ok {
		return ""
	}
	return region(string(value))
}

// localePath reads a region from a locale path segment such as /us-en/.
func localePath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segment, _, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	code, _, _ := strings.Cut(segment, "-")
	return region(code)
}

func hasMarker(body []byte, markers []string) bool {
	lower := bytes.ToLower(body)
	for _, marker := range markers {
		if bytes.Contains(lower, []byte(marker)) {
			return true
		}
	}
	return false
}

// region accepts only a two-letter code, so no other body text leaves the package.
func region(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 || value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return ""
	}
	return value
}
