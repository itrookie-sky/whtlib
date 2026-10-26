package tkua

import "regexp"

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
