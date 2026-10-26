package tkua

import "strings"

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
