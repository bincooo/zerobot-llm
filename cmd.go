package llm

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FloatTech/zbputils/control"
	"github.com/sirupsen/logrus"
	"github.com/wdvxdr1123/ZeroBot/extension/rate"
	"github.com/wdvxdr1123/ZeroBot/message"

	ctrl "github.com/FloatTech/zbpctrl"
	zero "github.com/wdvxdr1123/ZeroBot"
)

var (
	engine = control.Register("llm", &ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "llm",
		Help: "/config         查看全局配置\n" +
			"/config.Key     默认llm Key (Key name)\n" +
			"/config.model   默认模型类型\n" +
			"/config.baseUrl 默认请求地址\n" +
			"/config.proxies 默认代理\n" +
			"/config.imitate 默认开启自由发言 (true|false)\n" +
			"/config.freq    自由发言频率 (0~100)\n" +
			"/keys          查看所有key\n" +
			"/set-key       添加｜修改key (私聊)\n" +
			"/del-key       删除key\n" +
			"/chat [Key] ?? 指定key进行聊天\n" +
			"@Bot ??        艾特机器人使用默认key聊天",
		PrivateDataFolder: "llm",
	})

	chatMessages map[int64][]cacheMessage
	fmtMessage   = "%s uid为 [ %d ], 昵称为[ %s ] 的群友发送消息: \n%s"
	messageL     = 10
	historyL     = 50
	mu           sync.Mutex

	// 每个聊天室限流3s一次
	limitManager = rate.NewManager[int64](3*time.Second, 1)
)

type cacheMessage struct {
	time.Time
	uid      int64
	nickname string
	content  string
}

func (c cacheMessage) String() string {
	return fmt.Sprintf(fmtMessage, c.Format("2006-01-02 15:04:05"), c.uid, c.nickname, c.content)
}

func init() {
	chatMessages = make(map[int64][]cacheMessage)
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

		name := ctx.CardOrNickName(ctx.Event.UserID)
		if strings.Contains(name, "Q群管家") {
			return
		}
		plainMessage := ExtPlainMessage(ctx)
		if plainMessage != "" {
			uid := ctx.Event.UserID
			if ctx.Event.GroupID > 0 {
				uid = ctx.Event.GroupID
			}

			mu.Lock()
			chatMessages[uid] = append(chatMessages[uid], cacheMessage{
				Time:     time.Now(),
				uid:      ctx.Event.UserID,
				nickname: name,
				content:  plainMessage,
			})

			// 控制条数
			if l := len(chatMessages); l > messageL {
				chatMessages[uid] = chatMessages[uid][l-messageL:]
			}

			// 限流
			if time.Now().Before(limit) {
				logrus.Warnf("当前请求限流: %d", uid)
				// ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("已限流，请稍后再试..."))
				return
			}
			limiter := limitManager.Load(uid)
			if !limiter.Acquire() {
				mu.Unlock()
				logrus.Warnf("当前请求限流: %d", uid)
				return
			}

			// 随机回复
			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			if r.Intn(100) < c.Freq {
				histories, e := Db.findHistory(uid, k.Name, historyL)
				if e != nil && !IsSqlNull(e) {
					logrus.Error(e)
					mu.Unlock()
					return
				}

				messages := chatMessages[uid]
				chatMessages[uid] = nil
				mu.Unlock()

				now := time.Now()
				strMessages := make([]string, 0)
				for _, msg := range messages {
					if msg.Time.After(now.Add(-10 * time.Minute)) {
						strMessages = append(strMessages, msg.String())
					}
				}

				if len(strMessages) > 0 {
					completions(ctx, uid, k.Name, strings.Join(strMessages, "\n\n"), histories)
				}
			} else {
				mu.Unlock()
			}
		}
	})

	engine.OnMessage(zero.OnlyToMe, onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.CardOrNickName(ctx.Event.UserID)
		if strings.Contains(name, "Q群管家") {
			return
		}

		c := Db.config()
		plainMessage := ExtPlainMessage(ctx)
		if len(plainMessage) == 0 {
			emojis := []string{"😀", "😂", "🙃", "🥲", "🤔", "🤨"}
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}

		if plainMessage == "reset" || plainMessage == "消除记忆" {
			mu.Lock()
			defer mu.Unlock()
			chatMessages[uid] = nil
			err := Db.cleanHistories(uid, c.Key)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已清除上下文！"))
			return
		}

		// 限流
		if time.Now().Before(limit) {
			logrus.Warnf("当前请求限流: %d", uid)
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("已限流，请稍后再试..."))
			return
		}
		limiter := limitManager.Load(uid)
		if !limiter.Acquire() {
			mu.Unlock()
			logrus.Warnf("当前请求限流: %d", uid)
			return
		}

		histories, err := Db.findHistory(uid, c.Key, historyL)
		if err != nil && !IsSqlNull(err) {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		botN := ""
		if len(zero.BotConfig.NickName) > 0 {
			botN = fmt.Sprintf("@%s ", zero.BotConfig.NickName[0])
		}

		mu.Lock()
		if c.Imitate {
			strMessages := make([]string, 0)
			now := time.Now()
			for _, msg := range chatMessages[uid] {
				if msg.After(now.Add(-10 * time.Minute)) {
					strMessages = append(strMessages, msg.String())
				}
			}

			strMessages = append(strMessages, cacheMessage{now, ctx.Event.UserID, ctx.CardOrNickName(ctx.Event.UserID), botN + plainMessage}.String())
			plainMessage = strings.Join(strMessages, "\n\n")
		} else {
			plainMessage = botN + plainMessage
		}

		chatMessages[uid] = nil
		mu.Unlock()

		completions(ctx, uid, c.Key, plainMessage, histories)
	})

	engine.OnRegex(`^/chat\s+(\S+)\s*(.*)$`, onDb).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		uid := ctx.Event.UserID
		if ctx.Event.GroupID > 0 {
			uid = ctx.Event.GroupID
		}

		name := ctx.CardOrNickName(ctx.Event.UserID)
		if strings.Contains(name, "Q群管家") {
			return
		}

		msg := strings.TrimSpace(matched[2])
		if len(msg) == 0 {
			emojis := []string{"😀", "😂", "🙃", "🥲", "🤔", "🤨"}
			ctx.Send(message.Text(emojis[rand.Intn(len(emojis)-1)]))
			return
		}

		if msg == "reset" || msg == "消除记忆" {
			err := Db.cleanHistories(uid, matched[1])
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已清除上下文！"))
			return
		}

		// 限流
		if time.Now().Before(limit) {
			logrus.Warnf("当前请求限流: %d", uid)
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("已限流，请稍后再试..."))
			return
		}
		limiter := limitManager.Load(uid)
		if !limiter.Acquire() {
			mu.Unlock()
			logrus.Warnf("当前请求限流: %d", uid)
			return
		}

		histories, err := Db.findHistory(uid, matched[1], 100)
		if err != nil && !IsSqlNull(err) {
			ctx.Send(message.Text("ERROR: ", err))
			return
		}

		completions(ctx, uid, matched[1], msg, histories)
	})

	engine.OnRegex(`^/clear\s+(\S+)`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.cleanAllHistories(matched[1]); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send("已清理 ~")
		})

	engine.OnRegex(`^/set-key\s+(\S+)\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			if err := Db.saveKey(Key{Name: matched[1], Content: matched[2]}); err != nil {
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
			c := Db.config()
			content := "***  keys  ***\n\n"

			isEmpty := true
			for _, k := range ks {
				if c.Key != k.Name {
					content += k.Name + "\n"
					isEmpty = false
				}
			}

			if isEmpty {
				content += "   ~ none ~"
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
			content += "Key: " + c.Key + "\n"
			content += "imitate: " + strconv.FormatBool(c.Imitate) + "\n"
			content += "freq: " + strconv.Itoa(c.Freq) + "%\n"
			ctx.Send(message.Text(content))
		})

	engine.OnRegex(`^/config\.proxies\s*(\S*)$`, zero.AdminPermission, onDb).SetBlock(true).
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

	engine.OnRegex(`^/config\.Key\s+(\S+)$`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			c.Key = matched[1]
			err := Db.updateConfig(c)
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}
			ctx.Send(message.Text("已更新gpt Key。"))
		})

	engine.OnRegex(`^/config\.imitate\s(true|false)$`, zero.AdminPermission, onDb).SetBlock(true).
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

	engine.OnRegex(`^/config.freq\s(\d+)`, zero.AdminPermission, onDb).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			matched := ctx.State["regex_matched"].([]string)
			c := Db.config()
			i, err := strconv.Atoi(matched[1])
			if err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}

			if 0 > i && i > 100 {
				ctx.Send(message.Text("取值范围限制在0~100！"))
				return
			}

			c.Freq = i
			if err = Db.updateConfig(c); err != nil {
				ctx.Send(message.Text("ERROR: ", err))
				return
			}

			ctx.Send(message.Text("已修改回复频率为 " + matched[1] + "%。"))
		})
}

// 消息体转换成纯文本内容
func ExtPlainMessage(ctx *zero.Ctx) string {
	sb := new(strings.Builder)
	m := ctx.Event.Message
	for _, val := range m {
		if val.Type == "text" {
			sb.WriteString(val.Data["text"])
		} else if val.Type == "at" {
			qq := val.Data["qq"]
			i32, err := strconv.ParseInt(qq, 10, 64)
			if err != nil {
				logrus.Warn("解析uid失败：", err)
				continue
			}
			sb.WriteString(fmt.Sprintf(" @%s ", ctx.CardOrNickName(i32)))
		}
	}

	result := sb.String()
	if result == "是" {
		return ""
	}
	if matched, _ := regexp.MatchString(`\d+ \d+ \d+`, result); matched {
		return ""
	}
	return result
}

func IsSqlNull(err error) bool {
	return err != nil && err.Error() == "sqlite: null result"
}
