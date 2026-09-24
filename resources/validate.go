package resources

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strings"

	"github.com/fishman/clashpulse/config"
	"gopkg.in/yaml.v3"
)

// Validate checks the bytes against the resource's declared kind, format, and
// Mihomo rule behavior before they can be promoted into managed state.
func Validate(resource config.Resource, data []byte) error {
	if err := ValidateDeclaration(resource); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("resource %q is empty", resource.ID)
	}
	switch resource.Kind {
	case config.ResourceGeoIP, config.ResourceGeoSite:
		// Mihomo's protobuf geodata files have no standalone framing marker;
		// the kind/format pair and non-empty bounded payload are the checks
		// available without importing Mihomo or guessing at its protobuf schema.
		return nil
	case config.ResourceMMDB:
		if !bytes.Contains(data, []byte("\xab\xcd\xefMaxMind.com")) {
			return fmt.Errorf("resource %q is not a MaxMind database", resource.ID)
		}
		return nil
	case config.ResourceRuleProvider, config.ResourceRuleSet:
		return validateRules(resource, data)
	default:
		return fmt.Errorf("resource %q has unsupported kind %q", resource.ID, resource.Kind)
	}
}

// ValidateDeclaration checks whether a resource's kind, format, and rule behavior are supported.
func ValidateDeclaration(resource config.Resource) error {
	if !validID(resource.ID) {
		return fmt.Errorf("invalid resource ID %q", resource.ID)
	}
	switch resource.Kind {
	case config.ResourceGeoIP, config.ResourceGeoSite:
		if resource.Format != config.FormatDAT || resource.RuleType != "" {
			return fmt.Errorf("resource %q requires kind-specific dat format and no rule type", resource.ID)
		}
	case config.ResourceMMDB:
		if resource.Format != config.FormatMMDB || resource.RuleType != "" {
			return fmt.Errorf("resource %q requires mmdb format and no rule type", resource.ID)
		}
	case config.ResourceRuleProvider, config.ResourceRuleSet:
		if resource.Format != config.FormatYAML && resource.Format != config.FormatText && resource.Format != config.FormatMRS {
			return fmt.Errorf("resource %q has unsupported rule format %q", resource.ID, resource.Format)
		}
		switch resource.RuleType {
		case config.RuleDomain, config.RuleIPCIDR:
		case config.RuleClassical:
			if resource.Format == config.FormatMRS {
				return fmt.Errorf("resource %q: mrs format does not support classical rules", resource.ID)
			}
		default:
			return fmt.Errorf("resource %q has unsupported rule type %q", resource.ID, resource.RuleType)
		}
	default:
		return fmt.Errorf("resource %q has unsupported kind %q", resource.ID, resource.Kind)
	}
	return nil
}

func validateRules(resource config.Resource, data []byte) error {
	switch resource.Format {
	case config.FormatYAML:
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		var doc struct {
			Payload []string `yaml:"payload"`
		}
		if err := decoder.Decode(&doc); err != nil {
			return fmt.Errorf("resource %q: invalid yaml rule provider: %w", resource.ID, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return fmt.Errorf("resource %q: multiple yaml documents are not supported", resource.ID)
			}
			return fmt.Errorf("resource %q: invalid trailing yaml: %w", resource.ID, err)
		}
		if len(doc.Payload) == 0 {
			return fmt.Errorf("resource %q: yaml rule provider requires a non-empty payload", resource.ID)
		}
		for _, line := range doc.Payload {
			if err := validateRuleLine(resource.RuleType, line); err != nil {
				return fmt.Errorf("resource %q: invalid rule: %w", resource.ID, err)
			}
		}
		return nil
	case config.FormatText:
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), len(data)+1)
		count := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if err := validateRuleLine(resource.RuleType, line); err != nil {
				return fmt.Errorf("resource %q: invalid rule on line %d: %w", resource.ID, count+1, err)
			}
			count++
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("resource %q: read text rules: %w", resource.ID, err)
		}
		if count == 0 {
			return fmt.Errorf("resource %q: text rule provider has no rules", resource.ID)
		}
		return nil
	case config.FormatMRS:
		// MRS is a Zstandard stream with an embedded MRS header and behavior.
		// Verify the standard Zstandard frame signature here; Mihomo's selected
		// executable performs the full embedded-header and rule decoding check.
		if len(data) < 4 || !bytes.Equal(data[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			return fmt.Errorf("resource %q: invalid mrs binary", resource.ID)
		}
		return nil
	default:
		return fmt.Errorf("resource %q has unsupported format %q", resource.ID, resource.Format)
	}
}

func validateRuleLine(ruleType config.RuleType, line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return fmt.Errorf("empty or commented rule")
	}
	switch ruleType {
	case config.RuleDomain:
		if strings.HasPrefix(line, "+.") {
			line = line[2:]
		}
		return validateDomain(line)
	case config.RuleIPCIDR:
		if _, err := netip.ParsePrefix(line); err != nil {
			return fmt.Errorf("invalid IP prefix")
		}
		return nil
	case config.RuleClassical:
		return validateClassical(line)
	default:
		return fmt.Errorf("unknown rule behavior")
	}
}

func validateDomain(domain string) error {
	if len(domain) == 0 || len(domain) > 253 {
		return fmt.Errorf("invalid domain")
	}
	for _, label := range strings.Split(strings.TrimSuffix(domain, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid domain")
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return fmt.Errorf("invalid domain")
			}
		}
	}
	return nil
}

func validateClassical(line string) error {
	parts := strings.Split(line, ",")
	kind := strings.ToUpper(strings.TrimSpace(parts[0]))
	if kind == "AND" || kind == "OR" || kind == "NOT" {
		expression := strings.Join(parts[1:], ",")
		if !balancedExpression(expression) {
			return fmt.Errorf("invalid %s expression", kind)
		}
		return nil
	}
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("rule target is required")
	}
	target := strings.TrimSpace(parts[1])
	switch kind {
	case "DOMAIN", "DOMAIN-SUFFIX":
		return validateDomain(target)
	case "DOMAIN-REGEX":
		_, err := regexp.Compile(target)
		return err
	case "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "GEOSITE", "GEOIP", "SRC-GEOIP", "IP-ASN", "SRC-IP-ASN", "IP-SUFFIX", "SRC-IP-SUFFIX", "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX", "PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD", "UID", "IN-TYPE", "IN-USER", "IN-NAME", "REMATCH-NAME":
		return nil
	case "NETWORK":
		if strings.EqualFold(target, "tcp") || strings.EqualFold(target, "udp") {
			return nil
		}
		return fmt.Errorf("invalid network type")
	case "DSCP":
		if newPort(target) > 63 {
			return fmt.Errorf("invalid DSCP value")
		}
		if newPort(target) == 0 {
			return fmt.Errorf("invalid DSCP value")
		}
		return nil
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR":
		if _, err := netip.ParsePrefix(target); err != nil {
			if _, addrErr := netip.ParseAddr(target); addrErr != nil {
				return fmt.Errorf("invalid IP address or prefix")
			}
		}
		return nil
	case "DST-PORT", "SRC-PORT", "IN-PORT":
		_, err := parsePortOrRange(target)
		return err
	default:
		return fmt.Errorf("unsupported classical rule %q", kind)
	}
}

func balancedExpression(value string) bool {
	if len(value) < 2 {
		return false
	}
	depth := 0
	for _, r := range value {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && strings.HasPrefix(strings.TrimSpace(value), "(")
}

func parsePortOrRange(value string) (string, error) {
	if strings.Contains(value, "-") {
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid port range")
		}
		left, right := newPort(parts[0]), newPort(parts[1])
		if left == 0 || right < left {
			return "", fmt.Errorf("invalid port range")
		}
		return value, nil
	}
	if newPort(value) == 0 {
		return "", fmt.Errorf("invalid port")
	}
	return value, nil
}

func newPort(value string) int {
	var n int
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 65535 {
			return 0
		}
	}
	return n
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for i, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}
