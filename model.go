package llm

import (
	"strconv"
	"sync"
	"time"

	"github.com/FloatTech/floatbox/ctxext"
	"github.com/wdvxdr1123/ZeroBot/message"

	sql "github.com/FloatTech/sqlite"
	zero "github.com/wdvxdr1123/ZeroBot"
)

type DB struct {
	sql *sql.Sqlite
	sync.RWMutex
}

type Key struct {
	Name    string `DB:"name"`
	Content string `DB:"value"`
}

type config struct {
	Timestamp int64  `DB:"timestamp"`
	Proxies   string `DB:"proxies"`
	BaseUrl   string `DB:"base_url"`
	Key       string `DB:"key"`
	Model     string `DB:"model"`
	Imitate   bool   `DB:"imitate"` // 模仿模式
	Freq      int    `DB:"freq"`    // 模仿模式自动应答频率0~100
}

type History struct {
	Timestamp int64  `DB:"timestamp"`
	Uid       int64  `DB:"uid"`
	Name      string `DB:"name"`

	UserContent      string `DB:"user_content"`
	AssistantContent string `DB:"assistant_content"`
}

var (
	Db = &DB{
		sql: &sql.Sqlite{},
	}

	onDb = ctxext.DoOnceOnSuccess(func(ctx *zero.Ctx) bool {
		Db.sql.DBPath = engine.DataFolder() + "data.DB"
		err := Db.sql.Open(time.Hour * 24)
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		err = Db.sql.Create("Key", &Key{})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		err = Db.sql.Create("History", &History{})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		err = Db.sql.Create("config", &config{})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		return true
	})
)

func (d *DB) saveKey(k Key) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Insert("Key", &k)
}

func (d *DB) delKey(name string) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Del("Key", "where name = '"+name+"'")
}

func (d *DB) keys() ([]*Key, error) {
	d.Lock()
	defer d.Unlock()
	return sql.FindAll[Key](d.sql, "Key", "")
}

func (d *DB) config() config {
	d.Lock()
	defer d.Unlock()
	var c = config{
		Timestamp: -1,
		BaseUrl:   "https://api.openai.com",
		Model:     "gpt-4-turbo",
		Key:       "auto",
		Freq:      25,
	}
	_ = d.sql.Find("config", &c, "timestamp = -1")
	return c
}

func (d *DB) updateConfig(c config) error {
	d.Lock()
	defer d.Unlock()
	c.Timestamp = -1
	return d.sql.Insert("config", &c)
}

func (d *DB) saveHistory(h History) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Insert("History", &h)
}

func (d *DB) findHistory(uid int64, name string, count int) ([]*History, error) {
	d.Lock()
	defer d.Unlock()
	return sql.FindAll[History](d.sql, "History", "where uid = "+strconv.FormatInt(uid, 10)+" and name = '"+name+"' order by timestamp desc limit "+strconv.Itoa(count))
}

func (d *DB) cleanHistories(uid int64, name string) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Del("History", "where uid = "+strconv.FormatInt(uid, 10)+" and name = '"+name+"'")
}

func (d *DB) cleanAllHistories(name string) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Del("History", "where name = '"+name+"'")
}

func (d *DB) key(name string) (*Key, error) {
	d.Lock()
	defer d.Unlock()
	var k Key
	err := d.sql.Find("Key", &k, "where name = '"+name+"'")
	return &k, err
}
