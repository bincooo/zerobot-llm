package gpt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
	"golang.org/x/net/proxy"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type chatRequest struct {
	Messages      []map[string]string `json:"messages"`
	Model         string              `json:"model"`
	MaxTokens     int                 `json:"max_tokens"`
	StopSequences []string            `json:"stop_sequences"`
	Temperature   float32             `json:"temperature"`
	TopK          int                 `json:"topK"`
	TopP          float32             `json:"topP"`
	Stream        bool                `json:"stream"`
}

type chatResponse struct {
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

type ChatGenRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Quality string `json:"quality"`
	N       int    `json:"n"`
	Size    string `json:"size"`
	Style   string `json:"style"`
}

var (
	paintModels     = []string{"dall-e-3", "pg.dall-e-3"}
	fastErrorPrefix = []byte(`{"message":`)
)

// 画图
func generation(ctx *zero.Ctx, text string) {
	c := Db.config()
	k := c.PaintKey
	if k == "" {
		ctx.Send(message.Text("ERROR: 绘画key为空"))
		return
	}

	m := paintModels[rand.Intn(2)]
	q := "standard"
	s := "vivid"

	var payload = ChatGenRequest{
		Model:   m,
		Quality: q,
		Style:   s,
		Prompt:  text,
		N:       1,
	}

	client, err := newClient(c.Proxies)
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	marshal, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, c.PaintUrl+"/v1/images/generations", bytes.NewReader(marshal))
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	h := request.Header
	h.Set("authorization", "Bearer "+k)
	h.Set("content-type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	if response.StatusCode != http.StatusOK {
		ctx.Send(message.Text("ERROR: ", response.Status))
		return
	}

	var result map[string]interface{}
	data, err := io.ReadAll(response.Body)
	if err = json.Unmarshal(data, &result); err != nil {
		ctx.Send(message.Text("ERROR: ", response.Status))
		return
	}

	if errMessage, ok := result["error"]; ok {
		msg := errMessage.(map[string]interface{})["message"]
		ctx.Send(message.Text(fmt.Sprintf("ERROR: %s", msg)))
		return
	}

	list := result["data"].([]interface{})
	if len(list) == 0 {
		ctx.Send(message.Text("ERROR: 请求失败"))
		return
	}

	d := list[0].(map[string]interface{})
	response, err = client.Get(d["url"].(string))
	if err != nil {
		ctx.Send(message.Text("ERROR: 下载图片失败 > ", err))
		return
	}

	if response.StatusCode != http.StatusOK {
		ctx.Send(message.Text("ERROR: ", response.Status))
		return
	}

	data, err = io.ReadAll(response.Body)
	if err != nil {
		ctx.Send(message.Text("ERROR: 下载图片失败 > ", err))
		return
	}

	ctx.SendChain(message.Reply(ctx.Event.MessageID), message.ImageBytes(data))
}

// 对话
func completions(ctx *zero.Ctx, uid int64, name, content string, histories []*history) {
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
	client, err := newClient(c.Proxies)
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	im := false
	if c.Key == name {
		im = c.Imitate
	}

	payload := chatRequest{
		Model:       c.Model,
		Messages:    messages,
		MaxTokens:   2048,
		Temperature: .8,
		Stream:      true,
	}

	marshal, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, c.BaseUrl+"/v1/chat/completions", bytes.NewReader(marshal))
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	k, err := Db.key(name)
	if err != nil {
		ctx.Send(message.Text("ERROR: key query -> ", err))
		return
	}

	h := request.Header
	h.Set("authorization", "Bearer "+k.Content)
	h.Set("content-type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	if response.StatusCode != http.StatusOK {
		ctx.Send(message.Text("ERROR: ", response.Status))
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
	} else {
		result, err = batchResponse(ctx, ch, []string{"!", ".", "?", "！", "。", "？", "\n\n"}, []string{".", "。", "\n\n"})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}
	}

	err = Db.saveHistory(history{
		Timestamp:        time.Now().Unix(),
		Uid:              uid,
		Name:             name,
		UserContent:      content,
		AssistantContent: result,
	})
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
	}
}

func batchResponse(ctx *zero.Ctx, ch chan string, symbols []string, igSymbols []string) (result string, err error) {
	buf := ""

	for {
		text, ok := <-ch
		if !ok {
			if strings.TrimSpace(buf) != "" {
				if ctx.Event.IsToMe {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(buf))
				} else {
					ctx.SendChain(message.Text(buf))
				}
			}
			return
		}

		if strings.HasPrefix(text, "error: ") {
			return "", errors.New(strings.TrimPrefix(text, "error: "))
		}

		text = strings.TrimPrefix(text, "text: ")
		result += text

		buf += text

		toAt := ctx.Event.IsToMe
		if toAt { // 减少At别人
			toAt = rand.Intn(2) < 1
		}

		for _, symbol := range symbols {
			index := strings.Index(buf, symbol)
			if index > 0 {
				l := 0
				if !Contains(igSymbols, symbol) {
					l = len(symbol)
				}
				if !zero.OnlyPrivate(ctx) && toAt {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(buf[:index+l]))
				} else {
					ctx.SendChain(message.Text(buf[:index+l]))
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
			return
		}

		if strings.HasPrefix(text, "error: ") {
			return "", errors.New(strings.TrimPrefix(text, "error: "))
		}

		text = strings.TrimPrefix(text, "text: ")
		result += text
	}
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

		var res chatResponse
		data = bytes.TrimPrefix(data, before)
		if bytes.Equal(data, done) {
			return
		}

		// FastGPT 的自定义错误
		if bytes.HasPrefix(data, fastErrorPrefix) {
			var obj map[string]interface{}
			if e := json.Unmarshal(line, &obj); e != nil {
				ch <- fmt.Sprintf("error: %v", err)
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

func newClient(proxies string) (*http.Client, error) {
	client := http.DefaultClient
	if proxies != "" {
		proxiesUrl, err := url.Parse(proxies)
		if err != nil {
			return nil, err
		}

		if proxiesUrl.Scheme == "http" || proxiesUrl.Scheme == "https" {
			client = &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyURL(proxiesUrl),
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
					},
				},
			}
		}

		// socks5://127.0.0.1:7890
		if proxiesUrl.Scheme == "socks5" {
			client = &http.Client{
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						dialer, e := proxy.SOCKS5("tcp", proxiesUrl.Host, nil, proxy.Direct)
						if e != nil {
							return nil, e
						}
						return dialer.Dial(network, addr)
					},
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
					},
				},
			}
		}
	}

	return client, nil
}

func Contains[T comparable](list []T, item T) bool {
	for _, it := range list {
		if it == item {
			return true
		}
	}
	return false
}
