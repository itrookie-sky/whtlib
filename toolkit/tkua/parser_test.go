package tkua

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestParseSamples(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ua       string
		os       OS
		platform Platform
		device   DeviceType
		browser  Browser
		engine   Engine
		app      App
		client   ClientType
		arch     Architecture
		bot      BotType
	}{
		{name: "Chrome Windows", ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.130 Safari/537.36", os: OSWindows, platform: PlatformWindows, device: DeviceDesktop, browser: BrowserChrome, engine: EngineBlink, client: ClientBrowser, arch: ArchitectureAMD64},
		{name: "Edge over Chrome", ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.2210.61", os: OSWindows, platform: PlatformWindows, device: DeviceDesktop, browser: BrowserEdge, engine: EngineBlink, client: ClientBrowser, arch: ArchitectureAMD64},
		{name: "legacy Edge", ua: "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/42.0.2311.135 Safari/537.36 Edge/12.246", os: OSWindows, platform: PlatformWindows, device: DeviceDesktop, browser: BrowserEdge, engine: EngineEdgeHTML, client: ClientBrowser},
		{name: "Firefox Ubuntu", ua: "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0", os: OSUbuntu, platform: PlatformLinux, device: DeviceDesktop, browser: BrowserFirefox, engine: EngineGecko, client: ClientBrowser, arch: ArchitectureAMD64},
		{name: "Safari macOS", ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15", os: OSMacOS, platform: PlatformApple, device: DeviceDesktop, browser: BrowserSafari, engine: EngineWebKit, client: ClientBrowser},
		{name: "iPhone", ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1", os: OSIOS, platform: PlatformApple, device: DeviceMobile, browser: BrowserSafari, engine: EngineWebKit, client: ClientBrowser},
		{name: "iPad", ua: "Mozilla/5.0 (iPad; CPU OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1", os: OSIOS, platform: PlatformApple, device: DeviceTablet, browser: BrowserSafari, engine: EngineWebKit, client: ClientBrowser},
		{name: "iOS Chrome WebKit", ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/120.0.6099.119 Mobile/15E148 Safari/604.1", os: OSIOS, platform: PlatformApple, device: DeviceMobile, browser: BrowserChrome, engine: EngineWebKit, client: ClientBrowser},
		{name: "Android phone", ua: "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", os: OSAndroid, platform: PlatformAndroid, device: DeviceMobile, browser: BrowserChrome, engine: EngineBlink, client: ClientBrowser},
		{name: "Android tablet", ua: "Mozilla/5.0 (Linux; Android 12; SM-T870) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", os: OSAndroid, platform: PlatformAndroid, device: DeviceTablet, browser: BrowserChrome, engine: EngineBlink, client: ClientBrowser},
		{name: "ambiguous Android", ua: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", os: OSAndroid, platform: PlatformAndroid, browser: BrowserChrome, engine: EngineBlink, client: ClientBrowser},
		{name: "WeChat webview", ua: "Mozilla/5.0 (Linux; Android 13; Pixel 7; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/120.0.0.0 Mobile Safari/537.36 MicroMessenger/8.0.44", os: OSAndroid, platform: PlatformAndroid, device: DeviceMobile, browser: BrowserChrome, engine: EngineBlink, app: AppWeChat, client: ClientWebView},
		{name: "WeCom over WeChat", ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 MicroMessenger/8.0.44 wxwork/4.1.16", os: OSIOS, platform: PlatformApple, device: DeviceMobile, engine: EngineWebKit, app: AppWeCom, client: ClientApp},
		{name: "Samsung over Chrome", ua: "Mozilla/5.0 (Linux; Android 13; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/23.0 Chrome/115.0.0.0 Mobile Safari/537.36", os: OSAndroid, platform: PlatformAndroid, device: DeviceMobile, browser: BrowserSamsung, engine: EngineBlink, client: ClientBrowser},
		{name: "Googlebot phone disguise", ua: "Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", os: OSAndroid, platform: PlatformAndroid, browser: BrowserChrome, engine: EngineBlink, client: ClientBot, bot: BotSearch},
		{name: "AI bot", ua: "GPTBot/1.2 (+https://openai.com/gptbot)", client: ClientBot, bot: BotAI},
		{name: "social bot", ua: "Twitterbot/1.0", client: ClientBot, bot: BotSocial},
		{name: "monitor", ua: "UptimeRobot/2.0", client: ClientBot, bot: BotMonitor},
		{name: "SEO", ua: "AhrefsBot/7.0", client: ClientBot, bot: BotSEO},
		{name: "archive", ua: "CCBot/2.0", client: ClientBot, bot: BotArchive},
		{name: "curl", ua: "curl/8.5.0", client: ClientHTTP},
		{name: "Go client", ua: "Go-http-client/1.1", client: ClientHTTP},
		{name: "media player", ua: "VLC/3.0.20", client: ClientMediaPlayer},
		{name: "feed reader", ua: "Feedly/1.0", client: ClientFeedReader},
		{name: "validator", ua: "W3C_Validator/1.3", client: ClientValidator},
		{name: "generic robot", ua: "my-custom-robot-client", client: ClientBot, bot: BotOther},
		{name: "unrecognized", ua: "my-custom-client"},
		{name: "empty", ua: ""},
		{name: "whitespace", ua: " \t\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.ua)
			if err != nil {
				t.Fatal(err)
			}
			if r.Raw != tc.ua || r.OS.Family != tc.os || r.Platform != tc.platform || r.Device.Type != tc.device || r.Browser.Family != tc.browser || r.Engine.Family != tc.engine || r.App.Family != tc.app || r.Client.Type != tc.client || r.Architecture != tc.arch || r.Bot.Type != tc.bot {
				t.Fatalf("unexpected result: %+v", r)
			}
			if r.IsBot() != (tc.client == ClientBot) || r.IsMobile() != (tc.device == DeviceMobile) || r.IsTablet() != (tc.device == DeviceTablet) || r.IsDesktop() != (tc.device == DeviceDesktop) || r.IsWebView() != (tc.client == ClientWebView) {
				t.Fatal("分类辅助方法结果不一致")
			}
		})
	}
}

func TestVersionAndSnapshot(t *testing.T) {
	ua := "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.130 Mobile Safari/537.36"
	r, err := Parse(ua)
	if err != nil {
		t.Fatal(err)
	}
	if r.Browser.Version.Raw != "120.0.6099.130" || r.Browser.Version.Major != "120" || r.Browser.Version.Build != "130" || r.OS.Version.Major != "13" {
		t.Fatalf("versions: %+v", r)
	}
	if r.Device.Brand != "Google" || r.Device.Model != "Pixel 7" {
		t.Fatalf("device: %+v", r.Device)
	}
	if r.Engine.Version.Raw != "" {
		t.Fatal("Blink 版本不能借用 Chrome 版本")
	}
	want := r
	r.Device.Model = "changed"
	r.Client.Name = "changed"
	again, err := Parse(ua)
	if err != nil || again != want {
		t.Fatalf("修改结果污染缓存: %+v, %v", again, err)
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	err = json.Unmarshal(encoded, &decoded)
	if err != nil || decoded != want {
		t.Fatalf("JSON round trip: %+v, %v", decoded, err)
	}
}

func TestConfigAndLimits(t *testing.T) {
	for _, config := range []Config{{MaxLength: -1}, {CacheSize: -1}} {
		_, err := New(config)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid config: %v", err)
		}
	}
	p, err := New(Config{MaxLength: 12, CacheSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Parse(strings.Repeat("x", 12))
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse(strings.Repeat("x", 13))
	if !errors.Is(err, ErrTooLong) || r != (Result{}) {
		t.Fatalf("length: %+v %v", r, err)
	}
	_, err = p.Parse(strings.Repeat("中", 5))
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("byte length: %v", err)
	}
	_, err = Parse(strings.Repeat("x", DefaultMaxLength+1))
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("default length: %v", err)
	}
	for _, parser := range []*Parser{nil, {}} {
		_, err = parser.Parse("curl/8.0")
		if !errors.Is(err, ErrNotInitialized) {
			t.Fatalf("uninitialized: %v", err)
		}
	}
}

func TestConcurrentParser(t *testing.T) {
	p, err := New(Config{CacheSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for _, ua := range []string{"curl/8.1", "curl/8.2", "curl/8.3", "curl/8.4"} {
				r, parseErr := p.Parse(ua)
				if parseErr != nil || r.Raw != ua || r.Client.Type != ClientHTTP {
					t.Errorf("concurrent parse: %+v %v", r, parseErr)
				}
			}
		})
	}
	group.Wait()
}
