package tkua

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	productPattern       = regexp.MustCompile(`(?i)(?:^|[\s;(\[])([a-z][a-z0-9_.-]*)/([0-9][a-z0-9._+-]*)`)
	msiePattern          = regexp.MustCompile(`(?i)\bMSIE\s+([0-9.]+)`)
	rvPattern            = regexp.MustCompile(`(?i)\brv:([0-9.]+)`)
	operaMiniPattern     = regexp.MustCompile(`(?i)\bOpera Mini/([0-9.]+)`)
	operaMobilePattern   = regexp.MustCompile(`(?i)\bOpera Mobi(?:le)?/([0-9.]+)`)
	webViewPattern       = regexp.MustCompile(`(?i)(?:;\s*wv\s*\)|\bwebview\b)`)
	spacedProductPattern = regexp.MustCompile(`(?i)\b(OpenHarmony|HarmonyOS|iPadOS|watchOS|tvOS|visionOS|FireOS|Instagram|MetaSr)\s+([0-9][a-z0-9._+-]*)`)
	jdPattern            = regexp.MustCompile(`(?i)\bjdapp;[^;]+;([0-9.]+);`)
)

func parseProducts(raw string) (products map[string]string) {
	products = make(map[string]string)
	for _, match := range productPattern.FindAllStringSubmatch(raw, -1) {
		key := strings.ToLower(match[1])
		// 同名产品取首次声明，避免被后续兼容标记覆盖。
		if _, exists := products[key]; !exists {
			products[key] = match[2]
		}
	}
	for _, match := range spacedProductPattern.FindAllStringSubmatch(raw, -1) {
		key := strings.ToLower(match[1])
		if _, exists := products[key]; !exists {
			products[key] = match[2]
		}
	}
	if match := jdPattern.FindStringSubmatch(raw); len(match) > 1 {
		products["jdapp"] = match[1]
	}
	for key, pattern := range map[string]*regexp.Regexp{"msie": msiePattern, "rv": rvPattern, "opera mini": operaMiniPattern, "opera mobi": operaMobilePattern} {
		match := pattern.FindStringSubmatch(raw)
		if len(match) > 1 {
			products[key] = match[1]
		}
	}
	return
}

type browserRule struct {
	token  string
	family Browser
}

// 浏览器外壳优先于 Chromium/Chrome/Safari 兼容标记。
var browserRules = []browserRule{
	{token: "edgios", family: BrowserEdge}, {token: "edga", family: BrowserEdge}, {token: "edg", family: BrowserEdge}, {token: "edge", family: BrowserEdge},
	{token: "opera mini", family: BrowserOperaMini}, {token: "opera mobi", family: BrowserOperaMobile}, {token: "opios", family: BrowserOpera}, {token: "opr", family: BrowserOpera}, {token: "opera", family: BrowserOpera},
	{token: "samsungbrowser", family: BrowserSamsung}, {token: "ucbrowser", family: BrowserUC}, {token: "ucweb", family: BrowserUC},
	{token: "mqqbrowser", family: BrowserQQ}, {token: "qqbrowser", family: BrowserQQ}, {token: "baidubrowser", family: BrowserBaidu}, {token: "bidubrowser", family: BrowserBaidu},
	{token: "metasr", family: BrowserSogou}, {token: "sogoumobilebrowser", family: BrowserSogou}, {token: "maxthon", family: BrowserMaxthon},
	{token: "vivaldi", family: BrowserVivaldi}, {token: "yabrowser", family: BrowserYandex}, {token: "brave", family: BrowserBrave},
	{token: "huaweibrowser", family: BrowserHuawei}, {token: "miuibrowser", family: BrowserMiui}, {token: "quark", family: BrowserQuark},
	{token: "whale", family: BrowserWhale}, {token: "puffin", family: BrowserPuffin}, {token: "silk", family: BrowserSilk}, {token: "duckduckgo", family: BrowserDuckDuckGo},
	{token: "electron", family: BrowserElectron}, {token: "headlesschrome", family: BrowserHeadlessChrome},
	{token: "fxios", family: BrowserFirefox}, {token: "firefox", family: BrowserFirefox}, {token: "crios", family: BrowserChrome},
	{token: "chromium", family: BrowserChromium}, {token: "chrome", family: BrowserChrome}, {token: "msie", family: BrowserIE},
}

func browserFor(products map[string]string, os OS) (browser BrowserInfo) {
	for _, rule := range browserRules {
		if version, found := products[rule.token]; found {
			if rule.token == "opera" && products["version"] != "" {
				version = products["version"]
			}
			return BrowserInfo{Family: rule.family, Version: parseVersion(version)}
		}
	}
	if products["trident"] != "" {
		return BrowserInfo{Family: BrowserIE, Version: parseVersion(products["rv"])}
	}
	if products["safari"] != "" && products["version"] != "" {
		family := BrowserSafari
		if os == OSAndroid {
			family = BrowserAndroid
		}
		return BrowserInfo{Family: family, Version: parseVersion(products["version"])}
	}
	return
}

type appRule struct {
	token  string
	family App
}

var appRules = []appRule{
	{token: "wxwork", family: AppWeCom}, {token: "micromessenger", family: AppWeChat}, {token: "dingtalk", family: AppDingTalk},
	{token: "alipayclient", family: AppAlipay}, {token: "weibo", family: AppWeibo}, {token: "qq", family: AppQQ},
	{token: "aweme", family: AppDouyin}, {token: "tiktok", family: AppTikTok}, {token: "trill", family: AppTikTok},
	{token: "newsarticle", family: AppToutiao}, {token: "baiduboxapp", family: AppBaidu},
	{token: "jdapp", family: AppJD}, {token: "instagram", family: AppInstagram}, {token: "fbav", family: AppFacebook},
	{token: "line", family: AppLine}, {token: "feishu", family: AppFeishu}, {token: "lark", family: AppLark}, {token: "electron", family: AppElectron},
}

var taobaoPattern = regexp.MustCompile(`(?i)\bAliApp\(TB/([0-9.]+)\)`)

func appFor(raw string, products map[string]string) (app AppInfo) {
	match := taobaoPattern.FindStringSubmatch(raw)
	if len(match) > 1 {
		return AppInfo{Family: AppTaobao, Version: parseVersion(match[1])}
	}
	for _, rule := range appRules {
		if version, found := products[rule.token]; found && rule.family != AppUnknown {
			return AppInfo{Family: rule.family, Version: parseVersion(version)}
		}
	}
	return
}

func engineFor(products map[string]string, os OS) (engine EngineInfo) {
	for _, rule := range []struct {
		token  string
		family Engine
	}{
		{token: "trident", family: EngineTrident}, {token: "edge", family: EngineEdgeHTML},
		{token: "presto", family: EnginePresto}, {token: "goanna", family: EngineGoanna},
	} {
		if version, found := products[rule.token]; found {
			return EngineInfo{Family: rule.family, Version: parseVersion(version)}
		}
	}
	if version, found := products["applewebkit"]; found {
		// iOS 的 Chrome/Edge 标记不代表 Blink；Chromium 28 之前仍为 WebKit。
		chrome := products["chrome"]
		if chrome == "" {
			chrome = products["chromium"]
		}
		if chrome == "" {
			chrome = products["headlesschrome"]
		}
		major, err := strconv.Atoi(parseVersion(chrome).Major)
		if err == nil && major >= 28 && os != OSIOS && os != OSIPadOS {
			return EngineInfo{Family: EngineBlink}
		}
		return EngineInfo{Family: EngineWebKit, Version: parseVersion(version)}
	}
	if products["gecko"] != "" {
		// Gecko/20100101 是构建日期；实际引擎版本来自 rv。
		return EngineInfo{Family: EngineGecko, Version: parseVersion(products["rv"])}
	}
	if version, found := products["khtml"]; found {
		return EngineInfo{Family: EngineKHTML, Version: parseVersion(version)}
	}
	return
}

func clientTypeFor(raw string, products map[string]string, result Result) (kind ClientType) {
	if result.Bot.Type != BotUnknown {
		return ClientBot
	}
	for _, token := range []string{"curl", "wget", "python-requests", "python-urllib", "go-http-client", "okhttp", "java", "apache-httpclient", "postmanruntime", "httpie", "libwww-perl", "axios", "node-fetch"} {
		if products[token] != "" {
			return ClientHTTP
		}
	}
	for _, token := range []string{"vlc", "libvlc", "mpv", "lavf", "windows-media-player", "itunes"} {
		if products[token] != "" {
			return ClientMediaPlayer
		}
	}
	for _, token := range []string{"feedly", "feedparser", "reeder", "newsblur"} {
		if products[token] != "" {
			return ClientFeedReader
		}
	}
	for _, token := range []string{"w3c_validator", "w3c-checklink"} {
		if products[token] != "" {
			return ClientValidator
		}
	}
	if webViewPattern.MatchString(raw) || (result.OS.Family == OSAndroid && products["chrome"] != "" && products["version"] == "4.0") {
		return ClientWebView
	}
	if result.App.Family != AppUnknown {
		return ClientApp
	}
	if result.Browser.Family != BrowserUnknown {
		return ClientBrowser
	}
	return
}

var osFamilies = map[string]OS{
	"windows": OSWindows, "windows phone": OSWindowsPhone, "windows mobile": OSWindowsPhone, "windows ce": OSWindowsCE,
	"mac os x": OSMacOS, "mac os": OSMacOS, "macos": OSMacOS, "ios": OSIOS, "ipados": OSIPadOS,
	"watchos": OSWatchOS, "tvos": OSTvOS, "visionos": OSVisionOS, "android": OSAndroid, "chrome os": OSChromeOS,
	"harmonyos": OSHarmonyOS, "openharmony": OSOpenHarmony, "linux": OSLinux, "ubuntu": OSUbuntu,
	"debian": OSDebian, "fedora": OSFedora, "centos": OSCentOS, "freebsd": OSFreeBSD, "openbsd": OSOpenBSD,
	"netbsd": OSNetBSD, "solaris": OSSolaris, "blackberry os": OSBlackBerry, "blackberry tablet os": OSBlackBerry,
	"kaios": OSKaiOS, "firefox os": OSFirefoxOS, "tizen": OSTizen, "webos": OSWebOS,
	"symbian os": OSSymbian, "symbian^3": OSSymbian, "bada": OSBada, "roku": OSRoku, "fire os": OSFireOS,
}

func classifyOS(name string) (family OS) {
	if name == "" {
		return
	}
	normalized := strings.ToLower(name)
	if family, found := osFamilies[normalized]; found {
		return family
	}
	if strings.HasPrefix(normalized, "windows") {
		return OSWindows
	}
	if strings.HasPrefix(normalized, "playstation") {
		return OSPlayStation
	}
	return OSOther
}

// 显式新系统标记优先于 UA 中的 Android/Linux 兼容标记。
func applyExplicitOS(os *OSInfo, products map[string]string) {
	for _, rule := range []struct {
		token  string
		family OS
		name   string
	}{
		{token: "openharmony", family: OSOpenHarmony, name: "OpenHarmony"},
		{token: "harmonyos", family: OSHarmonyOS, name: "HarmonyOS"},
		{token: "ipados", family: OSIPadOS, name: "iPadOS"},
		{token: "watchos", family: OSWatchOS, name: "watchOS"},
		{token: "tvos", family: OSTvOS, name: "tvOS"},
		{token: "visionos", family: OSVisionOS, name: "visionOS"},
		{token: "fireos", family: OSFireOS, name: "Fire OS"},
	} {
		if version, found := products[rule.token]; found {
			*os = OSInfo{Family: rule.family, Name: rule.name, Version: parseVersion(version)}
			return
		}
	}
}

func platformFor(os OS) (platform Platform) {
	switch os {
	case OSUnknown:
		return PlatformUnknown
	case OSWindows, OSWindowsPhone, OSWindowsCE:
		return PlatformWindows
	case OSMacOS, OSIOS, OSIPadOS, OSWatchOS, OSTvOS, OSVisionOS:
		return PlatformApple
	case OSAndroid, OSFireOS:
		return PlatformAndroid
	case OSChromeOS:
		return PlatformChromeOS
	case OSHarmonyOS, OSOpenHarmony:
		return PlatformHarmony
	case OSLinux, OSUbuntu, OSDebian, OSFedora, OSCentOS:
		return PlatformLinux
	case OSFreeBSD, OSOpenBSD, OSNetBSD, OSSolaris:
		return PlatformUnix
	case OSBlackBerry:
		return PlatformBlackBerry
	case OSKaiOS, OSFirefoxOS:
		return PlatformKaiOS
	case OSTizen:
		return PlatformTizen
	case OSWebOS:
		return PlatformWebOS
	case OSPlayStation:
		return PlatformConsole
	default:
		return PlatformOther
	}
}

type architectureRule struct {
	pattern      *regexp.Regexp
	architecture Architecture
}

var architectureRules = []architectureRule{
	{pattern: regexp.MustCompile(`(?i)\b(?:aarch64|arm64)\b`), architecture: ArchitectureARM64},
	{pattern: regexp.MustCompile(`(?i)\b(?:x86_64|amd64|x64)\b`), architecture: ArchitectureAMD64},
	{pattern: regexp.MustCompile(`(?i)\bia64\b`), architecture: ArchitectureIA64},
	{pattern: regexp.MustCompile(`(?i)\b(?:i[3-6]86|x86)\b`), architecture: ArchitectureX86},
	{pattern: regexp.MustCompile(`(?i)\b(?:armv[5-8][a-z]*|arm)\b`), architecture: ArchitectureARM},
	{pattern: regexp.MustCompile(`(?i)\b(?:ppc64|powerpc64)\b`), architecture: ArchitecturePPC64},
	{pattern: regexp.MustCompile(`(?i)\b(?:ppc|powerpc)\b`), architecture: ArchitecturePPC},
	{pattern: regexp.MustCompile(`(?i)\bmips64\b`), architecture: ArchitectureMIPS64},
	{pattern: regexp.MustCompile(`(?i)\bmips\b`), architecture: ArchitectureMIPS},
	{pattern: regexp.MustCompile(`(?i)\briscv64\b`), architecture: ArchitectureRISCV64},
	{pattern: regexp.MustCompile(`(?i)\bsparc\b`), architecture: ArchitectureSPARC},
}

func architectureFor(raw string) (architecture Architecture) {
	for _, rule := range architectureRules {
		if rule.pattern.MatchString(raw) {
			return rule.architecture
		}
	}
	return
}

type deviceRule struct {
	pattern *regexp.Regexp
	kind    DeviceType
}

var deviceRules = []deviceRule{
	{pattern: regexp.MustCompile(`(?i)\b(?:oculus|quest|pico|visionos|vision pro)\b`), kind: DeviceVR},
	{pattern: regexp.MustCompile(`(?i)\b(?:tesla|android automotive|carbrowser)\b`), kind: DeviceCar},
	{pattern: regexp.MustCompile(`(?i)\b(?:playstation|xbox|nintendo)\b`), kind: DeviceConsole},
	{pattern: regexp.MustCompile(`(?i)\b(?:smart[- ]?tv|hbbtv|netcast|googletv|android tv|appletv|apple tv|tvos|roku|aft[a-z0-9]+)\b`), kind: DeviceTV},
	{pattern: regexp.MustCompile(`(?i)\b(?:watchos|apple watch|galaxy watch|wear os|sm-r[0-9]+)\b`), kind: DeviceWearable},
	{pattern: regexp.MustCompile(`(?i)\bKindle Fire\b`), kind: DeviceTablet},
	{pattern: regexp.MustCompile(`(?i)\b(?:kobo|kindle)\b`), kind: DeviceEReader},
	{pattern: regexp.MustCompile(`(?i)\b(?:ipad|tablet|playbook|nexus (?:7|9|10)|sm-t[a-z0-9]+|kf[a-z0-9]+)\b`), kind: DeviceTablet},
	{pattern: regexp.MustCompile(`(?i)\b(?:iphone|ipod|windows phone|blackberry|bb10|mobile)\b`), kind: DeviceMobile},
	{pattern: regexp.MustCompile(`(?i)\b(?:esp32|esp8266|arduino)\b`), kind: DeviceEmbedded},
}

func deviceTypeFor(raw string, result Result) (kind DeviceType) {
	// 机器人和程序库可能伪装桌面/手机，不据此推断其硬件形态。
	if result.Client.Type == ClientBot || result.Client.Type == ClientHTTP {
		return
	}
	// 桌面模式的 iPad 可声明 macOS，单靠 Mobile 无法确定实际终端。
	if result.OS.Family == OSMacOS && regexpMobile.MatchString(raw) {
		return
	}
	for _, rule := range deviceRules {
		if rule.pattern.MatchString(raw) {
			return rule.kind
		}
	}
	switch result.OS.Family {
	case OSIPadOS:
		return DeviceTablet
	case OSWatchOS:
		return DeviceWearable
	case OSTvOS, OSRoku:
		return DeviceTV
	case OSVisionOS:
		return DeviceVR
	case OSWindowsPhone, OSSymbian, OSBada, OSKaiOS:
		return DeviceMobile
	case OSWindows, OSMacOS, OSChromeOS, OSUbuntu, OSDebian, OSFedora, OSCentOS, OSFreeBSD, OSOpenBSD, OSNetBSD, OSSolaris:
		return DeviceDesktop
	}
	// 未携带 Mobile 的 Android 也可能是其他设备，不能直接归为平板。
	if result.OS.Family == OSLinux && strings.Contains(raw, "X11") {
		return DeviceDesktop
	}
	return
}

var regexpMobile = regexp.MustCompile(`(?i)\bMobile\b`)

type botRule struct {
	pattern *regexp.Regexp
	kind    BotType
}

var botRules = []botRule{
	{pattern: regexp.MustCompile(`(?i)\b(GPTBot|ChatGPT-User|OAI-SearchBot|ClaudeBot|Claude-Web|anthropic-ai|PerplexityBot|Perplexity-User|Bytespider|Google-Extended)\b`), kind: BotAI},
	{pattern: regexp.MustCompile(`(?i)\b(Googlebot(?:-[a-z]+)?|AdsBot-Google(?:-Mobile)?|Mediapartners-Google|bingbot|BingPreview|DuckDuckBot|Baiduspider(?:-[a-z]+)?|YandexBot|YandexImages|Applebot|PetalBot|Sogou (?:web )?spider|Slurp)\b`), kind: BotSearch},
	{pattern: regexp.MustCompile(`(?i)\b(facebookexternalhit|Facebot|Twitterbot|Slackbot(?:-LinkExpanding)?|Discordbot|LinkedInBot|TelegramBot)\b`), kind: BotSocial},
	{pattern: regexp.MustCompile(`(?i)\b(UptimeRobot|Pingdom|StatusCake|Site24x7|DatadogSynthetics)\b`), kind: BotMonitor},
	{pattern: regexp.MustCompile(`(?i)\b(AhrefsBot|SemrushBot|MJ12bot|DotBot|Screaming Frog SEO Spider)\b`), kind: BotSEO},
	{pattern: regexp.MustCompile(`(?i)\b(ia_archiver|archive.org_bot|CCBot)\b`), kind: BotArchive},
}

func botFor(raw, deviceFamily string) (bot BotInfo) {
	for _, rule := range botRules {
		match := rule.pattern.FindStringSubmatch(raw)
		if len(match) > 1 {
			return BotInfo{Name: match[1], Type: rule.kind}
		}
	}
	if deviceFamily == "Spider" {
		return BotInfo{Type: BotOther}
	}
	return
}
