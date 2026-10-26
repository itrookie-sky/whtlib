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
