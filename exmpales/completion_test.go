package exmpales

import (
	"github.com/bincooo/go.emoji"
	"testing"
)

func TestEmoji(t *testing.T) {
	t.Log(cleanEmoji("hi🇨🇳🇨🇳🇨🇳US🇺🇸"))
}

// 只保留一个emoji
func cleanEmoji(raw string) string {
	var (
		pos      int
		previous string
	)

	return emoji.ReplaceEmoji(raw, func(index int, emoji string) string {
		if index-len(emoji) != pos {
			previous = emoji
			pos = index
			return emoji
		}

		if emoji == previous {
			pos = index
			return ""
		}

		previous = emoji
		pos = index
		return emoji
	})
}
