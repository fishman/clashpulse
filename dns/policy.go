package dns

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/filters"
	resourcepkg "github.com/fishman/clashpulse/resources"
)

// Policy maps deterministic Mihomo nameserver-policy matchers to named resolver
// set endpoints.
type Policy map[string][]string

// Build validates DNS routing references and returns deterministic policy data
// for rendering. A resource matcher must be an enabled domain rule-set.
func Build(snapshot config.Snapshot) (Policy, error) {
	if err := validateListen(snapshot.DNS.Listen); err != nil {
		return nil, err
	}
	sets := make(map[string][]string, len(snapshot.DNS.ResolverSets))
	for _, set := range snapshot.DNS.ResolverSets {
		if !validID(set.ID) || len(set.Endpoints) == 0 {
			return nil, fmt.Errorf("dns: invalid or empty resolver set %q", set.ID)
		}
		if _, duplicate := sets[set.ID]; duplicate {
			return nil, fmt.Errorf("dns: duplicate resolver set %q", set.ID)
		}
		for _, raw := range set.Endpoints {
			parsed, err := parseEndpoint(raw)
			if err != nil {
				return nil, fmt.Errorf("dns: resolver set %q: %w", set.ID, err)
			}
			if set.DNSCrypt && (!isLoopback(parsed.host) || (parsed.network != "udp" && parsed.network != "tcp")) {
				return nil, fmt.Errorf("dns: DNSCrypt listener must be a loopback UDP or TCP endpoint")
			}
		}
		sets[set.ID] = append([]string(nil), set.Endpoints...)
	}
	resources := make(map[string]config.Resource, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		if !validID(resource.ID) {
			return nil, fmt.Errorf("dns: invalid resource ID %q", resource.ID)
		}
		if _, duplicate := resources[resource.ID]; duplicate {
			return nil, fmt.Errorf("dns: duplicate resource ID %q", resource.ID)
		}
		resources[resource.ID] = resource
	}
	listenHost, listenPort, _ := net.SplitHostPort(snapshot.DNS.Listen)
	listenPortNumber, _ := strconv.Atoi(listenPort)
	listenPort = strconv.Itoa(listenPortNumber)
	for _, set := range snapshot.DNS.ResolverSets {
		for _, raw := range set.Endpoints {
			parsed, err := parseEndpoint(raw)
			if err != nil {
				return nil, err
			}
			if set.DNSCrypt && !isLoopback(parsed.host) {
				return nil, fmt.Errorf("dns: DNSCrypt listener must be loopback")
			}
			if parsed.port == listenPort && isLoopback(parsed.host) && isLocalListen(listenHost) {
				return nil, fmt.Errorf("dns: resolver endpoint %q loops back to dns.listen", raw)
			}
		}
	}
	policy := make(Policy, len(snapshot.DNS.Routes))
	for _, route := range snapshot.DNS.Routes {
		if !validID(route.ResolverSet) {
			return nil, fmt.Errorf("dns: route has no valid resolver set")
		}
		endpoints, ok := sets[route.ResolverSet]
		if !ok {
			return nil, fmt.Errorf("dns: unknown resolver set %q", route.ResolverSet)
		}
		matchers := 0
		key := ""
		if route.Suffix != "" {
			matchers++
			if !validDomain(route.Suffix) {
				return nil, fmt.Errorf("dns: invalid suffix %q", route.Suffix)
			}
			key = "+." + strings.TrimPrefix(strings.TrimSuffix(route.Suffix, "."), ".")
		}
		if route.GeoSite != "" {
			matchers++
			if !validToken(route.GeoSite) {
				return nil, fmt.Errorf("dns: invalid GeoSite selector %q", route.GeoSite)
			}
			key = "geosite:" + route.GeoSite
		}
		if route.Resource != "" {
			matchers++
			resource, exists := resources[route.Resource]
			if !exists || !resource.Enabled || resource.Kind != config.ResourceRuleSet || resource.RuleType != config.RuleDomain {
				return nil, fmt.Errorf("dns: route resource %q must be an enabled domain rule-set", route.Resource)
			}
			if err := resourcepkg.ValidateDeclaration(resource); err != nil {
				return nil, fmt.Errorf("dns: route resource %q has an invalid declaration: %w", route.Resource, err)
			}
			key = "rule-set:" + filters.Name(route.Resource)
		}
		if matchers != 1 {
			return nil, fmt.Errorf("dns: route must select exactly one suffix, GeoSite, or domain rule-set resource")
		}
		if _, duplicate := policy[key]; duplicate {
			return nil, fmt.Errorf("dns: duplicate route matcher %q", key)
		}
		policy[key] = append([]string(nil), endpoints...)
	}
	return policy, nil
}

// Validate checks routing policy and managed resource availability, then probes
// every explicitly configured local DNSCrypt endpoint before policy promotion.
func Validate(ctx context.Context, snapshot config.Snapshot, managedPaths map[string]string) error {
	if _, err := Build(snapshot); err != nil {
		return err
	}
	resources := make(map[string]config.Resource, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		resources[resource.ID] = resource
	}
	managedHome := ""
	for _, route := range snapshot.DNS.Routes {
		if route.Resource == "" {
			continue
		}
		path := managedPaths[route.Resource]
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("dns: required rule-set resource %q has no managed absolute path", route.Resource)
		}
		resource := resources[route.Resource]
		if filepath.Base(path) != resourcepkg.ManagedFilename(resource) {
			return fmt.Errorf("dns: rule-set resource %q path is not its registry destination", route.Resource)
		}
		if managedHome == "" {
			managedHome = filepath.Dir(path)
		} else if filepath.Dir(path) != managedHome {
			return fmt.Errorf("dns: rule-set resources do not share one managed home")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("dns: required rule-set resource %q is unavailable", route.Resource)
		}
	}
	for _, set := range snapshot.DNS.ResolverSets {
		for _, raw := range set.Endpoints {
			endpoint, err := parseEndpoint(raw)
			if err != nil {
				return err
			}
			if !set.DNSCrypt {
				continue
			}
			if err := probeDNSCrypt(ctx, endpoint); err != nil {
				return fmt.Errorf("dns: configured DNSCrypt listener %q is unavailable: %w", raw, err)
			}
		}
	}
	return nil
}

// SortedKeys returns policy matchers in deterministic order for YAML emitters.
func (p Policy) SortedKeys() []string {
	keys := make([]string, 0, len(p))
	for key := range p {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type endpoint struct {
	host    string
	port    string
	network string
}

func parseEndpoint(raw string) (endpoint, error) {
	if !strings.Contains(raw, "://") {
		host, port, err := net.SplitHostPort(raw)
		if err != nil || host == "" || !validPort(port) {
			return endpoint{}, fmt.Errorf("dns: invalid resolver endpoint")
		}
		portNumber, _ := strconv.Atoi(port)
		return endpoint{host: host, port: strconv.Itoa(portNumber), network: "udp"}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Opaque != "" {
		return endpoint{}, fmt.Errorf("dns: invalid resolver endpoint")
	}
	network := strings.ToLower(u.Scheme)
	switch network {
	case "udp", "tcp", "tls", "https", "quic":
	default:
		return endpoint{}, fmt.Errorf("dns: unsupported resolver scheme %q", network)
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		return endpoint{}, fmt.Errorf("dns: invalid resolver host")
	}
	if port == "" {
		switch network {
		case "https":
			port = "443"
		case "tls", "quic":
			port = "853"
		default:
			port = "53"
		}
	}
	if !validPort(port) {
		return endpoint{}, fmt.Errorf("dns: invalid resolver port")
	}
	if network != "https" && u.Path != "" && u.Path != "/" {
		return endpoint{}, fmt.Errorf("dns: unexpected resolver path")
	}
	portNumber, _ := strconv.Atoi(port)
	return endpoint{host: host, port: strconv.Itoa(portNumber), network: network}, nil
}

func probeDNSCrypt(ctx context.Context, endpoint endpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, endpoint.network, net.JoinHostPort(endpoint.host, endpoint.port))
	if err != nil {
		return err
	}
	defer connection.Close()
	stopCancel := context.AfterFunc(probeCtx, func() { _ = connection.SetDeadline(time.Now()) })
	defer stopCancel()
	deadline := time.Now().Add(2 * time.Second)
	if contextDeadline, ok := probeCtx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	query, id, err := dnsProbeQuery()
	if err != nil {
		return err
	}
	if endpoint.network == "tcp" {
		packet := make([]byte, len(query)+2)
		binary.BigEndian.PutUint16(packet[:2], uint16(len(query)))
		copy(packet[2:], query)
		if err := writeAll(connection, packet); err != nil {
			return err
		}
		var size [2]byte
		if _, err := io.ReadFull(connection, size[:]); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint16(size[:]))
		if length < 12 || length > 4096 {
			return fmt.Errorf("dns: invalid DNS response length")
		}
		response := make([]byte, length)
		if _, err := io.ReadFull(connection, response); err != nil {
			return err
		}
		return validateDNSResponse(response, id)
	}
	if _, err := connection.Write(query); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, err := connection.Read(response)
	if err != nil {
		return err
	}
	return validateDNSResponse(response[:n], id)
}

func dnsProbeQuery() ([]byte, uint16, error) {
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, 0, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	query := make([]byte, 12)
	binary.BigEndian.PutUint16(query[:2], id)
	query[2] = 1
	query[5] = 1
	query = append(query, 10)
	query = append(query, "clashpulse"...)
	query = append(query, 7)
	query = append(query, "invalid"...)
	query = append(query, 0, 0, 1, 0, 1)
	return query, id, nil
}

func validateDNSResponse(response []byte, id uint16) error {
	if len(response) < 12 || binary.BigEndian.Uint16(response[:2]) != id || binary.BigEndian.Uint16(response[2:4])&0x8000 == 0 {
		return fmt.Errorf("dns: invalid DNS probe response")
	}
	return nil
}

func writeAll(connection net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := connection.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func validateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || !validPort(port) {
		return fmt.Errorf("dns: listen address must be loopback with a valid port")
	}
	return nil
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 || value == "." || value == ".." || strings.HasPrefix(value, ".") {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validDomain(value string) bool {
	value = strings.TrimSuffix(strings.TrimPrefix(value, "."), ".")
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalListen(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}
