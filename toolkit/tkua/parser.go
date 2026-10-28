// Package tkua 将 User-Agent 解析为结构化信息，所有结果均为 UA 自报信息。
package tkua

import (
	"strings"
	"sync"

	"github.com/ua-parser/uap-go/uaparser"
)

// Parser 可并发复用，配置在创建后不可变；请使用 New 初始化。
type Parser struct {
	native    *uaparser.Parser
	maxLength int
}

var defaultParser = sync.OnceValues(func() (parser *Parser, err error) { return New(Config{}) })

// New 创建解析器并编译内嵌规则，不访问网络；配置零值使用默认值。
func New(config Config) (parser *Parser, err error) {
	if config.MaxLength < 0 || config.CacheSize < 0 {
		return nil, invalidConfig()
	}
	if config.MaxLength == 0 {
		config.MaxLength = DefaultMaxLength
	}
	if config.CacheSize == 0 {
		config.CacheSize = DefaultCacheSize
	}
	native, err := uaparser.New(uaparser.WithCacheSize(config.CacheSize), uaparser.WithSort(false))
	if err != nil {
		return nil, initError(err)
	}
	return &Parser{native: native, maxLength: config.MaxLength}, nil
}

// Parse 使用默认解析器；空字符串和无法识别的 UA 返回未知字段，不视为错误。
func Parse(raw string) (result Result, err error) {
	if len(raw) > DefaultMaxLength {
		return result, tooLong(len(raw), DefaultMaxLength)
	}
	parser, err := defaultParser()
	if err != nil {
		return
	}
	return parser.Parse(raw)
}

// Parse 解析 UA；超长输入不会被截断，也不会进入上游缓存。
func (p *Parser) Parse(raw string) (result Result, err error) {
	if p == nil || p.native == nil {
		return result, ErrNotInitialized
	}
	if len(raw) > p.maxLength {
		return result, tooLong(len(raw), p.maxLength)
	}
	result.Raw = raw
	input := strings.TrimSpace(raw)
	if input == "" {
		return
	}
	parsed := p.native.Parse(input)
	result.Client = ClientInfo{Name: knownName(parsed.UserAgent.Family), Version: parseVersion(parsed.UserAgent.ToVersionString())}
	result.OS = OSInfo{Name: knownName(parsed.Os.Family), Version: parseVersion(parsed.Os.ToVersionString())}
	result.Device = DeviceInfo{Family: knownName(parsed.Device.Family), Brand: knownName(parsed.Device.Brand), Model: knownName(parsed.Device.Model)}
	products := parseProducts(input)
	result.OS.Family = classifyOS(result.OS.Name)
	applyExplicitOS(&result.OS, products)
	result.Platform = platformFor(result.OS.Family)
	result.Browser = browserFor(products, result.OS.Family)
	result.App = appFor(input, products)
	result.Bot = botFor(input, result.Device.Family)
	result.Engine = engineFor(products, result.OS.Family)
	result.Architecture = architectureFor(input)
	result.Client.Type = clientTypeFor(input, products, result)
	result.Device.Type = deviceTypeFor(input, result)
	if result.IsBot() {
		if result.Bot.Name == "" {
			result.Bot.Name = result.Client.Name
		}
		// 上游的 Spider/Desktop 是爬虫分类标签，不是设备品牌型号。
		result.Device = DeviceInfo{}
	}
	return
}

func knownName(name string) (known string) {
	if name == "Other" || name == "Generic" {
		return
	}
	return name
}

// Config 配置输入限制与上游缓存；零值使用默认值，负值无效。
type Config struct {
	MaxLength int // UA 最大字节数，默认 DefaultMaxLength。
	CacheSize int // 每类解析结果的缓存容量，默认 DefaultCacheSize。
}

// Version 保留上游或产品标记中的版本；缺失分段为空，不将其补成 0。
type Version struct {
	Raw   string `json:"raw"`
	Major string `json:"major"`
	Minor string `json:"minor"`
	Patch string `json:"patch"`
	Build string `json:"build"`
}

// ClientInfo 描述上游识别的主要客户端及其用途。
type ClientInfo struct {
	Name    string     `json:"name"` // 上游名称，未识别时为空。
	Type    ClientType `json:"type"`
	Version Version    `json:"version"`
}

// BrowserInfo 描述可识别的浏览器产品标记，可能是宿主应用的兼容标记。
type BrowserInfo struct {
	Family  Browser `json:"family"`
	Version Version `json:"version"`
}

// OSInfo 保留操作系统的标准家族、上游名称和版本。
type OSInfo struct {
	Family  OS      `json:"family"`
	Name    string  `json:"name"`
	Version Version `json:"version"`
}

// DeviceInfo 描述终端；型号和品牌来自上游规则，未知值为空。
type DeviceInfo struct {
	Type   DeviceType `json:"type"`
	Family string     `json:"family"`
	Brand  string     `json:"brand"`
	Model  string     `json:"model"`
}

// EngineInfo 描述渲染引擎，版本仅在 UA 提供时记录。
type EngineInfo struct {
	Family  Engine  `json:"family"`
	Version Version `json:"version"`
}

// AppInfo 描述宿主应用及显式版本。
type AppInfo struct {
	Family  App     `json:"family"`
	Version Version `json:"version"`
}

// BotInfo 描述 UA 自报的机器人名称与类别。
type BotInfo struct {
	Name string  `json:"name"`
	Type BotType `json:"type"`
}

// Result 是独立的解析快照，不包含上游缓存指针；未知枚举为空字符串。
type Result struct {
	Raw          string       `json:"raw"`
	Client       ClientInfo   `json:"client"`
	Browser      BrowserInfo  `json:"browser"`
	OS           OSInfo       `json:"os"`
	Platform     Platform     `json:"platform"`
	Device       DeviceInfo   `json:"device"`
	Engine       EngineInfo   `json:"engine"`
	App          AppInfo      `json:"app"`
	Architecture Architecture `json:"architecture"`
	Bot          BotInfo      `json:"bot"`
}

// IsBot 判断是否识别为自报机器人。
func (r Result) IsBot() (yes bool) { return r.Client.Type == ClientBot }

// IsMobile 判断是否为手机终端，不包含平板。
func (r Result) IsMobile() (yes bool) { return r.Device.Type == DeviceMobile }

// IsTablet 判断是否为平板终端。
func (r Result) IsTablet() (yes bool) { return r.Device.Type == DeviceTablet }

// IsDesktop 判断是否为桌面终端。
func (r Result) IsDesktop() (yes bool) { return r.Device.Type == DeviceDesktop }

// IsWebView 判断 UA 是否明确包含 WebView 特征。
func (r Result) IsWebView() (yes bool) { return r.Client.Type == ClientWebView }

func parseVersion(raw string) (version Version) {
	version.Raw = raw
	parts := strings.SplitN(strings.ReplaceAll(raw, "_", "."), ".", 4)
	fields := []*string{&version.Major, &version.Minor, &version.Patch, &version.Build}
	for i, part := range parts {
		*fields[i] = part
	}
	return
}
