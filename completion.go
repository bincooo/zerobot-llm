package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bincooo/emit.io"
	"github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/bincooo/go.emoji"
)

type Request struct {
	ChatId        *string                `json:"chatId"`
	Vars          map[string]interface{} `json:"variables"`
	Messages      []map[string]string    `json:"messages"`
	Model         string                 `json:"model"`
	MaxTokens     int                    `json:"max_tokens"`
	StopSequences []string               `json:"stop_sequences"`
	Temperature   float32                `json:"temperature"`
	TopK          int                    `json:"topK"`
	TopP          float32                `json:"topP"`
	Stream        bool                   `json:"stream"`
}

type Response struct {
	Id      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Code    int      `json:"code"`
	Message string   `json:"message"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type Choice struct {
	Index int `json:"index"`
	Delta *struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

var (
	FEPrefix = []byte(`{"message":`)

	limit = time.Now().Add(-100 * time.Second)
)

// 对话
func completions(ctx *zero.Ctx, uid int64, name, content string, histories []*History) {
	logrus.Infof("开始对话 [%d] ...", uid)
	messages := make([]map[string]string, 0)
	for hL := len(histories) - 1; hL >= 0; hL-- {
		h := histories[hL]
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": h.UserContent,
		})
		messages = append(messages, map[string]string{
			"role":    "assistant",
			"content": h.AssistantContent,
		})
	}

	messages = append(messages, map[string]string{
		"role":    "user",
		"content": content,
	})

	c := Db.config()
	im := false
	if c.Key == name {
		im = c.Imitate
	}

	payload := Request{
		// ChatId:      strconv.FormatInt(uid, 10),
		Vars: map[string]interface{}{
			"userId":   fmt.Sprintf("%d", ctx.Event.Sender.ID),
			"groupId":  fmt.Sprintf("%d", ctx.Event.GroupID),
			"nickname": ctx.CardOrNickName(ctx.Event.UserID),
		},
		Model:       c.Model,
		Messages:    messages,
		MaxTokens:   2048,
		Temperature: .8,
		Stream:      true,
	}

	k, err := Db.key(name)
	if err != nil {
		ctx.Send(message.Text("ERROR: Key query -> ", err))
		return
	}

	timeout, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	response, err := emit.ClientBuilder().
		Context(timeout).
		Proxies(c.Proxies).
		POST(c.BaseUrl+"/v1/chat/completions").
		JHeader().
		Header("Authorization", "Bearer "+k.Content).
		Body(payload).
		DoC(emit.Status(http.StatusOK), emit.IsSTREAM)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "429 Too Many Requests") || strings.Contains(errStr, "400 Bad Request") {
			limit.Add(60 * time.Second)
		}
		// ctx.Send(message.Text("ERROR: ", err))
		logrus.Error(err)
		return
	}

	ch := make(chan string)
	go resolve(response, ch)

	result := ""
	if !im {
		var messageID message.MessageID
		if zero.OnlyPrivate(ctx) {
			messageID = ctx.SendChain(message.Text("正在响应..."))
		} else {
			messageID = ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("正在响应..."))
		}

		result, err = waitResponse(ch)
		ctx.DeleteMessage(messageID)
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		if zero.OnlyPrivate(ctx) {
			ctx.SendChain(message.Text(result))
		} else {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(result))
		}
		return
	} else {
		result, err = batchResponse(ctx, ch, []string{"!", "...", ".", "！", "。。。", "。", "\n\n"}, []string{".", "。", "\n\n"})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}
	}

	if strings.TrimSpace(result) == "Oops" {
		logrus.Warn("completions Oops.")
		return
	}

	err = Db.saveHistory(History{
		Timestamp:        time.Now().Unix(),
		Uid:              uid,
		Name:             name,
		UserContent:      content,
		AssistantContent: result,
	})
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
	}
	logrus.Infof("结束对话 [%d] .", uid)
}

func batchResponse(ctx *zero.Ctx, ch chan string, symbols []string, igSymbols []string) (result string, err error) {
	buf := ""
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		toAt := ctx.Event.IsToMe
		if toAt { // 减少At别人
			toAt = !zero.OnlyPrivate(ctx) && rand.Intn(2) < 1
		}

		text, ok := <-ch
		if !ok {
			if tex := strings.TrimSpace(buf); tex != "" {
				if toAt {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(tex))
				} else {
					ctx.SendChain(message.Text(tex))
				}
			}
			return
		}

		if strings.HasPrefix(text, "error: ") {
			return "", errors.New(strings.TrimPrefix(text, "error: "))
		}

		text = strings.TrimPrefix(text, "text: ")
		buf += text
		buf = cleanEmoji(buf, r.Intn(3) > 0)
		result += text
		result = cleanEmoji(result, r.Intn(3) > 0)

		for _, symbol := range symbols {
			index := strings.Index(buf, symbol)
			if index > 0 {
				l := 0
				if !Contains(igSymbols, symbol) {
					l = len(symbol)
				}

				tex := strings.TrimSpace(buf[:index+l])
				if tex != "" && toAt {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(tex))
				} else {
					ctx.SendChain(message.Text(tex))
				}
				buf = buf[index+len(symbol):]
			}
		}
	}
}

func waitResponse(ch chan string) (result string, err error) {
	for {
		text, ok := <-ch
		if !ok {
			break
		}

		if strings.HasPrefix(text, "error: ") {
			return "", errors.New(strings.TrimPrefix(text, "error: "))
		}

		text = strings.TrimPrefix(text, "text: ")
		result += text
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	result = cleanEmoji(result, r.Intn(3) > 0)
	return
}

func resolve(response *http.Response, ch chan string) {
	defer close(ch)
	r := bufio.NewReader(response.Body)
	before := []byte("data: ")
	done := []byte("[DONE]")
	var data []byte

	for {
		line, prefix, err := r.ReadLine()
		if err != nil {
			if err != io.EOF {
				ch <- fmt.Sprintf("error: %v", err)
			}
			return
		}

		data = append(data, line...)
		if prefix {
			continue
		}

		if !bytes.HasPrefix(data, before) {
			data = nil
			continue
		}

		var res Response
		data = bytes.TrimPrefix(data, before)
		if bytes.Equal(data, done) {
			return
		}

		// FastGPT 的自定义错误
		if bytes.HasPrefix(data, FEPrefix) {
			var obj map[string]interface{}
			if e := json.Unmarshal(data, &obj); e != nil {
				ch <- fmt.Sprintf("error: %v", e)
				return
			}
			if msg, ok := obj["message"]; ok && msg != "" {
				ch <- fmt.Sprintf("error: %s", msg)
				return
			}
		}

		if err = json.Unmarshal(data, &res); err != nil {
			ch <- fmt.Sprintf("error: %v", err)
			return
		}

		if res.Error != nil {
			ch <- fmt.Sprintf("error: %s", res.Error.Message)
			return
		}

		if res.Code > 0 {
			ch <- fmt.Sprintf("error: %s", res.Message)
			return
		}

		if len(res.Choices) > 0 {
			ch <- fmt.Sprintf("text: %s", res.Choices[0].Delta.Content)
		}
		data = nil
	}
}

// 只保留一个emoji, flag 为true时不保留
func cleanEmoji(raw string, flag bool) string {
	var (
		pos      int
		previous string
	)

	return emoji.ReplaceEmoji(raw, func(index int, emoji string) string {
		if index-len(emoji) != pos {
			previous = emoji
			pos = index
			if flag {
				return ""
			}
			return emoji
		}

		if emoji == previous {
			pos = index
			return ""
		}

		previous = emoji
		pos = index
		if flag {
			return ""
		}
		return emoji
	})
}

func Contains[T comparable](list []T, item T) bool {
	for _, it := range list {
		if it == item {
			return true
		}
	}
	return false
}
