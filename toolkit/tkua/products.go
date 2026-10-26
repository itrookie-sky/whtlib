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
