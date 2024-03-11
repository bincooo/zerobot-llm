package gpt

import (
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
	"math/rand"
	"strings"
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
			"/set-key       添加｜修改key\n" +
			"/del-key       删除key\n" +
			"/chat [key] ?? 指定key进行聊天\n" +
			"@Bot ??        艾特机器人使用默认key聊天",
		PrivateDataFolder: "gpt",
	})
)

func init() {
	engine.OnMessage(zero.OnlyToMe).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.Event.Sender.NickName
		if strings.Contains(name, "Q群管家") {
			return
		}

		c := Db.config()
		histories, err := Db.findHistory(uid, c.Key, 100)
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		plainText := ctx.ExtractPlainText()
		if len(strings.TrimSpace(plainText)) == 0 {
			emojis := "😀😂🙃🥲🤔🤨"
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}
		completions(ctx, uid, c.Key, plainText, histories)
	})

	engine.OnRegex(`^/chat \s+(\S+)\s+(.+)$`, onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.Event.Sender.NickName
		if strings.Contains(name, "Q群管家") {
			return
		}

		histories, err := Db.findHistory(uid, matched[0], 100)
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}
		if len(strings.TrimSpace(matched[1])) == 0 {
			emojis := "😀😂🙃🥲🤔🤨"
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}
		completions(ctx, uid, matched[0], matched[1], histories)
	})

	engine.OnRegex(`^/set-key\s+(\S+)\s+(.+)$`, zero.AdminPermission, zero.OnlyPrivate, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.addKey(matched[0], matched[1]); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("添加key成功。"))
		})

	engine.OnRegex(`^/del-key\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.delKey(matched[0]); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已删除该key。"))
		})

	engine.OnFullMatch("/keys", onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		ks, err := Db.keys()
		if err != nil {
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

	engine.OnFullMatch("/config", zero.AdminPermission, onDb).Handle(func(ctx *zero.Ctx) {
		c := Db.config()
		content := "***  config  ***\n\n"
		content += "proxies: " + c.Proxies + "\n"
		content += "baseUrl: " + c.BaseUrl + "\n"
		content += "model: " + c.Model + "\n"
		content += "key: " + c.Key + "\n"
		ctx.Send(message.Text(content))
	})

	engine.OnRegex(`^/config\.proxies\s*(\S?)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.Proxies = matched[0]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新代理。"))
		})

	engine.OnRegex(`^/config\.baseUrl\s*(\S?)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.BaseUrl = matched[0]
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
			c.Model = matched[0]
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
			c.Key = matched[0]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新gpt key。"))
		})
}
