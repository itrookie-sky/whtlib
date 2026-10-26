package tkua

const (
	// DefaultMaxLength 是默认允许的 UA 最大字节数。
	DefaultMaxLength = 8192
	// DefaultCacheSize 是上游每类解析结果的缓存容量。
	DefaultCacheSize = 1024
)

// OS 表示操作系统家族，具体版本和上游名称见 OSInfo。
type OS string

const (
	OSUnknown      OS = ""
	OSOther        OS = "other"
	OSWindows      OS = "windows"
	OSWindowsPhone OS = "windows_phone"
	OSWindowsCE    OS = "windows_ce"
	OSMacOS        OS = "macos"
	OSIOS          OS = "ios"
	OSIPadOS       OS = "ipados"
	OSWatchOS      OS = "watchos"
	OSTvOS         OS = "tvos"
	OSVisionOS     OS = "visionos"
	OSAndroid      OS = "android"
	OSChromeOS     OS = "chromeos"
	OSHarmonyOS    OS = "harmonyos"
	OSOpenHarmony  OS = "openharmony"
	OSLinux        OS = "linux"
	OSUbuntu       OS = "ubuntu"
	OSDebian       OS = "debian"
	OSFedora       OS = "fedora"
	OSCentOS       OS = "centos"
	OSFreeBSD      OS = "freebsd"
	OSOpenBSD      OS = "openbsd"
	OSNetBSD       OS = "netbsd"
	OSSolaris      OS = "solaris"
	OSBlackBerry   OS = "blackberry"
	OSKaiOS        OS = "kaios"
	OSFirefoxOS    OS = "firefox_os"
	OSTizen        OS = "tizen"
	OSWebOS        OS = "webos"
	OSSymbian      OS = "symbian"
	OSBada         OS = "bada"
	OSRoku         OS = "roku"
	OSFireOS       OS = "fire_os"
	OSPlayStation  OS = "playstation"
)

// Platform 表示 OS 所属生态，不表示设备形态或应用容器。
type Platform string

const (
	PlatformUnknown    Platform = ""
	PlatformOther      Platform = "other"
	PlatformWindows    Platform = "windows"
	PlatformApple      Platform = "apple"
	PlatformAndroid    Platform = "android"
	PlatformChromeOS   Platform = "chromeos"
	PlatformHarmony    Platform = "harmony"
	PlatformLinux      Platform = "linux"
	PlatformUnix       Platform = "unix"
	PlatformBlackBerry Platform = "blackberry"
	PlatformKaiOS      Platform = "kaios"
	PlatformTizen      Platform = "tizen"
	PlatformWebOS      Platform = "webos"
	PlatformConsole    Platform = "console"
)

// DeviceType 表示终端形态；机器人和 HTTP 库属于 ClientType，不属于硬件类型。
type DeviceType string

const (
	DeviceUnknown  DeviceType = ""
	DeviceDesktop  DeviceType = "desktop"
	DeviceMobile   DeviceType = "mobile"
	DeviceTablet   DeviceType = "tablet"
	DeviceTV       DeviceType = "tv"
	DeviceConsole  DeviceType = "console"
	DeviceWearable DeviceType = "wearable"
	DeviceEmbedded DeviceType = "embedded"
	DeviceEReader  DeviceType = "ereader"
	DeviceCar      DeviceType = "car"
	DeviceVR       DeviceType = "vr"
)

// Browser 表示浏览器产品家族；应用内浏览器和底层引擎单独记录。
type Browser string

const (
	BrowserUnknown        Browser = ""
	BrowserChrome         Browser = "chrome"
	BrowserChromium       Browser = "chromium"
	BrowserEdge           Browser = "edge"
	BrowserFirefox        Browser = "firefox"
	BrowserSafari         Browser = "safari"
	BrowserIE             Browser = "ie"
	BrowserOpera          Browser = "opera"
	BrowserOperaMini      Browser = "opera_mini"
	BrowserOperaMobile    Browser = "opera_mobile"
	BrowserSamsung        Browser = "samsung"
	BrowserUC             Browser = "uc"
	BrowserQQ             Browser = "qq"
	BrowserBaidu          Browser = "baidu"
	BrowserSogou          Browser = "sogou"
	BrowserMaxthon        Browser = "maxthon"
	BrowserVivaldi        Browser = "vivaldi"
	BrowserYandex         Browser = "yandex"
	BrowserBrave          Browser = "brave"
	BrowserHuawei         Browser = "huawei"
	BrowserMiui           Browser = "miui"
	BrowserQuark          Browser = "quark"
	BrowserWhale          Browser = "whale"
	BrowserPuffin         Browser = "puffin"
	BrowserSilk           Browser = "silk"
	BrowserDuckDuckGo     Browser = "duckduckgo"
	BrowserHeadlessChrome Browser = "headless_chrome"
	BrowserAndroid        Browser = "android"
	BrowserElectron       Browser = "electron"
)

// Engine 表示渲染引擎；UA 未暴露版本时不会借用浏览器版本。
type Engine string

const (
	EngineUnknown  Engine = ""
	EngineBlink    Engine = "blink"
	EngineWebKit   Engine = "webkit"
	EngineGecko    Engine = "gecko"
	EngineTrident  Engine = "trident"
	EngineEdgeHTML Engine = "edgehtml"
	EnginePresto   Engine = "presto"
	EngineKHTML    Engine = "khtml"
	EngineGoanna   Engine = "goanna"
)

// App 表示 UA 中明确声明的宿主应用，不能据此认定所有页面均运行于 WebView。
type App string

const (
	AppUnknown   App = ""
	AppWeChat    App = "wechat"
	AppWeCom     App = "wecom"
	AppQQ        App = "qq"
	AppDingTalk  App = "dingtalk"
	AppAlipay    App = "alipay"
	AppWeibo     App = "weibo"
	AppDouyin    App = "douyin"
	AppTikTok    App = "tiktok"
	AppToutiao   App = "toutiao"
	AppBaidu     App = "baidu"
	AppTaobao    App = "taobao"
	AppJD        App = "jd"
	AppFacebook  App = "facebook"
	AppInstagram App = "instagram"
	AppLine      App = "line"
	AppFeishu    App = "feishu"
	AppLark      App = "lark"
	AppElectron  App = "electron"
)

// Architecture 只记录 UA 明确暴露的架构标记，不推断真实物理 CPU。
type Architecture string

const (
	ArchitectureUnknown Architecture = ""
	ArchitectureX86     Architecture = "x86"
	ArchitectureAMD64   Architecture = "amd64"
	ArchitectureIA64    Architecture = "ia64"
	ArchitectureARM     Architecture = "arm"
	ArchitectureARM64   Architecture = "arm64"
	ArchitecturePPC     Architecture = "ppc"
	ArchitecturePPC64   Architecture = "ppc64"
	ArchitectureMIPS    Architecture = "mips"
	ArchitectureMIPS64  Architecture = "mips64"
	ArchitectureRISCV64 Architecture = "riscv64"
	ArchitectureSPARC   Architecture = "sparc"
)

// ClientType 表示客户端用途；Browser 字段仍可保留兼容标记中的浏览器信息。
type ClientType string

const (
	ClientUnknown     ClientType = ""
	ClientBrowser     ClientType = "browser"
	ClientWebView     ClientType = "webview"
	ClientApp         ClientType = "app"
	ClientBot         ClientType = "bot"
	ClientHTTP        ClientType = "http_client"
	ClientMediaPlayer ClientType = "media_player"
	ClientFeedReader  ClientType = "feed_reader"
	ClientValidator   ClientType = "validator"
)

// BotType 表示自报的机器人用途，不表示经过身份验证。
type BotType string

const (
	BotUnknown BotType = ""
	BotOther   BotType = "other"
	BotSearch  BotType = "search"
	BotAI      BotType = "ai"
	BotSocial  BotType = "social"
	BotMonitor BotType = "monitor"
	BotSEO     BotType = "seo"
	BotArchive BotType = "archive"
)
