package tkua

import "testing"

func TestArchitectureSamples(t *testing.T) {
	for _, tc := range []struct {
		ua   string
		arch Architecture
	}{
		{ua: "Mozilla/5.0 (Windows NT 6.1; Win64; IA64)", arch: ArchitectureIA64},
		{ua: "Mozilla/5.0 (Windows NT 10.0; Win64)"},
		{ua: "Mozilla/5.0 (Windows NT 10.0; WOW64)"},
		{ua: "Mozilla/5.0 (X11; Linux i686)", arch: ArchitectureX86},
		{ua: "Mozilla/5.0 (X11; Linux armv7l)", arch: ArchitectureARM},
		{ua: "Mozilla/5.0 (X11; Linux aarch64)", arch: ArchitectureARM64},
		{ua: "Mozilla/5.0 (X11; Linux ppc)", arch: ArchitecturePPC},
		{ua: "Mozilla/5.0 (X11; Linux ppc64)", arch: ArchitecturePPC64},
		{ua: "Mozilla/5.0 (X11; Linux mips)", arch: ArchitectureMIPS},
		{ua: "Mozilla/5.0 (X11; Linux mips64)", arch: ArchitectureMIPS64},
		{ua: "Mozilla/5.0 (X11; Linux riscv64)", arch: ArchitectureRISCV64},
		{ua: "Mozilla/5.0 (X11; SunOS sun4u sparc)", arch: ArchitectureSPARC},
	} {
		r, err := Parse(tc.ua)
		if err != nil || r.Architecture != tc.arch {
			t.Errorf("%q: %q, %v", tc.ua, r.Architecture, err)
		}
	}
}

func TestAppSamples(t *testing.T) {
	for _, tc := range []struct {
		ua      string
		app     App
		version string
	}{
		{ua: "MicroMessenger/8.0.44", app: AppWeChat, version: "8.0.44"},
		{ua: "MicroMessenger/8.0.44 wxwork/4.1.16", app: AppWeCom, version: "4.1.16"},
		{ua: "AliApp(DingTalk/7.5.0)", app: AppDingTalk, version: "7.5.0"},
		{ua: "AlipayClient/10.5.0", app: AppAlipay, version: "10.5.0"},
		{ua: "Weibo/13.12.0", app: AppWeibo, version: "13.12.0"},
		{ua: "QQ/9.0.0", app: AppQQ, version: "9.0.0"},
		{ua: "aweme/28.0.0", app: AppDouyin, version: "28.0.0"},
		{ua: "trill/32.0.0", app: AppTikTok, version: "32.0.0"},
		{ua: "NewsArticle/9.5.0", app: AppToutiao, version: "9.5.0"},
		{ua: "baiduboxapp/13.50.0", app: AppBaidu, version: "13.50.0"},
		{ua: "AliApp(TB/10.30.0)", app: AppTaobao, version: "10.30.0"},
		{ua: "jdapp;iPhone;12.3.0;17.0;", app: AppJD, version: "12.3.0"},
		{ua: "[FBAN/FBIOS;FBAV/445.0.0.0.0;]", app: AppFacebook, version: "445.0.0.0.0"},
		{ua: "Instagram 305.0.0.34.110 Android", app: AppInstagram, version: "305.0.0.34.110"},
		{ua: "Line/13.20.0", app: AppLine, version: "13.20.0"},
		{ua: "Feishu/7.0.0", app: AppFeishu, version: "7.0.0"},
		{ua: "Lark/7.0.0", app: AppLark, version: "7.0.0"},
		{ua: "Electron/28.0.0", app: AppElectron, version: "28.0.0"},
	} {
		r, err := Parse(tc.ua)
		if err != nil || r.App.Family != tc.app || r.App.Version.Raw != tc.version {
			t.Errorf("%q: %+v, %v", tc.ua, r.App, err)
		}
	}
}

func TestDeviceSamples(t *testing.T) {
	for _, tc := range []struct {
		ua     string
		device DeviceType
	}{
		{ua: "Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.0)", device: DeviceTV},
		{ua: "Mozilla/5.0 (PlayStation 4 11.00)", device: DeviceConsole},
		{ua: "Mozilla/5.0 (Xbox; Xbox One)", device: DeviceConsole},
		{ua: "Mozilla/5.0 (Nintendo Switch)", device: DeviceConsole},
		{ua: "watchOS/10.2", device: DeviceWearable},
		{ua: "Mozilla/5.0 (Kindle/3.0)", device: DeviceEReader},
		{ua: "Mozilla/5.0 (Linux; Android 4.0; Kindle Fire)", device: DeviceTablet},
		{ua: "Mozilla/5.0 (Linux; Android 10; Quest) OculusBrowser/26.0", device: DeviceVR},
		{ua: "visionOS/1.0", device: DeviceVR},
		{ua: "Mozilla/5.0 (X11; Linux x86_64) Tesla/2023.44", device: DeviceCar},
		{ua: "ESP32/1.0", device: DeviceEmbedded},
		{ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1", device: DeviceUnknown},
	} {
		r, err := Parse(tc.ua)
		if err != nil || r.Device.Type != tc.device {
			t.Errorf("%q: %+v, %v", tc.ua, r, err)
		}
	}
}

func TestOSAndArchitectureSamples(t *testing.T) {
	for _, tc := range []struct {
		ua       string
		os       OS
		platform Platform
		arch     Architecture
	}{
		{ua: "Mozilla/5.0 (Linux; Android 12; Mobile) OpenHarmony 5.0", os: OSOpenHarmony, platform: PlatformHarmony},
		{ua: "Mozilla/5.0 (Linux; Android 12; Mobile) HarmonyOS/4.0", os: OSHarmonyOS, platform: PlatformHarmony},
		{ua: "iPadOS/17.0", os: OSIPadOS, platform: PlatformApple},
		{ua: "watchOS/10.0", os: OSWatchOS, platform: PlatformApple},
		{ua: "tvOS/17.0", os: OSTvOS, platform: PlatformApple},
		{ua: "visionOS/1.0", os: OSVisionOS, platform: PlatformApple},
		{ua: "FireOS/8.0", os: OSFireOS, platform: PlatformAndroid},
		{ua: "Mozilla/5.0 (X11; CrOS aarch64 14541.0.0) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36", os: OSChromeOS, platform: PlatformChromeOS, arch: ArchitectureARM64},
		{ua: "Mozilla/5.0 (X11; FreeBSD amd64; rv:121.0) Gecko/20100101 Firefox/121.0", os: OSFreeBSD, platform: PlatformUnix, arch: ArchitectureAMD64},
	} {
		r, err := Parse(tc.ua)
		if err != nil || r.OS.Family != tc.os || r.Platform != tc.platform || r.Architecture != tc.arch {
			t.Errorf("%q: %+v, %v", tc.ua, r, err)
		}
	}
}

func TestBrowserAndEnginePriority(t *testing.T) {
	for _, tc := range []struct {
		ua      string
		browser Browser
		engine  Engine
		version string
	}{
		{ua: "Mozilla/5.0 (Windows NT 6.1; Trident/7.0; rv:11.0) like Gecko", browser: BrowserIE, engine: EngineTrident, version: "11.0"},
		{ua: "Opera/9.80 (Windows NT 6.1) Presto/2.12.388 Version/12.18", browser: BrowserOpera, engine: EnginePresto, version: "12.18"},
		{ua: "Opera/9.80 (Android; Opera Mini/36.2) Presto/2.12.423 Version/12.16", browser: BrowserOperaMini, engine: EnginePresto, version: "36.2"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 OPR/106.0", browser: BrowserOpera, engine: EngineBlink, version: "106.0"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 UCBrowser/13.0", browser: BrowserUC, engine: EngineBlink, version: "13.0"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 MQQBrowser/14.0", browser: BrowserQQ, engine: EngineBlink, version: "14.0"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 HuaweiBrowser/14.0", browser: BrowserHuawei, engine: EngineBlink, version: "14.0"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 YaBrowser/23.0", browser: BrowserYandex, engine: EngineBlink, version: "23.0"},
		{ua: "AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 Vivaldi/6.0", browser: BrowserVivaldi, engine: EngineBlink, version: "6.0"},
		{ua: "AppleWebKit/537.36 HeadlessChrome/120.0.0.0 Safari/537.36", browser: BrowserHeadlessChrome, engine: EngineBlink, version: "120.0.0.0"},
		{ua: "AppleWebKit/537.22 Chrome/25.0.1364.172 Safari/537.22", browser: BrowserChrome, engine: EngineWebKit, version: "25.0.1364.172"},
	} {
		r, err := Parse(tc.ua)
		if err != nil || r.Browser.Family != tc.browser || r.Engine.Family != tc.engine || r.Browser.Version.Raw != tc.version {
			t.Errorf("%q: %+v, %v", tc.ua, r, err)
		}
	}
}
