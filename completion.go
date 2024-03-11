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
	for _, h := range histories {
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
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	h := request.Header
	h.Set("authorization", k.Content)
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
	defer close(ch)

	go resolve(response, ch)
	result, err := waitResponse(ch)
	if err != nil {
		ctx.Send(message.Text("ERROR: ", err))
		return
	}

	ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(result))
	err = Db.addHistory(history{
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
	r := bufio.NewReader(response.Body)
	before := []byte("data: ")
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
		if err = json.Unmarshal(data, &res); err != nil {
			ch <- fmt.Sprintf("error: %v", err)
			return
		}

		if res.Error != nil {
			ch <- fmt.Sprintf("error: %s", res.Error.Message)
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
