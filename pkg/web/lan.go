package web

import (
	"net"
	"os"
	"strings"
)

// ifaceAddr is one network interface and its addresses, the input to
// usableURLs.
type ifaceAddr struct {
	Name  string
	Flags net.Flags
	Addrs []net.Addr
}

// listInterfaceAddrs enumerates the host's interfaces.  It is a
// variable so tests can substitute fake interfaces.
var listInterfaceAddrs = func() ([]ifaceAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]ifaceAddr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		out = append(out, ifaceAddr{Name: iface.Name, Flags: iface.Flags, Addrs: addrs})
	}
	return out, nil
}

// virtualIfacePrefixes are interface name prefixes for container and
// VM bridges, whose addresses other computers on the event network
// cannot reach.
var virtualIfacePrefixes = []string{"docker", "br-", "veth", "virbr"}

// usableURLs returns http://<ip>:<port>/ for every IPv4 address other
// computers can plausibly reach: interfaces that are up and not
// virtual bridges, excluding loopback and link-local addresses.  IPv6
// is left out because people have to type these addresses.
func usableURLs(ifaces []ifaceAddr, port string) []string {
	urls := []string{}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		virtual := false
		for _, prefix := range virtualIfacePrefixes {
			if strings.HasPrefix(iface.Name, prefix) {
				virtual = true
				break
			}
		}
		if virtual {
			continue
		}
		for _, addr := range iface.Addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			urls = append(urls, "http://"+net.JoinHostPort(ip.String(), port)+"/")
		}
	}
	return urls
}

// listenPort returns the port the server is serving on: from the
// address passed to Serve, falling back to GAMEDAY_PORT and then 8080
// before Serve has run.
func (s *Server) listenPort() string {
	if s.n != nil && s.n.Addr != "" {
		if _, port, err := net.SplitHostPort(s.n.Addr); err == nil && port != "" {
			return port
		}
	}
	if p := os.Getenv("GAMEDAY_PORT"); p != "" {
		return p
	}
	return "8080"
}

// advertisedURLs returns the URLs other computers can use to reach
// this server.  GAMEDAY_ADVERTISE_ADDR, when set, is authoritative (the
// only correct answer behind Docker/NAT or a proxy) and gets the
// listen port appended if it has none; otherwise the host's usable
// interface addresses are listed.  An empty result means the address
// could not be determined.
func (s *Server) advertisedURLs() []string {
	port := s.listenPort()
	if adv := strings.TrimSpace(os.Getenv("GAMEDAY_ADVERTISE_ADDR")); adv != "" {
		adv = strings.TrimSuffix(strings.TrimPrefix(adv, "http://"), "/")
		if _, _, err := net.SplitHostPort(adv); err != nil {
			adv = net.JoinHostPort(adv, port)
		}
		return []string{"http://" + adv + "/"}
	}
	ifaces, err := listInterfaceAddrs()
	if err != nil {
		return nil
	}
	return usableURLs(ifaces, port)
}
