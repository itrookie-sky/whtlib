# tkua

将 User-Agent 解析为框架无关的结构化结果，基础客户端、系统与设备信息复用
[uap-go](https://github.com/ua-parser/uap-go) 的内嵌规则。运行时不访问网络。
全部枚举及默认值集中在 [consts.go](consts.go)，错误集中在 [error.go](error.go)。

## 使用

```go
info, err := tkua.Parse(req.UserAgent())
if err != nil {
    return err
}
if info.OS.Family == tkua.OSAndroid && info.App.Family == tkua.AppWeChat {
    // Android 微信容器。
}
if info.IsMobile() {
    // 手机终端，不包含平板。
}
```

默认入口懒初始化并复用解析器。自定义输入限制或缓存容量时创建独立实例：

```go
parser, err := tkua.New(tkua.Config{
    MaxLength: 16384,
    CacheSize: 2048,
})
if err != nil {
    return err
}
info, err := parser.Parse(ua)
```

Config 字段为零时使用默认值：MaxLength 为 8192 字节，CacheSize 为每类上游结果 1024 条。
负数返回 `ErrInvalidConfig`，缓存不支持零容量关闭。Parser 创建后可并发使用，不需要 Close；
返回值不暴露上游指针，调用方修改 Result 不会影响缓存或其他调用。

## 结果字段

| 字段 | 含义 |
| --- | --- |
| Raw | 原始输入，保留空白 |
| Client | 上游识别的主要客户端名称、版本和用途 |
| Browser | 标准浏览器枚举与产品标记中的版本，可能是宿主应用的兼容标记 |
| OS | 标准系统枚举、上游名称、版本 |
| Platform | 系统生态，例如 Windows、Apple、Android、Linux、Unix、Harmony |
| Device | 终端形态、上游设备家族、品牌、型号 |
| Engine | 渲染引擎及 UA 明确提供的版本 |
| App | 微信、企业微信、钉钉等宿主应用及版本 |
| Architecture | UA 显式暴露的架构标记 |
| Bot | 自报机器人名称与用途，不能用于身份验证 |

Version 包含 Raw、Major、Minor、Patch、Build；缺失分段为空。
Raw 保留来源版本字符串，解析时允许点或下划线分段，第四段之后的内容保留在 Build 中。
Client 和 OS 的版本来自上游，Browser 与 App 版本优先使用完整产品标记。

未知枚举的值统一为 `""`，例如 `OSUnknown`、`DeviceUnknown`。OS 已识别但未收录在枚举时使用
`OSOther` 并保留 OS.Name；未归入浏览器枚举的客户端仍保留 Client.Name。
所有枚举基于 string，JSON 字段使用 camelCase，适合日志、持久化和业务判断。

## 枚举覆盖

| 类别 | 已声明的主要取值 |
| --- | --- |
| OS | Windows、Windows Phone/CE、macOS、iOS、iPadOS、watchOS、tvOS、visionOS、Android、ChromeOS、HarmonyOS/OpenHarmony、Linux、Ubuntu/Debian/Fedora/CentOS、FreeBSD/OpenBSD/NetBSD、Solaris、BlackBerry、KaiOS/Firefox OS、Tizen、webOS、Symbian、Bada、Roku、Fire OS、PlayStation |
| Platform | Windows、Apple、Android、ChromeOS、Harmony、Linux、Unix、BlackBerry、KaiOS、Tizen、webOS、Console、Other |
| DeviceType | Desktop、Mobile、Tablet、TV、Console、Wearable、Embedded、EReader、Car、VR |
| Browser | Chrome/Chromium/HeadlessChrome、Edge、Firefox、Safari、IE、Opera/Mini/Mobile、Samsung、UC、QQ、Baidu、Sogou、Maxthon、Vivaldi、Yandex、Brave、Huawei、Miui、Quark、Whale、Puffin、Silk、DuckDuckGo、Android Browser、Electron |
| Engine | Blink、WebKit、Gecko、Trident、EdgeHTML、Presto、KHTML、Goanna |
| App | WeChat、WeCom、QQ、DingTalk、Alipay、Weibo、Douyin、TikTok、Toutiao、Baidu、Taobao、JD、Facebook、Instagram、Line、Feishu、Lark、Electron |
| Architecture | x86、amd64、ia64、arm、arm64、ppc、ppc64、mips、mips64、riscv64、sparc |
| ClientType | Browser、WebView、App、Bot、HTTPClient、MediaPlayer、FeedReader、Validator |
| BotType | Search、AI、Social、Monitor、SEO、Archive、Other |

客户端用途优先级为：Bot → HTTP 库 → 播放器/订阅器/校验器 → 明确 WebView → App → Browser。
浏览器外壳先于 Chrome/Safari 兼容标记；企业微信先于微信。
平台描述系统生态，DeviceType 描述终端形态：例如 Android 电视为 Android + TV。
Bot 和 HTTP 库不推断硬件类型；上游的 Spider 标签不会填成设备品牌型号。

## 判断边界

- 空 UA、空白、未知 UA 都返回成功及未知字段，不把“无法识别”当作解析错误。
- 超长输入返回 `ErrTooLong`，不会截断或进入缓存；`errors.Is` 可判断错误类别。
- nil Parser 或零值 Parser 返回 `ErrNotInitialized`，请使用 New 或包级 Parse。
- UA 是客户端自报文本，可以伪装；机器人识别不是身份认证。
- 仅靠 Windows NT 10.0 无法区分 Windows 10/11，保留上游 UA 分类，不推断实际系统。
- iPad 可能使用 macOS UA；macOS + Mobile 的歧义组合不强行归为手机或平板。
- Android 缺少 Mobile 不代表平板；Brave、部分国产浏览器没有独立标记时可能只识别为 Chrome。
- iOS 上 CriOS/FxiOS 不等于 Blink/Gecko，仍按实际 WebKit 标记识别；Gecko 构建日期不当作版本。
- Blink 未暴露独立版本时保持版本为空，不把 Chrome 版本填入；旧 Chrome 仍按 WebKit 识别。
- 单独的 Intel、Win64、WoW64 不足以确认架构，保持未知；Architecture 只是 UA 的架构声明，不能断言物理硬件。
- 本包只解析 UA，不读取 Client Hints，不补造被 UA Reduction 隐藏的系统版本和设备型号。

新增产品规则应在对应的分类文件维护，公开枚举只放 consts.go。更新 uap-go 版本后应重跑样本测试，
因为上游的客户端名称、系统名称或设备匹配可能随规则库改变。
