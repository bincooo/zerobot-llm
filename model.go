package gpt

import (
	"github.com/FloatTech/floatbox/ctxext"
	sql "github.com/FloatTech/sqlite"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
	"strconv"
	"sync"
	"time"
)

type db struct {
	sql *sql.Sqlite
	sync.RWMutex
}

type key struct {
	Name    string `db:"name"`
	Content string `db:"value"`
}

type config struct {
	Timestamp int64  `db:"timestamp"`
	Proxies   string `db:"proxies"`
	BaseUrl   string `db:"base-url"`
	Key       string `db:"key"`
	Model     string `db:"model"`
}

type history struct {
	Timestamp int64  `db:"timestamp"`
	Uid       int64  `db:"uid"`
	Name      string `db:"name"`

	UserContent      string `db:"user_content"`
	AssistantContent string `db:"assistant_content"`
}

var (
	Db = &db{
		sql: &sql.Sqlite{},
	}

	onDb = ctxext.DoOnceOnSuccess(func(ctx *zero.Ctx) bool {
		Db.sql.DBPath = engine.DataFolder() + "data.db"
		err := Db.sql.Open(time.Hour * 24)
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		err = Db.sql.Create("key", &key{})
		if err != nil {
			ctx.Send(message.Text("ERROR: ", err))
			return false
		}

		err = Db.sql.Create("history", &history{})
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

func (d *db) addKey(name, content string) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Insert("key", &key{
		Name:    name,
		Content: content,
	})
}

func (d *db) delKey(name string) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Del("key", "where name = '"+name+"'")
}

func (d *db) keys() ([]*key, error) {
	d.Lock()
	defer d.Unlock()
	return sql.FindAll[key](d.sql, "key", "")
}

func (d *db) findHistory(uid int64, name string, count int) ([]*history, error) {
	d.Lock()
	defer d.Unlock()
	return sql.FindAll[history](d.sql, "history", "uid = "+strconv.FormatInt(uid, 10)+" and name = '"+name+"' limit "+strconv.Itoa(count))
}

func (d *db) config() config {
	d.Lock()
	defer d.Unlock()
	var c = config{
		Timestamp: time.Now().Unix(),
		BaseUrl:   "https://api.openai.com",
		Model:     "gpt-4-turbo",
		Key:       "gpt",
	}
	_ = d.sql.Find("config", &c, "")
	return c
}

func (d *db) updateConfig(c config) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Insert("config", c)
}

func (d *db) addHistory(h history) error {
	d.Lock()
	defer d.Unlock()
	return d.sql.Insert("history", h)
}

func (d *db) key(name string) (*key, error) {
	d.Lock()
	defer d.Unlock()
	var k key
	err := d.sql.Find("key", &k, "name = '"+name+"'")
	return &k, err
}
