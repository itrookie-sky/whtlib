package tkua

import (
	"regexp"
	"strings"
)

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
