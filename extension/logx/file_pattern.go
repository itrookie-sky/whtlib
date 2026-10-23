package logx

import (
	"regexp"
	"strings"
	"time"
)

type filePart struct {
	literal string
	token   byte
}

// filePattern 仅解析花括号内的 y/m/d；其他字母不作为占位符。
type filePattern struct {
	parts   []filePart
	matcher *regexp.Regexp
	shape   []rune
	dynamic bool
}

func compileFilePattern(name string) (pattern *filePattern, err error) {
	pattern = &filePattern{}
	var expression strings.Builder
	expression.WriteByte('^')
	inside := false
	tokens := 0
	for _, char := range name {
		switch char {
		case '{':
			if inside {
				return nil, invalidConfig("nested filename expression")
			}
			inside = true
			tokens = 0
			continue
		case '}':
			if !inside || tokens == 0 {
				return nil, invalidConfig("invalid filename expression")
			}
			inside = false
			continue
		}
		if inside {
			width := 0
			switch char {
			case 'y':
				width = 4
				expression.WriteString(`[0-9]{4}`)
			case 'm':
				width = 2
				expression.WriteString(`(?:0[1-9]|1[0-2])`)
			case 'd':
				width = 2
				expression.WriteString(`(?:0[1-9]|[12][0-9]|3[01])`)
			case '-', '_', '.':
			default:
				return nil, invalidConfig("unsupported filename placeholder %q", char)
			}
			if width > 0 {
				pattern.parts = append(pattern.parts, filePart{token: byte(char)})
				for range width {
					pattern.shape = append(pattern.shape, -1)
				}
				tokens++
				pattern.dynamic = true
				continue
			}
		}
		pattern.parts = append(pattern.parts, filePart{literal: string(char)})
		pattern.shape = append(pattern.shape, char)
		expression.WriteString(regexp.QuoteMeta(string(char)))
	}
	if inside {
		return nil, invalidConfig("unclosed filename expression")
	}
	expression.WriteByte('$')
	pattern.matcher, err = regexp.Compile(expression.String())
	return
}

func (p *filePattern) expand(now time.Time) (name string) {
	var result strings.Builder
	for _, part := range p.parts {
		switch part.token {
		case 'y':
			result.WriteString(now.Format("2006"))
		case 'm':
			result.WriteString(now.Format("01"))
		case 'd':
			result.WriteString(now.Format("02"))
		default:
			result.WriteString(part.literal)
		}
	}
	return result.String()
}

// overlaps 保守拒绝可能生成相同名称的表达式，防止多个维护任务管理同一文件。
func (p *filePattern) overlaps(other *filePattern) (overlap bool) {
	if len(p.shape) != len(other.shape) {
		return false
	}
	for i, a := range p.shape {
		b := other.shape[i]
		if a == b {
			continue
		}
		if a == -1 && b >= '0' && b <= '9' {
			continue
		}
		if b == -1 && a >= '0' && a <= '9' {
			continue
		}
		if strings.EqualFold(string(a), string(b)) {
			continue
		}
		return false
	}
	return true
}
