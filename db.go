package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glog "gorm.io/gorm/logger"
)

const (
	UserTypeUsual uint = 0
	UserTypeAdmin uint = 1
)

// Custom logrus based logger for gorm
type gormlog struct {
	log *logrus.Logger
}

func (gl gormlog) LogMode(lvl glog.LogLevel) glog.Interface {
	return gl
}

func (gl gormlog) Info(_ context.Context, s string, v ...any) {
	gl.log.Infof(s, v...)
}

func (gl gormlog) Warn(_ context.Context, s string, v ...any) {
	gl.log.Warnf(s, v...)
}

func (gl gormlog) Error(_ context.Context, s string, v ...any) {
	gl.log.Errorf(s, v...)
}

func (gl gormlog) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
}

type CardFields struct {
	Name        string `form:"name" binding:"required"`
	Company     string `form:"company"`
	Position    string `form:"position"`
	Description string `form:"description"`
	Phone       string `form:"phone"`
	Email       string `form:"email" binding:"omitempty,email"`
	Telegram    string `form:"telegram"`
	Whatsapp    string `form:"whatsapp"`
	VK          string `form:"vk"`
	IsHidden    bool   `form:"hidden"`
}

type Card struct {
	ID          uint `gorm:"primaryKey"`
	Owner       uint
	Fields      CardFields `gorm:"embedded"`
	AvatarExist bool
	LogoExist   bool
}

type ByID []Card

func (a ByID) Len() int           { return len(a) }
func (a ByID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByID) Less(i, j int) bool { return a[i].ID < a[j].ID }

type User struct {
	ID         uint `gorm:"primaryKey"`
	ProviderID string
	Name       string
	Type       uint
}

type PGDB struct {
	DB      *gorm.DB
	Storage *BlobStorage
}

func (db *PGDB) SignUser(pid, name string) (string, error) {
	var user User

	result := db.DB.Where("provider_id = ?", pid).First(&user)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			user = User{ProviderID: pid, Name: name}
			result = db.DB.Create(&user)
		}
	}

	if result.Error != nil {
		return "", result.Error
	}

	return strconv.FormatUint(uint64(user.ID), 10), nil
}

func (db *PGDB) GetUser(user *User) error {
	result := db.DB.First(user)
	return result.Error
}

func (db *PGDB) DeleteUser(id uint) error {
	user := User{ID: uint(id)}

	result := db.DB.Delete(&user)
	if result.Error != nil {
		return result.Error
	}

	cards, err := db.ListCards(id)
	if err != nil {
		return err
	}
	for _, card := range cards {
		e := db.DeleteCard(card.ID)
		if e != nil {
			err = e
		}
	}
	return err
}

func (db *PGDB) CreateCard(owner uint, fields CardFields) (Card, error) {
	card := Card{Owner: uint(owner), Fields: fields}
	result := db.DB.Create(&card)
	if result.Error != nil {
		return card, result.Error
	}
	return card, nil
}

func (db *PGDB) UpdateCard(card Card) error {
	result := db.DB.Save(&card)
	return result.Error
}

func (db *PGDB) GetCard(id uint) (Card, error) {
	card := Card{ID: uint(id)}
	result := db.DB.First(&card)
	return card, result.Error
}

func (db *PGDB) DeleteCard(id uint) error {
	card := Card{ID: uint(id)}
	result := db.DB.Delete(&card)

	db.Storage.DelKey(context.Background(), fmt.Sprintf("media/avatar/%d", id))
	db.Storage.DelKey(context.Background(), fmt.Sprintf("media/logo/%d", id))

	return result.Error
}

func (db *PGDB) ListCards(uid uint) ([]Card, error) {
	cards := []Card{}

	result := db.DB.Where(&Card{Owner: uid}).Find(&cards)
	sort.Sort(ByID(cards))
	return cards, result.Error
}

func (db *PGDB) ListUsers() ([]User, error) {
	users := []User{}

	result := db.DB.Find(&users)
	return users, result.Error
}

func SetupDB(store *BlobStorage, log *logrus.Logger) *PGDB {
	host := os.Getenv("PG_HOST")
	port := os.Getenv("PG_PORT")
	user := os.Getenv("PG_USER")
	password := os.Getenv("PG_PASSWORD")
	dbname := os.Getenv("PG_NAME")
	sslmode := os.Getenv("PG_SSLMODE")
	timezone := os.Getenv("PG_TIMEZONE")

	if timezone != "" {
		timezone = "TimeZone=" + timezone
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s %s",
		host, port, user, password, dbname, sslmode, timezone,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlog{log},
	})

	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}

	err = db.AutoMigrate(&User{})
	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}
	err = db.AutoMigrate(&Card{})
	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}

	return &PGDB{DB: db, Storage: store}
}
