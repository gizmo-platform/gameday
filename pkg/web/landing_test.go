package web

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/the-maldridge/authware"

	"github.com/gizmo-platform/gameday/pkg/buildinfo"
)

func ipNet(cidr string) net.Addr {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

// fakeIfaces is a host with wifi and ethernet plus every kind of
// address and interface usableURLs must drop.
func fakeIfaces() []ifaceAddr {
	up := net.FlagUp
	return []ifaceAddr{
		{Name: "lo", Flags: up | net.FlagLoopback, Addrs: []net.Addr{ipNet("127.0.0.1/8")}},
		{Name: "wlan0", Flags: up, Addrs: []net.Addr{ipNet("192.168.1.20/24"), ipNet("fe80::1/64"), ipNet("2001:db8::20/64")}},
		{Name: "eth0", Flags: up, Addrs: []net.Addr{ipNet("10.0.0.5/24"), ipNet("169.254.3.4/16")}},
		{Name: "eth1", Flags: 0, Addrs: []net.Addr{ipNet("10.9.9.9/24")}},
		{Name: "docker0", Flags: up, Addrs: []net.Addr{ipNet("172.17.0.1/16")}},
		{Name: "br-1a2b", Flags: up, Addrs: []net.Addr{ipNet("172.18.0.1/16")}},
		{Name: "veth1", Flags: up, Addrs: []net.Addr{ipNet("172.19.0.1/16")}},
		{Name: "virbr0", Flags: up, Addrs: []net.Addr{ipNet("192.168.122.1/24")}},
	}
}

// stubInterfaces substitutes listInterfaceAddrs for the test.
func stubInterfaces(t *testing.T, ifaces []ifaceAddr) {
	t.Helper()
	orig := listInterfaceAddrs
	listInterfaceAddrs = func() ([]ifaceAddr, error) { return ifaces, nil }
	t.Cleanup(func() { listInterfaceAddrs = orig })
}

func TestUsableURLs(t *testing.T) {
	got := usableURLs(fakeIfaces(), "8080")
	want := []string{"http://192.168.1.20:8080/", "http://10.0.0.5:8080/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("usableURLs = %v, want %v", got, want)
	}
}

func TestAdvertisedURLsOverride(t *testing.T) {
	ws, _ := newTestServer(t)
	stubInterfaces(t, fakeIfaces())
	t.Setenv("GAMEDAY_PORT", "9000")

	cases := []struct{ env, want string }{
		{"192.168.50.2", "http://192.168.50.2:9000/"},
		{"192.168.50.2:80", "http://192.168.50.2:80/"},
		{"http://gameday.local:8080/", "http://gameday.local:8080/"},
	}
	for _, c := range cases {
		t.Setenv("GAMEDAY_ADVERTISE_ADDR", c.env)
		got := ws.advertisedURLs()
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("GAMEDAY_ADVERTISE_ADDR=%q: got %v, want [%s]", c.env, got, c.want)
		}
	}
}

// landing renders the landing page for scope, optionally logged in.
func landing(t *testing.T, scope string, loggedIn bool) string {
	t.Helper()
	ws, _ := newTestServer(t)
	ws.scope = scope

	req := httptest.NewRequest("GET", "/ui", nil)
	if loggedIn {
		req = req.WithContext(context.WithValue(req.Context(), authware.UserKey{}, authware.User{Identity: "tester"}))
	}
	w := httptest.NewRecorder()
	ws.uiViewLanding(w, req)
	return w.Body.String()
}

func assertContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

func assertNotContains(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(body, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}

func TestLandingOnsite(t *testing.T) {
	stubInterfaces(t, fakeIfaces())
	t.Setenv("GAMEDAY_PORT", "8080")

	body := landing(t, "onsite", false)
	assertContains(t, body,
		"Welcome to Gameday",
		"You need to log in",
		`href="/login">Login</a>`,
		"<code>http://192.168.1.20:8080/</code>",
		"<code>http://10.0.0.5:8080/</code>",
	)
	assertNotContains(t, body, "172.17.0.1", "scorecards")

	body = landing(t, "onsite", true)
	assertContains(t, body, "Logged in as <strong>tester</strong>")
	assertNotContains(t, body, `href="/login">Login</a>`)
}

func TestLandingOnsiteNoAddress(t *testing.T) {
	stubInterfaces(t, nil)
	assertContains(t, landing(t, "onsite", false), "Set <code>GAMEDAY_ADVERTISE_ADDR</code>")
}

func TestLandingCloud(t *testing.T) {
	stubInterfaces(t, fakeIfaces())
	t.Setenv("GAMEDAY_CLOUD_WELCOME_MSG", "Welcome to <b>State</b>\nGood luck")

	body := landing(t, "cloud", false)
	assertContains(t, body,
		"Welcome to &lt;b&gt;State&lt;/b&gt;<br />Good luck",
		`href="/ui/mod/game/scorecards"`,
	)
	// Escaped exactly once, the line break kept, and no onsite content.
	assertNotContains(t, body, "<b>State", "&amp;lt;", "&lt;br", "You need to log in", "192.168.1.20")

	body = landing(t, "cloud", true)
	assertContains(t, body, "Good luck")
	assertNotContains(t, body, `href="/ui/mod/game/scorecards"`)
}

func TestLandingCloudNoMessage(t *testing.T) {
	t.Setenv("GAMEDAY_CLOUD_WELCOME_MSG", "  ")
	body := landing(t, "cloud", false)
	assertContains(t, body, "Welcome to Gameday", `href="/ui/mod/game/scorecards"`)
}

func TestLandingNoScope(t *testing.T) {
	body := landing(t, "", false)
	assertContains(t, body, "<html", "</html>")
	assertNotContains(t, body, "Welcome to Gameday", "scorecards", `class="box"`, "Error while rendering")
}

// TestFooter verifies every page built on base.p2 carries the docked,
// non-printing footer with the credit line and the build version.
func TestFooter(t *testing.T) {
	orig := buildinfo.Version
	buildinfo.Version = "9.8.7-test"
	t.Cleanup(func() { buildinfo.Version = orig })

	for _, scope := range []string{"", "onsite", "cloud"} {
		body := landing(t, scope, false)
		assertContains(t, body,
			`<body class="has-docked-footer">`,
			`<footer class="footer gameday-footer noprint">`,
			"Gameday by Gizmo Mechatronics &middot; 9.8.7-test",
		)
	}
}

// TestNavbarMobileStructure verifies the navbar follows Bulma's
// collapsible structure: a burger as the last child of navbar-brand
// whose data-target names the navbar-menu wrapping start and end, with
// the toggle script loaded.  Without the menu wrapper and burger every
// item stacks down the page below 1024px.
func TestNavbarMobileStructure(t *testing.T) {
	body := landing(t, "cloud", false)
	assertContains(t, body,
		`<nav class="navbar noprint" role="navigation" aria-label="main navigation">`,
		`<button type="button" class="navbar-burger" aria-label="menu" aria-expanded="false" aria-controls="gameday-navbar-menu" data-target="gameday-navbar-menu">`,
		`<div id="gameday-navbar-menu" class="navbar-menu">`,
		`<script src="/static/js/navbar.js" defer></script>`,
	)
	brand := strings.Index(body, `class="navbar-brand"`)
	burger := strings.Index(body, `class="navbar-burger"`)
	menu := strings.Index(body, `class="navbar-menu"`)
	start := strings.Index(body, `class="navbar-start"`)
	end := strings.Index(body, `class="navbar-end`)
	if !(brand < burger && burger < menu && menu < start && start < end) {
		t.Errorf("navbar order brand=%d burger=%d menu=%d start=%d end=%d", brand, burger, menu, start, end)
	}
}
