package tkua

import "strings"

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
