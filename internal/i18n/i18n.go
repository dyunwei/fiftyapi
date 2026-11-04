// Package i18n localizes fixed operational messages without changing stored data.
package i18n

import (
	"embed"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed en.json
var catalog embed.FS
var english = func() map[string]string {
	data, err := catalog.ReadFile("en.json")
	if err != nil {
		panic(err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		panic(err)
	}
	return messages
}()
var prefixes = func() []string {
	var keys []string
	for key := range english {
		if strings.HasSuffix(key, "：") || strings.HasSuffix(key, "（") {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return keys
}()

type formattedMessage struct {
	pattern     *regexp.Regexp
	translation string
}

var formatToken = regexp.MustCompile(`%[dswvt]`)
var formats = func() []formattedMessage {
	var result []formattedMessage
	for key, value := range english {
		if !formatToken.MatchString(key) {
			continue
		}
		parts := formatToken.Split(key, -1)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		result = append(result, formattedMessage{regexp.MustCompile("^" + strings.Join(parts, "(.*?)") + "$"), value})
	}
	return result
}()

// Language selects the highest-weight supported Accept-Language entry.
// Missing or unsupported preferences retain Chinese for existing API clients.
func Language(header string) string {
	selected, best := "zh-CN", 0.0
	for _, entry := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ";")
		tag, weight := strings.ToLower(strings.TrimSpace(parts[0])), 1.0
		for _, parameter := range parts[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					weight = 0
				} else {
					weight = parsed
				}
			}
		}
		language := ""
		if tag == "en" || strings.HasPrefix(tag, "en-") {
			language = "en"
		}
		if tag == "zh" || strings.HasPrefix(tag, "zh-") {
			language = "zh-CN"
		}
		if language != "" && weight > best {
			selected, best = language, weight
		}
	}
	return selected
}

func Translate(language, message string) string {
	if language != "en" {
		return message
	}
	if value, ok := english[message]; ok {
		return value
	}
	for _, format := range formats {
		if matches := format.pattern.FindStringSubmatch(message); matches != nil {
			index := 0
			return formatToken.ReplaceAllStringFunc(format.translation, func(string) string { index++; return matches[index] })
		}
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(message, prefix) {
			return english[prefix] + Translate(language, strings.TrimPrefix(message, prefix))
		}
	}
	return message
}
