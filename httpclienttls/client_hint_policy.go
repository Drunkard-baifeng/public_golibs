package httpclienttls

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/dunglas/httpsfv"
)

// ClientHintContext is an immutable, standalone document policy snapshot.
// Pass it for subresource requests made under that document. No DOM, iframe
// inheritance, HTML meta policy, report delivery or JS engine is simulated.
type ClientHintContext struct {
	origin string
	rules  map[string][]hintAllowedOrigin
	hints  map[string]bool
}

type hintAllowedOrigin struct {
	origin   string
	all      bool
	wildcard bool
}

// NewClientHintContext snapshots Accept-CH and Permissions-Policy from a
// document's HTTPS response. Unknown features are ignored; malformed managed
// fields return an error. It retains no references to the caller's header map.
// An absent Accept-CH means no high-entropy hints in this standalone snapshot;
// it does not inherit preferences from previous documents or destination caches.
// Without an explicit rule, high-entropy hints are same-origin only and the
// three low-entropy UA hints use their unrestricted default allowlist.
func NewClientHintContext(documentURL string, headers http.Header) (*ClientHintContext, error) {
	origin := canonicalOrigin(documentURL)
	if !strings.HasPrefix(origin, "https://") {
		return nil, fmt.Errorf("Client Hints document context requires a valid HTTPS origin")
	}
	values := headers.Values("Permissions-Policy")
	size := 0
	for _, value := range values {
		size += len(value) + 2
		if size > 8192 {
			return nil, fmt.Errorf("Permissions-Policy exceeds 8 KiB")
		}
	}
	dictionary, err := httpsfv.UnmarshalDictionary(values)
	if err != nil {
		return nil, fmt.Errorf("invalid Permissions-Policy structured field")
	}
	hints, valid := hintTokenList(headers.Values("Accept-CH"))
	if !valid {
		return nil, fmt.Errorf("invalid Accept-CH structured field")
	}
	result := &ClientHintContext{origin: origin, rules: make(map[string][]hintAllowedOrigin), hints: hints}
	for _, header := range clientHintHeaderNames {
		feature := strings.TrimPrefix(strings.ToLower(header), "sec-")
		member, ok := dictionary.Get(feature)
		if !ok {
			continue
		}
		var items []httpsfv.Item
		switch value := member.(type) {
		case httpsfv.Item:
			if token, ok := value.Value.(httpsfv.Token); !ok || (token != "*" && token != "self") {
				return nil, fmt.Errorf("Permissions-Policy origin grants require an inner list for %s", feature)
			}
			items = []httpsfv.Item{value}
		case httpsfv.InnerList:
			items = value.Items
		default:
			return nil, fmt.Errorf("invalid Permissions-Policy directive %s", feature)
		}
		patterns := make([]hintAllowedOrigin, 0, len(items))
		for _, item := range items {
			switch value := item.Value.(type) {
			case httpsfv.Token:
				switch value {
				case "*":
					patterns = append(patterns, hintAllowedOrigin{all: true})
				case "self":
					patterns = append(patterns, hintAllowedOrigin{origin: origin})
				default:
					return nil, fmt.Errorf("invalid Permissions-Policy token for %s", feature)
				}
			case string:
				pattern, err := parseHintAllowedOrigin(value)
				if err != nil {
					return nil, fmt.Errorf("invalid Permissions-Policy origin for %s", feature)
				}
				patterns = append(patterns, pattern)
			default:
				return nil, fmt.Errorf("invalid Permissions-Policy value for %s", feature)
			}
		}
		result.rules[header] = patterns
	}
	return result, nil
}

func parseHintAllowedOrigin(raw string) (hintAllowedOrigin, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return hintAllowedOrigin{}, fmt.Errorf("invalid origin")
	}
	wildcard := strings.HasPrefix(u.Hostname(), "*.")
	if wildcard {
		host := strings.TrimPrefix(u.Hostname(), "*.")
		if host == "" || net.ParseIP(host) != nil {
			return hintAllowedOrigin{}, fmt.Errorf("invalid wildcard origin")
		}
		if port := u.Port(); port != "" {
			u.Host = net.JoinHostPort(host, port)
		} else {
			u.Host = host
		}
	}
	origin := canonicalOrigin(u.String())
	if !strings.HasPrefix(origin, "https://") {
		return hintAllowedOrigin{}, fmt.Errorf("invalid secure origin")
	}
	return hintAllowedOrigin{origin: origin, wildcard: wildcard}, nil
}

func (p *ClientHintContext) allows(header, target string) bool {
	if p == nil {
		return true
	}
	origin := canonicalOrigin(target)
	if p.origin == "" || origin == "" {
		return false
	}
	patterns, exists := p.rules[header]
	if !exists {
		for _, low := range clientHintHeaderNames[:3] {
			if header == low {
				return true
			}
		}
		return p.origin == origin
	}
	for _, pattern := range patterns {
		if pattern.all {
			return true
		}
		if !pattern.wildcard && pattern.origin == origin {
			return true
		}
		if pattern.wildcard {
			base, _ := url.Parse(pattern.origin)
			destination, _ := url.Parse(origin)
			if base.Scheme == destination.Scheme && base.Port() == destination.Port() && strings.HasSuffix(destination.Hostname(), "."+base.Hostname()) {
				return true
			}
		}
	}
	return false
}
