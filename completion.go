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
		if zero.OnlyPrivate(ctx) {
			ctx.SendChain(message.Text("正在响应..."))
		} else {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("正在响应..."))
		}

		result, err = waitResponse(ch)
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
		result, err = batchResponse(ctx, ch, []string{"!", ".", "?", "！", "。", "？"})
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

func batchResponse(ctx *zero.Ctx, ch chan string, symbols []string) (result string, err error) {
	buf := ""

	for {
		text, ok := <-ch
		if !ok {
			if buf != "" {
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
		for _, symbol := range symbols {
			index := strings.Index(buf, symbol)
			if index > 0 {
				if !zero.OnlyPrivate(ctx) && ctx.Event.IsToMe {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(buf[:index+len(symbol)]))
				} else {
					ctx.SendChain(message.Text(buf[:index+len(symbol)]))
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

		ch <- fmt.Sprintf("text: %s", res.Choices[0].Delta.Content)
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
