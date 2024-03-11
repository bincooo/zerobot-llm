package gpt

import (
	"fmt"
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	"github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

var (
	engine = control.Register("gpt", &ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "gpt",
		Help: "/config         查看全局\n" +
			"/config.key     默认gpt key\n" +
			"/config.model   默认模型类型\n" +
			"/config.baseUrl 默认请求地址\n" +
			"/config.proxies 默认代理\n" +
			"/keys          查看所有key\n" +
			"/set-key       添加｜修改key (私聊)\n" +
			"/del-key       删除key\n" +
			"/chat [key] ?? 指定key进行聊天\n" +
			"@Bot ??        艾特机器人使用默认key聊天",
		PrivateDataFolder: "gpt",
	})

	cacheChatMessages []string
	fmtMessage        = "[%s] %s > %s"
)

func init() {
	engine.OnMessage(onDb).Handle(func(ctx *zero.Ctx) {
		if zero.OnlyToMe(ctx) {
			return
		}

		c := Db.config()
		if !c.Imitate {
			return
		}
		k, err := Db.key(c.Key)
		if err != nil {
			return
		}

		plainText := strings.TrimSpace(ctx.ExtractPlainText())
		if plainText != "" {
			date := time.Now().Format("2006-01-02 15:04:05")
			cacheChatMessages = append(cacheChatMessages, fmt.Sprintf(fmtMessage, ctx.Event.Sender.NickName, date, plainText))
			// 100条
			if l := len(cacheChatMessages); l > 100 {
				cacheChatMessages = cacheChatMessages[l-100:]
			}

			// 随机回复
			if rand.Intn(100) > 90 {
				uid := ctx.Event.UserID
				if ctx.Event.GroupID > 0 {
					uid = ctx.Event.GroupID
				}

				histories, e := Db.findHistory(uid, k.Name, 100)
				if e != nil && !IsSqlNull(e) {
					logrus.Error(e)
					return
				}

				completions(ctx, uid, k.Name, strings.Join(cacheChatMessages, "\n\n"), histories)
				cacheChatMessages = nil
			}
		}
	})

	engine.OnMessage(zero.OnlyToMe, onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.Event.Sender.NickName
		if strings.Contains(name, "Q群管家") {
			return
		}

		c := Db.config()
		plainText := strings.TrimSpace(ctx.ExtractPlainText())
		if len(plainText) == 0 {
			emojis := []string{"😀", "😂", "🙃", "🥲", "🤔", "🤨"}
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}

		if plainText == "reset" || plainText == "重置记忆" {
			err := Db.cleanHistories(uid, c.Key)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已清除上下文！"))
			return
		}

		histories, err := Db.findHistory(uid, c.Key, 100)
		if err != nil && !IsSqlNull(err) {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		if c.Imitate {
			date := time.Now().Format("2006-01-02 15:04:05")
			plainText = fmt.Sprintf(fmtMessage, name, date, plainText)
		}
		completions(ctx, uid, c.Key, plainText, histories)
	})

	engine.OnRegex(`^/chat\s+(\S+)\s*(.*)$`, onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.Event.Sender.NickName
		if strings.Contains(name, "Q群管家") {
			return
		}

		msg := strings.TrimSpace(matched[2])
		if len(msg) == 0 {
			emojis := []string{"😀", "😂", "🙃", "🥲", "🤔", "🤨"}
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}

		if msg == "reset" || msg == "重置记忆" {
			err := Db.cleanHistories(uid, matched[1])
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已清除上下文！"))
			return
		}

		histories, err := Db.findHistory(uid, matched[1], 100)
		if err != nil && !IsSqlNull(err) {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		completions(ctx, uid, matched[1], msg, histories)
	})

	engine.OnRegex(`^/set-key\s+(\S+)\s+(\S+)$`, zero.AdminPermission, zero.OnlyPrivate, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.saveKey(key{Name: matched[1], Content: matched[2]}); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("添加key成功。"))
		})

	engine.OnRegex(`^/del-key\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.delKey(matched[1]); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已删除该key。"))
		})

	engine.OnFullMatch("/keys", onDb).SetBlock(true).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			ks, err := Db.keys()
			if err != nil && !IsSqlNull(err) {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			content := "***  keys  ***\n\n"
			if len(ks) == 0 {
				content += "   ~ none ~"
			}
			for _, k := range ks {
				content += k.Name + "\n"
			}
			ctx.Send(message.Text(content))
		})

	engine.OnFullMatch("/config", zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			c := Db.config()
			content := "***  config  ***\n\n"
			content += "proxies: " + c.Proxies + "\n"
			content += "baseUrl: " + c.BaseUrl + "\n"
			content += "model: " + c.Model + "\n"
			content += "key: " + c.Key + "\n"
			content += "imitate: " + strconv.FormatBool(c.Imitate) + "\n"
			ctx.Send(message.Text(content))
		})

	engine.OnRegex(`^/config\.proxies\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.Proxies = matched[1]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新代理。"))
		})

	engine.OnRegex(`^/config\.baseUrl\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.BaseUrl = matched[1]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新请求地址。"))
		})

	engine.OnRegex(`^/config\.model\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.Model = matched[1]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新模型类型。"))
		})

	engine.OnRegex(`^/config\.key\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.Key = matched[1]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新gpt key。"))
		})

	engine.OnRegex(`^/config\.Im\s(true|false)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			tex := "关闭"
			if matched[1] == "true" {
				c.Imitate = true
				tex = "开启"
			} else {
				c.Imitate = false
				tex = "关闭"
			}

			if err := Db.updateConfig(c); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}

			ctx.Send(message.Text("已" + tex + "模仿模式。"))
		})
}

func IsSqlNull(err error) bool {
	return err != nil && err.Error() == "sqlite: null result"
}
