package subparse

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

type Node struct {
	Name, Type, Address string
	Port                int
	URI                 string
}

var allowed = map[string]bool{"ss": true, "ssr": true, "vmess": true, "vless": true, "trojan": true, "hysteria": true, "hysteria2": true, "tuic": true}

func Parse(body []byte) ([]Node, error) {
	if len(body) == 0 {
		return nil, errors.New("empty subscription")
	}
	s := strings.TrimSpace(string(body))
	if !strings.Contains(s, "://") {
		var d []byte
		var e error
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			d, e = enc.DecodeString(strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == ' ' {
					return -1
				}
				return r
			}, s))
			if e == nil {
				break
			}
		}
		if e != nil {
			return nil, errors.New("unsupported subscription format")
		}
		s = string(d)
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 10000 {
		return nil, errors.New("too many lines")
	}
	out := make([]Node, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 16384 {
			continue
		}
		u, e := url.Parse(line)
		if e != nil || !allowed[strings.ToLower(u.Scheme)] {
			continue
		}
		host := u.Hostname()
		if host == "" { // vmess/ss opaque payload remains valid interoperable URI
			if u.Opaque == "" && u.Host == "" {
				continue
			}
		}
		port := 0
		if u.Port() != "" {
			for _, c := range u.Port() {
				port = port*10 + int(c-'0')
			}
			if port < 1 || port > 65535 {
				continue
			}
		}
		name, _ := url.PathUnescape(u.Fragment)
		out = append(out, Node{Name: name, Type: strings.ToLower(u.Scheme), Address: host, Port: port, URI: line})
	}
	if len(out) == 0 {
		return nil, errors.New("no supported nodes")
	}
	return out, nil
}
